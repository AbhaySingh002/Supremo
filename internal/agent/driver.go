package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type AgentPhaseKind string

const (
	PhaseIdle    AgentPhaseKind = "idle"
	PhaseRunning AgentPhaseKind = "running"
)

type AgentPhase struct {
	Kind AgentPhaseKind
	Turn int
	Step int
}

type stepStatus int

const (
	stepContinue stepStatus = iota
	stepComplete
	stepBlocked
	stepFatal
)

func (a *Agent) Followup(session *Session, message models.Message) {
	a.hooks.NotifyUserInput(runtime.UserInputFollowup)
	a.inbox.EnqueueTurn(newTurnRequest(session, message, turnConfig{
		stream: true,
		makeRequest: func() ContextRequest {
			return ContextRequest{Session: session, Objective: message.Content, Profile: protocol.Execution}
		},
	}))
}

func (a *Agent) Steer(message models.Message) {
	a.hooks.NotifyUserInput(runtime.UserInputSteer)
	a.inbox.StageNextStep(message)
}

func (a *Agent) Cancel() {
	a.mu.Lock()
	cancel := a.turnCancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *Agent) enqueueAndDrive(ctx context.Context, req *TurnRequest) TurnResult {
	if req.ctx == nil {
		req.ctx = ctx
	}
	a.inbox.EnqueueTurn(req)
	if a.tryStartDriver() {
		go a.drive()
	}
	return req.wait()
}

func (a *Agent) tryStartDriver() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.driverRunning {
		return false
	}
	a.driverRunning = true
	a.driverDone = make(chan struct{})
	a.phase.Kind = PhaseRunning
	return true
}

func (a *Agent) drive() {
	for {
		req := a.inbox.ClaimTurn()
		if req == nil {
			if a.stopIfIdle() {
				return
			}
			continue
		}
		ctx := req.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		out := a.runTurn(ctx, req)
		req.complete(out)
	}
}

func (a *Agent) stopIfIdle() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inbox.mu.Lock()
	defer a.inbox.mu.Unlock()
	if len(a.inbox.nextTurn) > 0 {
		return false
	}
	a.driverRunning = false
	a.phase.Kind = PhaseIdle
	a.turnCancel = nil
	close(a.driverDone)
	return true
}

func (a *Agent) waitIdle() {
	if a == nil {
		return
	}
	a.mu.Lock()
	done := a.driverDone
	running := a.driverRunning
	a.mu.Unlock()
	if running && done != nil {
		<-done
	}
}

func (a *Agent) submitTurn(ctx context.Context, req *TurnRequest) TurnResult {
	return a.enqueueAndDrive(ctx, req)
}

func (a *Agent) runTurn(ctx context.Context, req *TurnRequest) (out TurnResult) {
	session := req.Session
	if session == nil && req.Config.makeRequest != nil {
		session = req.Config.makeRequest().Session
	}
	if session == nil {
		out.Err = errors.New("session is required")
		return out
	}
	turnCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.phase.Turn++
	a.phase.Step = 0
	a.turnCancel = cancel
	turn := a.phase.Turn
	a.mu.Unlock()
	defer cancel()

	if session.NeedsName && strings.TrimSpace(req.Message.Content) != "" {
		if err := a.nameSession(session, req.Message.Content); err != nil && a.debug {
			a.emit(ProgressEvent{Kind: ProgressDebug, Message: "Session naming failed: " + err.Error()})
		}
	}
	if !a.ephemeral {
		if runtime, ok := a.provider.(interface {
			GetRuntimeConfig() *providers.RuntimeConfig
		}); ok && runtime.GetRuntimeConfig() != nil {
			provider, model, _, _, _ := runtime.GetRuntimeConfig().Get()
			session.Provider, session.Model = provider, model
			if err := session.Save(a.workspace); err != nil {
				out.Err = fmt.Errorf("persist provider metadata: %w", err)
				return out
			}
		}
	}
	if err := a.syncSessionSurface(turnCtx, session); err != nil {
		out.Err = err
		return out
	}
	if err := a.repairAndFold(turnCtx, session); err != nil {
		out.Err = err
		return out
	}
	if err := a.appendHarness(turnCtx, session, EventTurnStart, sessionlog.TurnStartPayload{Turn: turn}); err != nil {
		out.Err = fmt.Errorf("persist turn start: %w", err)
		return out
	}
	if strings.TrimSpace(req.Message.Content) != "" || len(req.Message.ToolCalls) > 0 {
		if err := a.appendConfigured(turnCtx, session, req.Config, req.Message); err != nil {
			out.Err = err
			return a.finishTurn(turnCtx, session, turn, "error", out)
		}
	}
	if !req.Config.sideAnswer && strings.TrimSpace(req.Message.Content) != "" {
		if err := a.recordObjective(turnCtx, session, req.Message.Content); err != nil {
			out.Err = fmt.Errorf("persist objective: %w", err)
			return a.finishTurn(turnCtx, session, turn, "error", out)
		}
	}

	for {
		if err := turnCtx.Err(); err != nil {
			out.Err = err
			return a.finishTurn(turnCtx, session, turn, "aborted", out)
		}
		status, _, text, err := a.runStep(turnCtx, session, req.Config)
		out.Text = text
		switch status {
		case stepContinue:
			continue
		case stepBlocked:
			out.Blocked = true
			return a.finishTurn(turnCtx, session, turn, "blocked", out)
		case stepFatal:
			out.Err = err
			return a.finishTurn(turnCtx, session, turn, "error", out)
		default:
			if a.inbox.HasNextStep() {
				continue
			}
			out.Err = err
			return a.finishTurn(turnCtx, session, turn, "completed", out)
		}
	}
}

func (a *Agent) finishTurn(ctx context.Context, session *Session, turn int, reason string, out TurnResult) TurnResult {
	if err := a.appendTerminalHarness(ctx, session, EventTurnEnd, sessionlog.TurnEndPayload{Turn: turn, Reason: reason}); err != nil {
		out.Err = errors.Join(out.Err, fmt.Errorf("persist turn end: %w", err))
	}
	return out
}

func (a *Agent) runStep(ctx context.Context, session *Session, cfg turnConfig) (status stepStatus, parsed *parser.Response, text string, err error) {
	a.mu.Lock()
	a.phase.Step++
	step := a.phase.Step
	turn := a.phase.Turn
	a.mu.Unlock()
	a.emit(ProgressEvent{Kind: ProgressIteration, Iteration: step})
	if err := a.appendHarness(ctx, session, EventStepStart, sessionlog.StepStartPayload{Turn: turn, Step: step}); err != nil {
		return stepFatal, nil, "", fmt.Errorf("persist step start: %w", err)
	}
	defer func() {
		reason := "completed"
		if status == stepContinue {
			reason = "continued"
		} else if status == stepBlocked {
			reason = "blocked"
		} else if status == stepFatal {
			reason = "error"
		}
		if endErr := a.appendTerminalHarness(ctx, session, EventStepEnd, sessionlog.StepEndPayload{Turn: turn, Step: step, Reason: reason}); endErr != nil {
			status = stepFatal
			err = errors.Join(err, fmt.Errorf("persist step end: %w", endErr))
		}
	}()

	stepCtx := a.stepContext(ctx, session)

	for _, msg := range a.inbox.ClaimNextStep() {
		if err := a.appendConfigured(stepCtx, session, cfg, msg); err != nil {
			return stepFatal, nil, "", err
		}
	}

	request := ContextRequest{Session: session}
	if cfg.makeRequest != nil {
		request = cfg.makeRequest()
		request.Session = session
	}
	if cfg.taskID != "" {
		request.TaskID = cfg.taskID
	}
	request.Turn, request.Step = turn, step
	var prompt *models.Prompt
	var completion *providers.Completion
	prompt, parsed, completion, err = a.completeStep(stepCtx, session, request, cfg)
	if err != nil {
		return stepFatal, nil, "", err
	}
	if parsed == nil {
		return stepFatal, nil, "", errors.New("model returned empty response")
	}
	if err := a.recordContextUsage(ctx, prompt, completion.Usage); err != nil {
		return stepFatal, parsed, "", fmt.Errorf("persist context usage: %w", err)
	}

	if parsed.TurnProgress == nil && completion.Text != "" {
		parsed.TurnProgress = parser.ExtractAssistantTurnProgress(completion.Text)
	}
	assistant := models.Message{Role: models.RoleAssistant, Content: completion.Text, ToolCalls: append([]models.ToolCall(nil), parsed.ToolCalls...), TurnProgress: parsed.TurnProgress}
	if cfg.sideAnswer {
		assistant.Content = strings.TrimSpace(completion.Text)
	}
	if err := a.appendAssistantMessage(ctx, session, cfg, assistant, completion.SourceEventSeqs); err != nil {
		return stepFatal, parsed, "", err
	}
	if len(parsed.ToolCalls) > 0 {
		status, err := a.finishTools(stepCtx, session, cfg, prompt, parsed)
		return status, parsed, strings.TrimSpace(completion.Text), err
	}

	if parsed.TurnProgress != nil {
		a.persistFocusAfterTurn(ctx, session.ID, request.TaskID, parsed.TurnProgress, "", true, "")
	}
	text = strings.TrimSpace(completion.Text)
	a.LogPostTurnStateTransition(session, request.TaskID, step, nil, nextGoalFrom(parsed))
	return stepComplete, parsed, text, nil
}

// stepContext evaluates Plan Mode at the Step boundary. An approved
// exit_plan_mode therefore changes the following Step's safety boundary
// without weakening previous planning Steps.
func (a *Agent) stepContext(ctx context.Context, session *Session) context.Context {
	if session != nil && session.PlanModeActive() {
		return tools.WithResearchOnly(ctx)
	}
	return ctx
}

func (a *Agent) completeStep(ctx context.Context, session *Session, request ContextRequest, cfg turnConfig) (*models.Prompt, *parser.Response, *providers.Completion, error) {
	var store state.EventStore
	if !a.ephemeral && a.workspace != "" {
		opened, err := state.Open(a.workspace)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("open context store: %w", err)
		}
		store = opened
	}

	var parsed *parser.Response
	var prompt *models.Prompt
	var completion *providers.Completion
	var err error
	limit := a.providerContextLimit()
	recoveryPasses := 0

	for {
		prepared, prepareErr := a.prepareContext(ctx, request)
		if prepareErr != nil {
			return nil, nil, nil, fmt.Errorf("failed to build context: %w", prepareErr)
		}
		prompt = prepared.Prompt
		if err = freezeProviderRequest(prompt); err != nil {
			return nil, nil, nil, err
		}
		if a.pressureManager != nil && session != nil {
			measurement := a.pressureManager.Measure(session, prompt, limit)
			if measurement.IsPressured() {
				if recoveryPasses >= 3 {
					return prompt, nil, nil, fmt.Errorf("%w after three recovery passes", ErrContextNotConverging)
				}
				beforeGeneration := surfaceGeneration(session)
				result, pressureErr := a.pressureManager.ResolvePressure(ctx, store, session, a.provider, prompt, measurement)
				if pressureErr != nil {
					return prompt, nil, nil, fmt.Errorf("resolve context pressure: %w", pressureErr)
				}
				if err := validatePressureResult(result, beforeGeneration); err != nil {
					return prompt, nil, nil, err
				}
				recoveryPasses++
				continue
			}
		}
		if err = prepared.Commit(ctx); err != nil {
			return nil, nil, nil, fmt.Errorf("commit provider request: %w", err)
		}
		parsed = nil
		completion, err = a.completeWithRetry(ctx, session, prompt, cfg.stream, func(completion *providers.Completion) error {
			var parseErr error
			parsed, parseErr = a.parseCompletion(prompt, completion)
			if parseErr != nil {
				return parseErr
			}
			if cfg.sideAnswer {
				if len(parsed.ToolCalls) > 0 || strings.TrimSpace(completion.Text) == "" {
					return fmt.Errorf("side answer requested an action instead of answering from context")
				}
				return nil
			}
			return nil
		})
		if !providers.IsContextOverflow(err) {
			break
		}
		if a.pressureManager == nil || session == nil || recoveryPasses >= 3 {
			break
		}
		beforeGeneration := surfaceGeneration(session)
		result, recoveryErr := a.pressureManager.RecoverOverflow(ctx, store, session, a.provider, prompt, limit)
		if recoveryErr != nil {
			return prompt, nil, nil, errors.Join(fmt.Errorf("provider request: %w", err), fmt.Errorf("recover provider overflow: %w", recoveryErr))
		}
		if validationErr := validatePressureResult(result, beforeGeneration); validationErr != nil {
			return prompt, nil, nil, errors.Join(fmt.Errorf("provider request: %w", err), validationErr)
		}
		recoveryPasses++
	}
	if err != nil {
		return prompt, nil, nil, fmt.Errorf("provider request: %w", err)
	}
	return prompt, parsed, completion, nil
}

func validatePressureResult(result PressureResult, beforeGeneration uint64) error {
	if !result.Changed || result.SurfaceGeneration <= beforeGeneration || result.AfterTokens >= result.BeforeTokens {
		return fmt.Errorf("%w: tokens %d -> %d, surface generation %d -> %d", ErrContextNotConverging, result.BeforeTokens, result.AfterTokens, beforeGeneration, result.SurfaceGeneration)
	}
	return nil
}

func (a *Agent) finishTools(ctx context.Context, session *Session, cfg turnConfig, prompt *models.Prompt, parsed *parser.Response) (stepStatus, error) {
	logging.Info("Executing %d tool call(s)", len(parsed.ToolCalls))
	toolCtx := promptToolContext(ctx, prompt)
	taskID := cfg.taskID
	if taskID == "" && session != nil {
		taskID = session.ActiveTaskID
	}
	opts := ToolExecutionOptions{
		TaskID: taskID,
	}
	summary := a.executeAll(toolCtx, session, parsed.ToolCalls, opts)

	lastAction := ""
	if len(parsed.ToolCalls) > 0 {
		lastAction = parsed.ToolCalls[0].Name
	}
	lastFailure := ""
	for _, obs := range summary.Observations {
		if !obs.Success {
			lastFailure = obs.Output
			if len(lastFailure) > 500 {
				lastFailure = lastFailure[:500]
			}
		}
	}
	sessionID := ""
	if session != nil {
		sessionID = session.ID
	}
	a.persistFocusAfterTurn(ctx, sessionID, taskID, parsed.TurnProgress, lastAction, summary.ReusedCount < len(parsed.ToolCalls), lastFailure)
	a.LogPostTurnStateTransition(session, taskID, a.phase.Step, observationRepoChanges(summary.Observations), nextGoalFrom(parsed))

	if summary.Err != nil {
		switch summary.Outcome {
		case tools.ToolOutcomeSuccess, tools.ToolOutcomeRecoverable:
			return stepContinue, nil
		case tools.ToolOutcomeCancelled, tools.ToolOutcomeFatal:
			return stepFatal, summary.Err
		default:
			return stepContinue, nil
		}
	}
	return stepContinue, nil
}

func (a *Agent) appendConfigured(ctx context.Context, session *Session, cfg turnConfig, message models.Message) error {
	if cfg.sideAnswer || cfg.taskID != "" {
		return a.appendMessageForTask(ctx, session, cfg.taskID, message)
	}
	return a.appendMessage(ctx, session, message)
}

func (a *Agent) repairAndFold(ctx context.Context, session *Session) error {
	if session == nil {
		return nil
	}
	extra := repairSessionTail(session.events)
	if len(extra) == 0 {
		return nil
	}
	if a.transcript == nil {
		for i := range extra {
			extra[i].Seq = int64(len(session.events))
			if err := session.applyEvent(extra[i]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, event := range extra {
		if event.Type == EventToolResult {
			if err := a.transcript.Append(ctx, session.ID, event.Message); err != nil {
				return err
			}
			continue
		}
		if err := a.transcript.AppendHarnessEvent(ctx, session.ID, event.Type, event.Data); err != nil {
			return err
		}
	}
	return a.transcript.SyncSurface(ctx, session)
}

func (a *Agent) appendHarness(ctx context.Context, session *Session, kind EventType, data any) error {
	if session == nil || a.transcript == nil {
		return nil
	}
	if err := a.transcript.AppendHarnessEvent(ctx, session.ID, kind, data); err != nil {
		return err
	}
	return a.transcript.SyncSurface(ctx, session)
}

func (a *Agent) appendTerminalHarness(ctx context.Context, session *Session, kind EventType, data any) error {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	return a.appendHarness(terminalCtx, session, kind, data)
}

func (a *Agent) appendTerminalConfigured(ctx context.Context, session *Session, cfg turnConfig, message models.Message) error {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	return a.appendConfigured(terminalCtx, session, cfg, message)
}

func terminalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
}

func repairSessionTail(events []SessionEvent) []SessionEvent {
	return sessionlog.RepairTail(events)
}
