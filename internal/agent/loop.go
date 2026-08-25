package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

var (
	ErrInvalidResponse     = errors.New("model returned an invalid response")
	ErrProviderUnavailable = errors.New("provider temporarily unavailable")
)

// Run submits one next-turn request and waits for that Turn to finish.
func (a *Agent) Run(
	ctx context.Context,
	session *Session,
	userInput string,
) (string, error) {
	return a.RunAccepted(ctx, session, RunRequest{Content: userInput})
}

type RunRequest struct {
	RunID     string
	MessageID string
	Content   string
}

// RunAccepted executes one already-admitted backend request. Run remains the
// synchronous compatibility entrypoint for CLI and TUI callers.
func (a *Agent) RunAccepted(ctx context.Context, session *Session, request RunRequest) (string, error) {
	userInput := request.Content
	if request.RunID != "" || request.MessageID != "" {
		ctx = sessionlog.WithEventMeta(ctx, sessionlog.EventMeta{CorrelationID: request.RunID, CausationID: request.MessageID})
	}
	ctx, cancel, err := a.taskContext(ctx, session)
	if err != nil {
		return "", err
	}
	defer cancel()
	cfg := turnConfig{
		stream: true,
		makeRequest: func() ContextRequest {
			return ContextRequest{Session: session, Objective: userInput, Profile: protocol.Execution}
		},
	}
	a.hooks.NotifyUserInput(runtime.UserInputRun)
	logging.Info("User prompt received (session=%s): %s", session.ID, userInput)
	req := newTurnRequest(session, models.Message{LocalID: request.MessageID, Role: models.RoleUser, Content: userInput}, cfg)
	result := a.enqueueAndDrive(ctx, req)
	if result.Err != nil {
		return "", result.Err
	}
	return result.Text, nil
}

// AnswerSideQuestion answers from existing session memory without tools or writes.
func (a *Agent) AnswerSideQuestion(ctx context.Context, sessionID, question string) (string, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return "", fmt.Errorf("side question cannot be empty")
	}
	a.hooks.NotifyUserInput(runtime.UserInputSideQuestion)
	session := &Session{ID: sessionID}
	if err := a.syncSessionSurface(ctx, session); err != nil {
		return "", err
	}
	if err := a.repairAndFold(ctx, session); err != nil {
		return "", err
	}
	out := a.submitTurn(ctx, newTurnRequest(session, models.Message{Role: models.RoleUser, Content: question}, turnConfig{
		stream: false, sideAnswer: true,
		makeRequest: func() ContextRequest {
			return ContextRequest{Session: session, Profile: protocol.SideAnswer, Mode: tools.ToolModeSide}
		},
	}))
	if out.Err != nil {
		return "", fmt.Errorf("side answer response: %w", out.Err)
	}
	return out.Text, nil
}

func (a *Agent) nameSession(session *Session, firstRequest string) error {
	words := strings.Fields(firstRequest)
	if len(words) > 6 {
		words = words[:6]
	}
	title := cleanSessionTitle(strings.Join(words, " "))
	if title == "" {
		title = session.Name
	}
	if err := session.Rename(a.workspace, title); err != nil {
		return err
	}
	a.emit(ProgressEvent{Kind: ProgressSessionName, SessionID: session.ID, Message: session.Name})
	return nil
}

func cleanSessionTitle(title string) string {
	title = strings.TrimSpace(title)
	if line, _, ok := strings.Cut(title, "\n"); ok {
		title = line
	}
	if strings.HasPrefix(strings.ToLower(title), "title:") {
		title = strings.TrimSpace(title[len("title:"):])
	}
	title = strings.Trim(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, title), " \"'`#*_.")
	runes := []rune(title)
	if len(runes) > 60 {
		title = strings.TrimSpace(string(runes[:60]))
	}
	if _, err := validatedSessionName(title); err != nil {
		return ""
	}
	return title
}

func (a *Agent) complete(ctx context.Context, prompt *models.Prompt, stream bool, receive func(providers.StreamEvent) error) error {
	if stream {
		if streamer, ok := a.provider.(providers.StreamProvider); ok {
			return streamer.Stream(ctx, prompt, receive)
		}
	}
	completion, err := a.provider.Chat(ctx, prompt)
	if err != nil {
		return err
	}
	return providers.EmitCompletion(completion, receive)
}

// ModelInfoProvider exposes active provider and model names without concrete type assertions.
type ModelInfoProvider interface {
	ModelInfo() (provider, model string)
}

func (a *Agent) resolvedProviderModel(session *Session) (string, string) {
	provider, model := "", ""
	if session != nil {
		provider, model = session.Provider, session.Model
	}
	if (provider == "" || model == "") && a != nil {
		if info, ok := a.provider.(ModelInfoProvider); ok && info != nil {
			pName, mName := info.ModelInfo()
			if provider == "" {
				provider = pName
			}
			if model == "" {
				model = mName
			}
		}
	}
	return provider, model
}

func (a *Agent) logExactModelRequest(session *Session, prompt *models.Prompt, stream bool) {
	provider, model := a.resolvedProviderModel(session)
	LogTurnRequest(TurnRequestLogParams{Session: session, Prompt: prompt, Provider: provider, Model: model, Stream: stream, Timestamp: time.Now().UTC()})
}

func logExactModelResponse(completion *providers.Completion, parsed *parser.Response) {
	LogTurnResponse(completion, parsed)
}

func (a *Agent) completeWithRetry(
	ctx context.Context,
	session *Session,
	prompt *models.Prompt,
	stream bool,
	validate func(*providers.Completion) error,
) (*providers.Completion, error) {
	if prompt == nil {
		return nil, errors.New("provider prompt is required")
	}
	if prompt.RequestDigest == "" {
		if err := freezeProviderRequest(prompt); err != nil {
			return nil, err
		}
	}
	retriesEnabled := session.ResponseRetryEnabled()
	policy := a.retryPolicy
	if policy == nil {
		policy = runtime.NewDefaultRetryPolicy()
	}
	providerAttempts := 0
	for {
		attempt := providerAttempts + 1
		recorder := newStreamDiagnosticRecorder(a, ctx, session, prompt, attempt)
		if err := recorder.Begin(); err != nil {
			return nil, fmt.Errorf("persist request diagnostics: %w", err)
		}
		a.logExactModelRequest(session, prompt, stream)
		logging.Info("Sending model request: provider=%s model=%s profile=%s stream=%v messages=%d tools=%d", session.Provider, session.Model, prompt.Metadata.Profile, stream, len(prompt.Messages), len(prompt.ToolDefinitions))
		err := a.complete(ctx, prompt, stream, recorder.Receive)
		if err != nil {
			_ = recorder.RecordError(err)
			logging.Error("Model request failed (provider=%s model=%s): %v", session.Provider, session.Model, err)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			decision := policy.Decide(runtime.RetryEvent{
				Attempt:        providerAttempts,
				Err:            err,
				RetriesEnabled: retriesEnabled,
			}, prompt)

			if !decision.ShouldRetry {
				if !providers.IsTransient(err) {
					return nil, err
				}
				return nil, fmt.Errorf("%w after %d attempts: %v", ErrProviderUnavailable, providerAttempts+1, err)
			}

			providerAttempts++
			if persistErr := recorder.RecordRetry(decision.Delay.Milliseconds(), err); persistErr != nil {
				return nil, fmt.Errorf("persist retry diagnostics: %w", persistErr)
			}
			logging.Warn("Provider network error (attempt %d): %v — retrying in %v", providerAttempts, err, decision.Delay)
			a.emit(ProgressEvent{
				Kind:    ProgressRetry,
				Message: decision.ProgressMessage,
			})
			wait := a.retryWait
			if wait == nil {
				wait = waitForProviderRetry
			}
			if err := wait(ctx, decision.Delay); err != nil {
				return nil, err
			}
			continue
		}
		completion, err := recorder.Assemble()
		if err != nil {
			_ = recorder.RecordError(err)
			return nil, err
		}

		if completion == nil {
			logging.Error("Model request returned nil completion")
			return nil, fmt.Errorf("%w: provider returned no completion", ErrInvalidResponse)
		}
		logging.Info("Model response received (finish_reason=%s input_tokens=%d output_tokens=%d): %s", completion.FinishReason, completion.Usage.InputTokens, completion.Usage.OutputTokens, completion.Text)
		for _, tc := range completion.ToolCalls {
			logging.Info("Model requested tool call: %s (id=%s) args=%s", tc.Name, tc.ID, string(tc.Arguments))
		}
		if validate == nil {
			logExactModelResponse(completion, nil)
			return completion, nil
		}
		if err := validate(completion); err != nil {
			logging.Warn("Model response rejected by step policy: %v", err)
			logExactModelResponse(completion, nil)
			return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
		}
		parsed := &parser.Response{ToolCalls: completion.ToolCalls, TurnProgress: parser.ExtractAssistantTurnProgress(completion.Text)}
		logExactModelResponse(completion, parsed)
		return completion, nil
	}
}

func (a *Agent) parseCompletion(prompt *models.Prompt, completion *providers.Completion) (*parser.Response, error) {
	if completion == nil {
		return nil, fmt.Errorf("provider returned no completion")
	}
	if len(completion.ToolCalls) == 0 && strings.TrimSpace(completion.Text) == "" {
		return nil, &providers.ProviderFailure{Code: providers.FailureEmptyResponse, Message: "provider returned no assistant text or tool calls"}
	}
	return &parser.Response{ToolCalls: append([]models.ToolCall(nil), completion.ToolCalls...), TurnProgress: parser.ExtractAssistantTurnProgress(completion.Text)}, nil
}

func waitForProviderRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
