package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/repository"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ContextRequest describes the reason a provider prompt is being compiled.
type ContextRequest struct {
	Session              *Session
	TaskID               string
	Turn                 int
	Step                 int
	Objective            string
	OverflowPressure     int
	Mode                 tools.ToolMode
	RequiredCapabilities []string
	Profile              protocol.Profile
}

type ToolObservation struct {
	Name       string
	Status     tools.ToolStatus
	Success    bool
	ArtifactID string
}

// contextLifecycle is the private context pipeline used by Agent. Production
// always receives RealContextBuilder; tests can substitute this narrow seam.
type contextLifecycle interface {
	Compile(context.Context, ContextRequest) (*models.Prompt, error)
	RecordObjective(ctx context.Context, sessionID, taskID, objective string) error
	RecordUsage(context.Context, *models.Prompt, providers.Usage) error
	ObserveTool(context.Context, string, string, ToolObservation) error
}

type PreparedContext struct {
	Prompt *models.Prompt
	commit func(context.Context) error
}

func (p *PreparedContext) Commit(ctx context.Context) error {
	if p == nil || p.commit == nil {
		return nil
	}
	return p.commit(ctx)
}

type contextPreparer interface {
	Prepare(context.Context, ContextRequest) (*PreparedContext, error)
}

// TranscriptStore keeps durable transcript data separate from prompt selection.
type TranscriptStore interface {
	Append(ctx context.Context, sessionID string, msg models.Message) error
	AppendHarnessEvent(ctx context.Context, sessionID string, kind EventType, data any) error
	ReadAllTranscript(ctx context.Context, sessionID string) ([]models.Message, error)
	Clear(ctx context.Context, sessionID string) error
	SyncSurface(ctx context.Context, session *Session) error
}

// Agent coordinates the execution of the ReAct loop across different subsystems.
type Agent struct {
	provider            providers.Provider
	toolManager         *tools.Manager
	contextLifecycle    contextLifecycle
	transcript          TranscriptStore
	workspace           string
	ephemeral           bool
	debug               bool
	progress            func(ProgressEvent)
	repository          *repository.Service
	retryWait           func(context.Context, time.Duration) error
	workingMemory       *WorkingMemoryManager
	mu                  sync.Mutex
	driverRunning       bool
	driverDone          chan struct{}
	phase               AgentPhase
	inbox               Inbox
	turnCancel          context.CancelFunc
	hooks               *runtime.HookSet
	retryPolicy         runtime.RetryPolicy
	pressureManager     contextPressureManager
	requestHeaderSeen   bool
	requestHeaderDigest string
	maxParallelTools    int
}

func (a *Agent) providerContextLimit() int {
	if a == nil || a.provider == nil {
		return DefaultFallbackLimit
	}
	if cl, ok := a.provider.(interface{ ContextLimit() int }); ok {
		if limit := cl.ContextLimit(); limit > 0 {
			return limit
		}
	}
	return DefaultFallbackLimit
}

// WorkingMemory returns the working memory manager for the agent workspace.
func (a *Agent) WorkingMemory() *WorkingMemoryManager {
	if a.workingMemory != nil {
		return a.workingMemory
	}
	if store, err := state.Open(a.workspace); err == nil {
		a.workingMemory = NewWorkingMemoryManager(store)
		return a.workingMemory
	}
	return nil
}

// SetRepository makes workspace discovery available to tool calls in every task.
func (a *Agent) SetRepository(service *repository.Service) { a.repository = service }

// SetRetryPolicy configures an explicit step retry policy on the agent.
func (a *Agent) SetRetryPolicy(policy runtime.RetryPolicy) { a.retryPolicy = policy }

// SetProgress installs an interactive lifecycle reporter.
func (a *Agent) SetProgress(report func(ProgressEvent)) {
	a.progress = report
	if a.toolManager != nil {
		a.toolManager.SetReporter(a.reportTool)
	}
}

// ApprovePendingTool releases one mutating tool call waiting for confirmation.
func (a *Agent) ApprovePendingTool() bool {
	return a != nil && a.toolManager != nil && a.toolManager.Approve()
}

// ApprovePendingToolWithInput executes an edited, revalidated approval request.
func (a *Agent) ApprovePendingToolWithInput(input any) bool {
	return a != nil && a.toolManager != nil && a.toolManager.ApproveWithInput(input)
}

// DenyPendingTool rejects one mutating tool call waiting for confirmation.
func (a *Agent) DenyPendingTool(reason string) bool {
	return a != nil && a.toolManager != nil && a.toolManager.Deny(reason)
}

func (a *Agent) ResolvePendingTool(interactionID string, resolution tools.ApprovalResolution) error {
	if a == nil || a.toolManager == nil {
		return errors.New("tool manager is unavailable")
	}
	return a.toolManager.ResolveApproval(interactionID, resolution)
}

func (a *Agent) hasPendingApproval() bool {
	return a != nil && a.toolManager != nil && a.toolManager.HasPendingApproval()
}

func (a *Agent) taskContext(ctx context.Context, session *Session) (context.Context, context.CancelFunc, error) {
	// Tasks end when their caller cancels them or they reach a durable semantic
	// terminal state; tool count and elapsed time are not completion criteria.
	ctx, cancel := context.WithCancel(ctx)
	ctx = tools.WithWorkspace(ctx, a.workspace)
	ctx = tools.WithDryRun(ctx, session.DryRun)
	ctx = tools.WithCheckpointSession(ctx, session.ID, session.RewindEnabled())
	ctx = repository.WithService(ctx, a.repository)
	if session.Origin == "subagent" {
		ctx = tools.WithDelegated(ctx)
		if session.DelegationScope == SubagentScopeLocalRead {
			ctx = tools.WithResearchOnly(tools.WithReadOnly(ctx))
		}
	}
	if !a.ephemeral {
		store, err := state.Open(a.workspace)
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("open context store: %w", err)
		}
		ctx = tools.WithLifecycleRecorder(ctx, &stateRecorder{store: store, repository: a.repository, root: a.workspace, sessionID: session.ID})
	}
	if session.ApprovalMode != "" {
		ctx = tools.WithApprovalMode(ctx, session.ApprovalMode)
	}
	return ctx, cancel, nil
}

// appendMemory tags durable work with its selected live task. Provider-facing
// messages stay unchanged; this is solely local context bookkeeping.
func (a *Agent) appendMemory(ctx context.Context, session *Session, role models.Role, content string) error {
	return a.appendMessage(ctx, session, models.Message{Role: role, Content: content})
}

func (a *Agent) appendMessage(ctx context.Context, session *Session, message models.Message) error {
	if a.transcript == nil || session == nil {
		return nil
	}
	message.TaskID = session.ActiveTaskID
	if err := a.transcript.Append(ctx, session.ID, message); err != nil {
		return err
	}
	return a.syncSessionSurface(ctx, session)
}

func (a *Agent) appendMessageForTask(ctx context.Context, session *Session, taskID string, message models.Message) error {
	if a.transcript == nil || session == nil {
		return nil
	}
	message.TaskID = taskID
	if err := a.transcript.Append(ctx, session.ID, message); err != nil {
		return err
	}
	return a.syncSessionSurface(ctx, session)
}

type assistantMessageAppender interface {
	AppendAssistantMessage(context.Context, string, models.Message, []int64) error
}

func (a *Agent) appendAssistantMessage(ctx context.Context, session *Session, cfg turnConfig, message models.Message, sourceEventSeqs []int64) error {
	if a.transcript == nil || session == nil || len(sourceEventSeqs) == 0 {
		return a.appendConfigured(ctx, session, cfg, message)
	}
	if cfg.sideAnswer || cfg.taskID != "" {
		message.TaskID = cfg.taskID
	} else {
		message.TaskID = session.ActiveTaskID
	}
	if appender, ok := a.transcript.(assistantMessageAppender); ok {
		if err := appender.AppendAssistantMessage(ctx, session.ID, message, sourceEventSeqs); err != nil {
			return err
		}
		return a.syncSessionSurface(ctx, session)
	}
	return a.appendConfigured(ctx, session, cfg, message)
}

func (a *Agent) syncSessionSurface(ctx context.Context, session *Session) error {
	if a.transcript == nil || session == nil {
		return nil
	}
	return a.transcript.SyncSurface(ctx, session)
}

// NewAgent constructs a new Agent instance.
func NewAgent(
	provider providers.Provider,
	toolManager *tools.Manager,
	contextBuilder *RealContextBuilder,
	transcript TranscriptStore,
	hooks *runtime.HookSet,
) *Agent {
	workspace, _ := os.Getwd()
	if hooks == nil {
		hooks = runtime.NewHookSet()
	}
	var lifecycle contextLifecycle
	if contextBuilder != nil {
		lifecycle = contextBuilder
	}
	return &Agent{
		provider:         provider,
		toolManager:      toolManager,
		contextLifecycle: lifecycle,
		transcript:       transcript,
		workspace:        workspace,
		retryWait:        waitForProviderRetry,
		hooks:            hooks,
		retryPolicy:      runtime.NewDefaultRetryPolicy(),
		pressureManager:  NewRealContextPressureManager(nil, nil, nil),
		maxParallelTools: 4,
	}
}

// SetPlanMode is the only CLI-facing Plan Mode transition. Session retains the
// durable state, while Agent owns when a runtime transition is allowed.
func (a *Agent) SetPlanMode(ctx context.Context, session *Session, active bool) error {
	if a == nil {
		return fmt.Errorf("agent is required")
	}
	if session == nil {
		return fmt.Errorf("session is required")
	}
	if a.workspace == "" {
		return fmt.Errorf("agent workspace is required")
	}
	return session.setPlanMode(ctx, a.workspace, active)
}

// ClearMemory clears a session without exposing the memory implementation.
func (a *Agent) ClearMemory(ctx context.Context, sessionID string) error {
	return a.transcript.Clear(ctx, sessionID)
}

func (a *Agent) prepareContext(ctx context.Context, request ContextRequest) (*PreparedContext, error) {
	if err := a.syncSessionSurface(ctx, request.Session); err != nil {
		return nil, err
	}
	if a.contextLifecycle == nil {
		return nil, fmt.Errorf("agent context pipeline is required")
	}
	if preparer, ok := a.contextLifecycle.(contextPreparer); ok {
		return preparer.Prepare(ctx, request)
	}
	prompt, err := a.contextLifecycle.Compile(ctx, request)
	if err != nil {
		return nil, err
	}
	return &PreparedContext{Prompt: prompt}, nil
}

func (a *Agent) recordObjective(ctx context.Context, session *Session, objective string) error {
	if strings.TrimSpace(objective) == "" {
		return nil
	}
	if a.contextLifecycle == nil {
		return fmt.Errorf("agent context pipeline is required")
	}
	return a.contextLifecycle.RecordObjective(ctx, session.ID, session.ActiveTaskID, objective)
}

func (a *Agent) recordContextUsage(ctx context.Context, prompt *models.Prompt, usage providers.Usage) error {
	if a.contextLifecycle == nil {
		return nil
	}
	return a.contextLifecycle.RecordUsage(ctx, prompt, usage)
}

func (a *Agent) observeContextTool(ctx context.Context, session *Session, observation ToolObservation) error {
	if session == nil {
		return nil
	}
	return a.observeContextToolForTask(ctx, session, session.ActiveTaskID, observation)
}

func (a *Agent) observeContextToolForTask(ctx context.Context, session *Session, taskID string, observation ToolObservation) error {
	if a.contextLifecycle == nil || session == nil {
		return nil
	}
	return a.contextLifecycle.ObserveTool(ctx, session.ID, taskID, observation)
}

func promptToolContext(ctx context.Context, prompt *models.Prompt) context.Context {
	if prompt == nil || prompt.ActiveTools == nil {
		return ctx
	}
	return tools.WithActiveTools(ctx, prompt.ActiveTools)
}

// ReadAllTranscript returns the complete persisted transcript for one session.
func (a *Agent) ReadAllTranscript(ctx context.Context, sessionID string) ([]models.Message, error) {
	messages, err := a.transcript.ReadAllTranscript(ctx, sessionID)
	return append([]models.Message(nil), messages...), err
}

// Checkpoints returns the current chat's rewind history.
func (a *Agent) Checkpoints(root, sessionID string) ([]tools.CheckpointSummary, error) {
	return tools.ListCheckpoints(root, sessionID)
}

// Rewind restores covered workspace files to immediately before a checkpoint.
func (a *Agent) Rewind(ctx context.Context, root, sessionID, checkpointID string, force bool) (tools.RewindResult, error) {
	result, err := tools.Rewind(ctx, root, sessionID, checkpointID, force)
	if err != nil || result.Restored == 0 {
		return result, err
	}
	store, err := state.Open(root)
	if err != nil {
		return result, err
	}
	revision, err := store.ObserveWorkspace(context.WithoutCancel(ctx), workspaceSnapshot(ctx, root, "rewind"))
	if err != nil {
		return result, err
	}
	_, err = store.AppendEvent(context.WithoutCancel(ctx), state.EventInput{SessionID: sessionID, Type: "workspace.rewound", Payload: map[string]any{"checkpoint_id": checkpointID, "workspace_revision": revision.ID, "restored": result.Restored}})
	return result, err
}

// DeleteSession permanently removes one session and its private history.
func (a *Agent) DeleteSession(ctx context.Context, root, sessionID string) error {
	store, err := state.Open(root)
	if err != nil {
		return err
	}
	if err := store.ArchiveSession(ctx, sessionID); err != nil {
		return err
	}
	return a.transcript.Clear(ctx, sessionID)
}

// SetDebug enables or disables debug logging for the agent loop.
func (a *Agent) SetDebug(enabled bool) {
	a.debug = enabled
}

// Debug reports whether diagnostic lifecycle entries are enabled.
func (a *Agent) Debug() bool { return a.debug }

// ToolExecutionResult captures the execution lifecycle outcome for one ToolCall.
type ToolExecutionResult struct {
	CallID      string                 `json:"call_id"`
	ToolName    string                 `json:"tool_name"`
	Success     bool                   `json:"success"`
	Status      tools.ToolStatus       `json:"status"`
	Outcome     tools.ToolOutcomeClass `json:"outcome"`
	Output      string                 `json:"output"`
	ArtifactID  string                 `json:"artifact_id,omitempty"`
	Reused      bool                   `json:"reused,omitempty"`
	Observation Observation            `json:"-"`
	Result      *tools.ToolResult      `json:"-"`
	Err         error                  `json:"-"`
}

// ToolExecutionSummary aggregates execution results across all dispatched calls.
type ToolExecutionSummary struct {
	Results      []ToolExecutionResult
	Outcome      tools.ToolOutcomeClass
	Observations []Observation
	ReusedCount  int
	Err          error
}

// ToolExecutionOptions parameterizes one batch tool execution pass.
type ToolExecutionOptions struct {
	TaskID    string
	AfterTool func(models.ToolCall, Observation) error
}

type preparedToolCall struct {
	call       models.ToolCall
	descriptor tools.ToolDescriptor
	result     *ToolExecutionResult
	ctx        context.Context
}

type settledToolCall struct {
	index  int
	result ToolExecutionResult
}

// executeAll schedules consecutive parallel-safe calls together. Exclusive
// calls remain ordering barriers and every durable result commits in model order.
func (a *Agent) executeAll(ctx context.Context, session *Session, calls []models.ToolCall, opts ToolExecutionOptions) ToolExecutionSummary {
	summary := ToolExecutionSummary{Outcome: tools.ToolOutcomeSuccess}
	if len(calls) == 0 {
		return summary
	}
	taskID := opts.TaskID
	if taskID == "" && session != nil {
		taskID = session.ActiveTaskID
	}
	for next := 0; next < len(calls); {
		if ctx.Err() != nil {
			summary.Outcome, summary.Err = tools.ToolOutcomeCancelled, ctx.Err()
			a.synthesizeRemaining(ctx, session, taskID, calls[next:], "Error: tool call aborted before dispatch", tools.ToolOutcomeCancelled, &summary)
			break
		}
		end := next + 1
		if a.parallelSafe(calls[next]) {
			for end < len(calls) && a.parallelSafe(calls[end]) {
				end++
			}
		}
		consumed, stop := a.executeGroup(ctx, session, calls[next:end], opts, &summary)
		next += consumed
		if stop {
			if next < len(calls) {
				a.synthesizeRemaining(ctx, session, taskID, calls[next:], "Error: tool call aborted before dispatch", tools.ToolOutcomeCancelled, &summary)
			}
			break
		}
	}
	return summary
}

func (a *Agent) parallelSafe(call models.ToolCall) bool {
	if a == nil || a.toolManager == nil {
		return false
	}
	desc, err := a.toolManager.Descriptor(call.Name)
	return err == nil && desc.ParallelSafe
}

func (a *Agent) parallelLimit() int {
	if a != nil && a.maxParallelTools > 0 {
		return a.maxParallelTools
	}
	return 4
}

func (a *Agent) executeGroup(ctx context.Context, session *Session, calls []models.ToolCall, opts ToolExecutionOptions, summary *ToolExecutionSummary) (int, bool) {
	parallel := len(calls) > 1 && a.parallelSafe(calls[0])
	limit := 1
	if parallel {
		limit = a.parallelLimit()
	}
	sessionID, taskID := "", opts.TaskID
	if session != nil {
		sessionID = session.ID
		if taskID == "" {
			taskID = session.ActiveTaskID
		}
	}
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	settled := make([]*ToolExecutionResult, len(calls))
	done := make(chan settledToolCall, limit)
	next, active, committed := 0, 0, 0
	stopStarting, stopBatch := false, false

	start := func(index int) error {
		call := calls[index]
		if err := a.appendHarness(groupCtx, session, EventToolCall, sessionlog.ToolCallPayload{
			Turn: a.phase.Turn, Step: a.phase.Step, CallID: call.ID, Tool: call.Name, Arguments: string(call.Arguments),
		}); err != nil {
			return fmt.Errorf("persist tool call %s: %w", call.ID, err)
		}
		prepared := a.prepareOne(groupCtx, sessionID, taskID, call)
		if prepared.result != nil {
			result := *prepared.result
			settled[index] = &result
			return nil
		}
		active++
		go func() {
			done <- settledToolCall{index: index, result: a.dispatchPrepared(prepared)}
		}()
		return nil
	}

	commitReady := func() {
		for committed < next && settled[committed] != nil {
			result := *settled[committed]
			if a.commitOne(groupCtx, session, taskID, calls[committed], result, opts, summary) {
				stopStarting, stopBatch = true, true
			}
			committed++
		}
	}

	for committed < len(calls) {
		for !stopStarting && next < len(calls) && active < limit {
			if groupCtx.Err() != nil {
				stopStarting, stopBatch = true, true
				break
			}
			if err := start(next); err != nil {
				summary.Outcome = tools.ToolOutcomeFatal
				summary.Err = errors.Join(summary.Err, err)
				stopStarting, stopBatch = true, true
				cancel()
				break
			}
			next++
			commitReady()
		}
		if active == 0 {
			commitReady()
			break
		}
		completed := <-done
		active--
		result := completed.result
		settled[completed.index] = &result
		commitReady()
	}
	for active > 0 {
		completed := <-done
		active--
		result := completed.result
		settled[completed.index] = &result
	}
	commitReady()
	if ctx.Err() != nil {
		summary.Outcome = tools.ToolOutcomeCancelled
		summary.Err = errors.Join(summary.Err, ctx.Err())
		stopBatch = true
	}
	return next, stopBatch
}

func (a *Agent) prepareOne(ctx context.Context, sessionID, taskID string, call models.ToolCall) preparedToolCall {
	if tools.Workspace(ctx) == "" && a.workspace != "" {
		ctx = tools.WithWorkspace(ctx, a.workspace)
	}
	if sessionID != "" || taskID != "" {
		meta := sessionlog.EventMetaFromContext(ctx)
		ctx = tools.WithProgressScope(ctx, tools.ProgressScope{SessionID: sessionID, TaskID: taskID, RunID: meta.CorrelationID, MessageID: meta.CausationID, CallID: call.ID})
	}
	prepared := preparedToolCall{call: call, ctx: ctx}
	res := ToolExecutionResult{CallID: call.ID, ToolName: call.Name}

	if a.toolManager == nil {
		err := fmt.Errorf("tool manager is required to execute %s", call.Name)
		res.Success = false
		res.Status = tools.ToolStatusFailed
		res.Outcome = tools.ToolOutcomeFatal
		res.Err = err
		res.Observation = NewObservation(call.Name, nil, err)
		res.Output = res.Observation.Output
		prepared.result = &res
		return prepared
	}

	desc, _ := a.toolManager.Descriptor(call.Name)
	prepared.descriptor = desc
	before, err := a.hooks.RunBeforeTool(runtime.BeforeToolEvent{
		Context: ctx, SessionID: sessionID, TaskID: taskID, Call: call, Descriptor: desc,
	})
	if err != nil {
		res.Success = false
		res.Status = tools.ToolStatusFailed
		res.Outcome = tools.ToolOutcomeFatal
		res.Err = err
		res.Observation = NewObservation(call.Name, nil, err)
		res.Output = res.Observation.Output
		prepared.result = &res
		return prepared
	}

	if before.Result != nil {
		toolRes := before.Result
		res.Reused = before.Reused
		obs := NewObservation(call.Name, toolRes, nil)
		_, cArgs, _, _ := state.ComputeCallFingerprint(call.Name, call.Arguments, a.workspace)
		logResolvedToolExecution(call, string(cArgs), "cached", obs, "", "", nil, 0, nil)
		res.Success = true
		res.Status = tools.ToolStatusCompleted
		res.Outcome = tools.ToolOutcomeSuccess
		res.ArtifactID = toolRes.ArtifactID
		res.Observation = obs
		res.Result = toolRes
		res.Output = obs.Output
		prepared.result = &res
	}
	return prepared
}

func (a *Agent) dispatchPrepared(prepared preparedToolCall) ToolExecutionResult {
	call, ctx, desc := prepared.call, prepared.ctx, prepared.descriptor
	start := time.Now()
	res := ToolExecutionResult{CallID: call.ID, ToolName: call.Name}
	_, cArgs, _, scope := state.ComputeCallFingerprint(call.Name, call.Arguments, a.workspace)

	logging.Info("Tool execution starting (physical): %s (id=%s) args=%s", call.Name, call.ID, string(call.Arguments))
	toolRes, err := a.toolManager.Execute(ctx, call.Name, call.Arguments)
	obs := NewObservation(call.Name, toolRes, err)
	outcome := tools.ClassifyToolOutcome(toolRes, err)
	invalidations := []string(nil)
	if !desc.Inspection {
		invalidations = mutatingInvalidation(scope, obs)
	}
	logResolvedToolExecution(call, string(cArgs), "physical", obs, "", "", err, time.Since(start), invalidations)

	res.Success = obs.Success && (outcome == tools.ToolOutcomeSuccess)
	res.Status = obs.Status
	res.Outcome = outcome
	res.ArtifactID = obs.ArtifactID
	res.Observation = obs
	res.Result = toolRes
	res.Output = obs.Output
	res.Err = err
	return res
}

func (a *Agent) commitOne(ctx context.Context, session *Session, taskID string, call models.ToolCall, execRes ToolExecutionResult, opts ToolExecutionOptions, summary *ToolExecutionSummary) bool {
	sessionID := ""
	if session != nil {
		sessionID = session.ID
	}
	controlErr := a.applyToolControl(ctx, session, call, &execRes)
	summary.Results = append(summary.Results, execRes)
	summary.Observations = append(summary.Observations, execRes.Observation)
	if execRes.Reused {
		summary.ReusedCount++
	}
	if session != nil {
		msg := models.Message{Role: models.RoleTool, Content: execRes.Output, ToolCallID: call.ID, ToolName: call.Name}
		if err := a.appendTerminalConfigured(ctx, session, turnConfig{taskID: taskID}, msg); err != nil {
			summary.Outcome = tools.ToolOutcomeFatal
			summary.Err = errors.Join(summary.Err, fmt.Errorf("persist tool result %s: %w", call.ID, err))
			return true
		}
	}
	desc := tools.ToolDescriptor{}
	if a.toolManager != nil {
		desc, _ = a.toolManager.Descriptor(call.Name)
	}
	decision, err := a.hooks.RunAfterTool(runtime.AfterToolEvent{Context: ctx, SessionID: sessionID, TaskID: taskID, Call: call, Result: execRes.Result, Outcome: execRes.Outcome, Reused: execRes.Reused, Descriptor: desc})
	if err != nil {
		summary.Outcome, summary.Err = tools.ToolOutcomeFatal, errors.Join(summary.Err, err)
		return true
	}
	for _, message := range decision.NextStep {
		a.inbox.StageNextStep(message)
	}
	if err := a.observeContextTool(ctx, session, ToolObservation{Name: execRes.Observation.ToolName, Status: execRes.Observation.Status, Success: execRes.Observation.Success, ArtifactID: execRes.Observation.ArtifactID}); err != nil {
		summary.Outcome = tools.ToolOutcomeFatal
		summary.Err = errors.Join(summary.Err, fmt.Errorf("persist tool observation: %w", err))
		return true
	}
	if opts.AfterTool != nil {
		if err := opts.AfterTool(call, execRes.Observation); err != nil {
			summary.Outcome, summary.Err = tools.ToolOutcomeFatal, errors.Join(summary.Err, err)
			return true
		}
	}
	if controlErr != nil {
		summary.Outcome = tools.ToolOutcomeRecoverable
		summary.Err = errors.Join(summary.Err, controlErr)
		return true
	}
	switch execRes.Outcome {
	case tools.ToolOutcomeFatal, tools.ToolOutcomeCancelled, tools.ToolOutcomePermissionBlocked, tools.ToolOutcomeApprovalRequired:
		summary.Outcome = execRes.Outcome
		summary.Err = errors.Join(summary.Err, execRes.Err)
		return true
	case tools.ToolOutcomeRecoverable:
		summary.Outcome = tools.ToolOutcomeRecoverable
		summary.Err = errors.Join(summary.Err, execRes.Err)
		return true
	}
	if !execRes.Success {
		summary.Outcome = tools.ToolOutcomeRecoverable
		return true
	}
	return false
}

func (a *Agent) applyToolControl(ctx context.Context, session *Session, call models.ToolCall, execution *ToolExecutionResult) error {
	if execution == nil || execution.Result == nil || !execution.Result.RequestPlanModeExit {
		return nil
	}
	if call.Name != "exit_plan_mode" {
		return a.replaceToolControlWithFailure(execution, call.Name, fmt.Errorf("tool %q cannot request a Plan Mode transition", call.Name))
	}
	if session == nil || !session.PlanModeActive() {
		return a.replaceToolControlWithFailure(execution, call.Name, fmt.Errorf("Plan Mode is not active"))
	}
	if err := a.SetPlanMode(ctx, session, false); err != nil {
		return a.replaceToolControlWithFailure(execution, call.Name, fmt.Errorf("exit Plan Mode: %w", err))
	}
	return nil
}

func (a *Agent) replaceToolControlWithFailure(execution *ToolExecutionResult, toolName string, err error) error {
	result := &tools.ToolResult{
		Status:    tools.ToolStatusFailed,
		Success:   false,
		Retryable: true,
		Message:   err.Error(),
	}
	observation := NewObservation(toolName, result, nil)
	execution.Success = false
	execution.Status = observation.Status
	execution.Outcome = tools.ToolOutcomeRecoverable
	execution.Output = observation.Output
	execution.Observation = observation
	execution.Result = result
	execution.Err = err
	return err
}

func (a *Agent) synthesizeRemaining(ctx context.Context, session *Session, taskID string, calls []models.ToolCall, message string, outcome tools.ToolOutcomeClass, summary *ToolExecutionSummary) {
	if err := a.synthesizeSkippedCalls(ctx, session, taskID, calls, message, outcome, summary); err != nil {
		summary.Outcome = tools.ToolOutcomeFatal
		summary.Err = errors.Join(summary.Err, fmt.Errorf("persist skipped tool calls: %w", err))
	}
}

func (a *Agent) synthesizeSkippedCalls(ctx context.Context, session *Session, taskID string, calls []models.ToolCall, message string, outcome tools.ToolOutcomeClass, summary *ToolExecutionSummary) error {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	for _, call := range calls {
		if err := a.appendHarness(terminalCtx, session, EventToolCall, sessionlog.ToolCallPayload{
			Turn: a.phase.Turn, Step: a.phase.Step, CallID: call.ID, Tool: call.Name, Arguments: string(call.Arguments), Skipped: true, Reason: message,
		}); err != nil {
			return err
		}
		skippedRes := ToolExecutionResult{
			CallID:   call.ID,
			ToolName: call.Name,
			Success:  outcome == tools.ToolOutcomeSuccess,
			Status:   tools.ToolStatusFailed,
			Outcome:  outcome,
			Output:   message,
		}
		if outcome == tools.ToolOutcomeSuccess {
			skippedRes.Status = tools.ToolStatusCompleted
		}
		skippedRes.Observation = Observation{
			ToolName: call.Name,
			Success:  skippedRes.Success,
			Status:   skippedRes.Status,
			Output:   message,
		}
		summary.Results = append(summary.Results, skippedRes)
		summary.Observations = append(summary.Observations, skippedRes.Observation)
		if session != nil {
			if err := a.appendConfigured(terminalCtx, session, turnConfig{taskID: taskID}, models.Message{
				Role:       models.RoleTool,
				Content:    message,
				ToolCallID: call.ID,
				ToolName:   call.Name,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
