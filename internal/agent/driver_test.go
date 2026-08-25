package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type driverLifecycle struct {
	histories   [][]models.Message
	activeTools []string
}

func (l *driverLifecycle) Compile(_ context.Context, request ContextRequest) (*models.Prompt, error) {
	history := append([]models.Message(nil), request.Session.DeriveMessages()...)
	l.histories = append(l.histories, history)
	activeTools := l.activeTools
	if len(activeTools) == 0 {
		activeTools = []string{"probe"}
	}
	return &models.Prompt{
		Messages:    history,
		ActiveTools: activeTools,
		Metadata:    models.PromptMetadata{Profile: string(protocol.Conversational)},
	}, nil
}
func (*driverLifecycle) RecordObjective(context.Context, string, string, string) error { return nil }
func (*driverLifecycle) RecordUsage(context.Context, *models.Prompt, providers.Usage) error {
	return nil
}
func (*driverLifecycle) ObserveTool(context.Context, string, string, ToolObservation) error {
	return nil
}

type scriptedProvider struct {
	mu    sync.Mutex
	calls int
	chat  func(ctx context.Context, n int, prompt *models.Prompt) (*providers.Completion, error)
}

type streamedProvider struct {
	mu     sync.Mutex
	calls  int
	events [][]providers.StreamEvent
	errs   []error
}

type faultTranscript struct {
	TranscriptStore
	failKind      EventType
	failErr       error
	failSyncAfter EventType
	lastKind      EventType
}

func (t *faultTranscript) AppendHarnessEvent(ctx context.Context, sessionID string, kind EventType, data any) error {
	t.lastKind = kind
	if kind == t.failKind {
		return t.failErr
	}
	return t.TranscriptStore.AppendHarnessEvent(ctx, sessionID, kind, data)
}

func (t *faultTranscript) SyncSurface(ctx context.Context, session *Session) error {
	if t.failSyncAfter != "" && t.lastKind == t.failSyncAfter {
		return t.failErr
	}
	return t.TranscriptStore.SyncSurface(ctx, session)
}

func (p *streamedProvider) Chat(context.Context, *models.Prompt) (*providers.Completion, error) {
	return nil, errors.New("unexpected non-streaming provider call")
}

func (p *streamedProvider) Stream(_ context.Context, _ *models.Prompt, receive func(providers.StreamEvent) error) error {
	p.mu.Lock()
	n := p.calls
	p.calls++
	events := append([]providers.StreamEvent(nil), p.events[n]...)
	var streamErr error
	if n < len(p.errs) {
		streamErr = p.errs[n]
	}
	p.mu.Unlock()
	for _, event := range events {
		if err := receive(event); err != nil {
			return err
		}
	}
	return streamErr
}

func (p *scriptedProvider) Chat(ctx context.Context, prompt *models.Prompt) (*providers.Completion, error) {
	p.mu.Lock()
	n := p.calls
	p.calls++
	fn := p.chat
	p.mu.Unlock()
	return fn(ctx, n, prompt)
}

type probeTool struct {
	name string
	err  error
	fn   func(context.Context) (*tools.ToolResult, error)
}

func (p *probeTool) Name() string {
	if p.name != "" {
		return p.name
	}
	return "probe"
}
func (p *probeTool) Description() string { return "probe" }
func (p *probeTool) Schema() any         { return map[string]any{"type": "object"} }
func (p *probeTool) Capabilities() tools.CapabilitySet {
	return tools.CapabilityReadWorkspace
}
func (p *probeTool) Execute(ctx context.Context, _ any) (*tools.ToolResult, error) {
	if p.fn != nil {
		return p.fn(ctx)
	}
	if p.err != nil {
		return nil, p.err
	}
	return &tools.ToolResult{Success: true, Status: tools.ToolStatusCompleted, Message: "ok"}, nil
}

func driverAgent(t *testing.T, provider providers.Provider, tool tools.Tool, life *driverLifecycle) (*Agent, *Session) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	session := &Session{ID: "drv", Name: "Driver"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	if tool != nil {
		if err := reg.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	worker := newTestAgent(provider, tools.NewManager(reg), life, newDurableMemory(store), nil)
	worker.workspace = root
	worker.retryWait = func(context.Context, time.Duration) error { return nil }
	return worker, session
}

func countEvents(session *Session, kind EventType) int {
	n := 0
	for _, event := range session.events {
		if event.Type == kind {
			n++
		}
	}
	return n
}

func TestDriverNaturalCompletion(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(context.Context, int, *models.Prompt) (*providers.Completion, error) {
		return &providers.Completion{Text: "done"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	text, err := worker.Run(context.Background(), session, "hello")
	if err != nil || text != "done" || provider.calls != 1 {
		t.Fatalf("text=%q calls=%d err=%v", text, provider.calls, err)
	}
	if countEvents(session, EventTurnStart) != 1 || countEvents(session, EventTurnEnd) != 1 || countEvents(session, EventStepStart) != 1 {
		t.Fatalf("events turn/step mismatch: starts=%d ends=%d steps=%d", countEvents(session, EventTurnStart), countEvents(session, EventTurnEnd), countEvents(session, EventStepStart))
	}
}

func TestDriverDoesNotCallProviderWhenTurnStartSurfaceSyncFails(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(context.Context, int, *models.Prompt) (*providers.Completion, error) {
		return &providers.Completion{Text: "must not run"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	worker.transcript = &faultTranscript{TranscriptStore: worker.transcript, failSyncAfter: EventTurnStart, failErr: errors.New("surface unavailable")}

	if _, err := worker.Run(context.Background(), session, "hello"); err == nil || provider.calls != 0 {
		t.Fatalf("provider calls=%d err=%v", provider.calls, err)
	}
}

func TestDriverDoesNotExecuteToolWhenCallPersistenceFails(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(context.Context, int, *models.Prompt) (*providers.Completion, error) {
		return &providers.Completion{ToolCalls: []models.ToolCall{{ID: "blocked", Name: "probe", Arguments: json.RawMessage(`{}`)}}}, nil
	}}
	var executions atomic.Int32
	worker, session := driverAgent(t, provider, &probeTool{fn: func(context.Context) (*tools.ToolResult, error) {
		executions.Add(1)
		return &tools.ToolResult{Success: true, Status: tools.ToolStatusCompleted}, nil
	}}, life)
	persistErr := errors.New("tool call persistence failed")
	worker.transcript = &faultTranscript{TranscriptStore: worker.transcript, failKind: EventToolCall, failErr: persistErr}

	_, err := worker.Run(context.Background(), session, "run it")
	if !errors.Is(err, persistErr) || executions.Load() != 0 {
		t.Fatalf("executions=%d err=%v", executions.Load(), err)
	}
}

func TestDriverJoinsOperationAndTerminalPersistenceErrors(t *testing.T) {
	life := &driverLifecycle{}
	operationErr := errors.New("provider failed")
	terminalErr := errors.New("step end persistence failed")
	provider := &scriptedProvider{chat: func(context.Context, int, *models.Prompt) (*providers.Completion, error) {
		return nil, operationErr
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	worker.transcript = &faultTranscript{TranscriptStore: worker.transcript, failKind: EventStepEnd, failErr: terminalErr}

	_, err := worker.Run(context.Background(), session, "hello")
	if !errors.Is(err, operationErr) || !errors.Is(err, terminalErr) {
		t.Fatalf("joined error = %v", err)
	}
}

func TestDriverPlanExitAppliesToFollowingStep(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })

	session := &Session{ID: "plan-exit", Name: "Plan Exit"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}

	var writeSawResearchOnly bool
	registry := tools.NewRegistry()
	if err := registry.Register(&probeTool{name: "exit_plan_mode", fn: func(context.Context) (*tools.ToolResult, error) {
		return &tools.ToolResult{Success: true, Status: tools.ToolStatusCompleted, RequestPlanModeExit: true}, nil
	}}, tools.ToolMetadata{CanonicalName: "exit_plan_mode", Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&probeTool{name: "write_file", fn: func(ctx context.Context) (*tools.ToolResult, error) {
		writeSawResearchOnly = tools.IsResearchOnly(ctx)
		return &tools.ToolResult{Success: true, Status: tools.ToolStatusCompleted, Message: "write complete"}, nil
	}}, tools.ToolMetadata{CanonicalName: "write_file", Access: tools.ToolAccessWrite, SideEffect: tools.ToolSideEffectWorkspace}); err != nil {
		t.Fatal(err)
	}

	lifecycle := &driverLifecycle{activeTools: []string{"exit_plan_mode", "write_file"}}
	provider := &scriptedProvider{chat: func(_ context.Context, step int, _ *models.Prompt) (*providers.Completion, error) {
		switch step {
		case 0:
			return &providers.Completion{ToolCalls: []models.ToolCall{{ID: "exit", Name: "exit_plan_mode", Arguments: []byte(`{}`)}}}, nil
		case 1:
			if session.PlanModeActive() {
				t.Fatal("Plan Mode must be inactive before the next provider request")
			}
			return &providers.Completion{ToolCalls: []models.ToolCall{{ID: "write", Name: "write_file", Arguments: []byte(`{}`)}}}, nil
		default:
			return &providers.Completion{Text: "implementation complete"}, nil
		}
	}}
	worker := newTestAgent(provider, tools.NewManager(registry), lifecycle, newDurableMemory(store), nil)
	worker.workspace = root
	worker.retryWait = func(context.Context, time.Duration) error { return nil }
	if err := worker.SetPlanMode(context.Background(), session, true); err != nil {
		t.Fatal(err)
	}

	text, err := worker.Run(context.Background(), session, "make a plan and implement it")
	if err != nil {
		t.Fatal(err)
	}
	if text != "implementation complete" || provider.calls != 3 {
		t.Fatalf("text=%q provider calls=%d", text, provider.calls)
	}
	if writeSawResearchOnly {
		t.Fatal("the Step after exit_plan_mode remained research-only")
	}
}

func TestDriverPersistsCanonicalStreamDiagnosticsAndProvenance(t *testing.T) {
	life := &driverLifecycle{}
	provider := &streamedProvider{events: [][]providers.StreamEvent{{
		{Type: providers.StreamEventReasoningDelta, ReasoningDelta: "private reasoning"},
		{Type: providers.StreamEventTextDelta, TextDelta: "Hello"},
		{Type: providers.StreamEventTextDelta, TextDelta: " there"},
		{Type: providers.StreamEventUsage, Usage: &providers.Usage{InputTokens: 3, OutputTokens: 2}},
		{Type: providers.StreamEventFinish, FinishReason: providers.FinishStop},
	}}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	text, err := worker.Run(context.Background(), session, "hi")
	if err != nil || text != "Hello there" || provider.calls != 1 {
		t.Fatalf("text=%q calls=%d err=%v", text, provider.calls, err)
	}

	events := session.Events()
	var chunks []SessionEvent
	var assistant, header, request SessionEvent
	headerAt, requestAt, firstChunkAt, usageAt, finishAt, assistantAt := -1, -1, -1, -1, -1, -1
	for index, event := range events {
		switch event.Type {
		case EventRequestHeader:
			header, headerAt = event, index
		case EventRequestContext:
			request, requestAt = event, index
		case EventAssistantChunk:
			chunks = append(chunks, event)
			if firstChunkAt < 0 {
				firstChunkAt = index
			}
		case EventUsage:
			usageAt = index
		case EventFinish:
			finishAt = index
		case EventAssistantMessage:
			assistant, assistantAt = event, index
		}
	}
	if len(chunks) != 2 || headerAt < 0 || requestAt < 0 || usageAt < 0 || finishAt < 0 || assistantAt < 0 || !(headerAt < requestAt && requestAt < firstChunkAt && firstChunkAt < usageAt && usageAt < finishAt && finishAt < assistantAt) {
		t.Fatalf("diagnostic event ordering = %#v", events)
	}
	if payload, ok := header.Data.(sessionlog.RequestHeaderPayload); !ok || payload.Reason != "initial" || payload.HeaderDigest == "" || payload.SystemDigest == "" || payload.ToolSchemaDigest == "" {
		t.Fatalf("request header payload = %#v", header.Data)
	}
	if payload, ok := chunks[0].Data.(sessionlog.AssistantChunkPayload); !ok || payload.Event.Type != providers.StreamEventTextDelta {
		t.Fatalf("chunk payload = %#v", chunks[0].Data)
	}
	if payload, ok := request.Data.(sessionlog.RequestContextPayload); !ok || payload.PromptArtifactID == "" || payload.RequestDigest == "" || payload.HeaderDigest == "" {
		t.Fatalf("request context payload = %#v", request.Data)
	} else if _, err := worker.transcript.(*DurableMemory).store.ReadArtifact(context.Background(), payload.PromptArtifactID); err != nil {
		t.Fatalf("compiled prompt artifact unreadable: %v", err)
	}
	if len(assistant.SourceEventSeqs) != 2 || assistant.SourceEventSeqs[0] != chunks[0].Seq || assistant.SourceEventSeqs[1] != chunks[1].Seq {
		t.Fatalf("assistant provenance = %#v, chunks=%#v", assistant.SourceEventSeqs, chunks)
	}
	if messages := session.DeriveMessages(); len(messages) != 2 || messages[1].Content != "Hello there" {
		t.Fatalf("stream diagnostics leaked into context: %#v", messages)
	}
}

func TestDriverWritesRequestHeaderOnlyWhenHeaderChanges(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(context.Context, int, *models.Prompt) (*providers.Completion, error) {
		return &providers.Completion{Text: "done"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	if _, err := worker.Run(context.Background(), session, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Run(context.Background(), session, "second"); err != nil {
		t.Fatal(err)
	}
	if countEvents(session, EventRequestHeader) != 1 {
		t.Fatalf("unchanged header events = %d", countEvents(session, EventRequestHeader))
	}
}

func TestDriverMarksFirstHeaderAfterRestartAsResume(t *testing.T) {
	provider := &scriptedProvider{chat: func(context.Context, int, *models.Prompt) (*providers.Completion, error) {
		return &providers.Completion{Text: "done"}, nil
	}}
	worker, session := driverAgent(t, provider, nil, &driverLifecycle{})
	if _, err := worker.Run(context.Background(), session, "before restart"); err != nil {
		t.Fatal(err)
	}

	restartedProvider := &scriptedProvider{chat: provider.chat}
	restarted := newTestAgent(restartedProvider, tools.NewManager(tools.NewRegistry()), &driverLifecycle{}, worker.transcript, nil)
	restarted.workspace = worker.workspace
	reloaded := &Session{ID: session.ID, Name: session.Name}
	if _, err := restarted.Run(context.Background(), reloaded, "after restart"); err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, event := range reloaded.events {
		if event.Type == EventRequestHeader {
			if payload, ok := event.Data.(sessionlog.RequestHeaderPayload); ok {
				reasons = append(reasons, payload.Reason)
			}
		}
	}
	if len(reasons) != 2 || reasons[0] != "initial" || reasons[1] != "resume" {
		t.Fatalf("request header reasons after restart = %v", reasons)
	}
}

func TestDriverStreamedToolOnlyResponseContinues(t *testing.T) {
	life := &driverLifecycle{}
	provider := &streamedProvider{events: [][]providers.StreamEvent{
		{
			{Type: providers.StreamEventToolCallDelta, ToolCall: &providers.ToolCallDelta{Index: 0, ID: "call-1", Name: "probe", ArgumentsDelta: `{"value":`}},
			{Type: providers.StreamEventToolCallDelta, ToolCall: &providers.ToolCallDelta{Index: 0, ArgumentsDelta: `"ok"}`}},
			{Type: providers.StreamEventFinish, FinishReason: providers.FinishToolCalls},
		},
		{{Type: providers.StreamEventTextDelta, TextDelta: "done"}, {Type: providers.StreamEventFinish, FinishReason: providers.FinishStop}},
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	text, err := worker.Run(context.Background(), session, "run probe")
	if err != nil || text != "done" || provider.calls != 2 {
		t.Fatalf("text=%q calls=%d err=%v", text, provider.calls, err)
	}
	if countEvents(session, EventToolCall) != 1 || countEvents(session, EventToolResult) != 1 {
		t.Fatalf("tool-only stream was not executed causally: %#v", session.Events())
	}
}

func TestDriverToolContinuationAndCausalPair(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(_ context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		if n == 0 {
			return &providers.Completion{ToolCalls: []models.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		return &providers.Completion{Text: "ok"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	text, err := worker.Run(context.Background(), session, "go")
	if err != nil || text != "ok" || provider.calls != 2 {
		t.Fatalf("text=%q calls=%d err=%v", text, provider.calls, err)
	}
	if countEvents(session, EventStepStart) != 2 {
		t.Fatalf("steps=%d", countEvents(session, EventStepStart))
	}
	if len(life.histories) < 2 {
		t.Fatalf("histories=%d", len(life.histories))
	}
	second := life.histories[1]
	if len(second) < 3 || second[1].Role != models.RoleAssistant || len(second[1].ToolCalls) != 1 || second[2].Role != models.RoleTool || second[2].ToolCallID != "c1" {
		t.Fatalf("causal pair missing: %#v", second)
	}
}

func TestDriverToolOnlyAssistant(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(_ context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		if n == 0 {
			return &providers.Completion{Text: "", ToolCalls: []models.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		return &providers.Completion{Text: "after"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	text, err := worker.Run(context.Background(), session, "x")
	if err != nil || text != "after" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestDriverRecoverableToolFailureContinues(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(_ context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		if n == 0 {
			return &providers.Completion{ToolCalls: []models.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}}}, nil
		}
		return &providers.Completion{Text: "repaired"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{err: tools.ErrInvalidInput}, life)
	text, err := worker.Run(context.Background(), session, "x")
	if err != nil || text != "repaired" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestDriverFatalToolFailureEndsTurn(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(context.Context, int, *models.Prompt) (*providers.Completion, error) {
		return &providers.Completion{ToolCalls: []models.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}}}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{err: errors.New("disk exploded")}, life)
	_, err := worker.Run(context.Background(), session, "x")
	if err == nil {
		t.Fatal("expected fatal tool error")
	}
}

func TestDriverRetryStaysInSameStep(t *testing.T) {
	life := &driverLifecycle{}
	provider := &scriptedProvider{chat: func(_ context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		if n < 2 {
			return nil, &net.DNSError{IsTimeout: true}
		}
		return &providers.Completion{Text: "ok"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	text, err := worker.Run(context.Background(), session, "retry")
	if err != nil || text != "ok" || provider.calls != 3 {
		t.Fatalf("text=%q calls=%d err=%v", text, provider.calls, err)
	}
	if countEvents(session, EventStepStart) != 1 || countEvents(session, EventUserMessage) != 1 {
		t.Fatalf("step=%d users=%d", countEvents(session, EventStepStart), countEvents(session, EventUserMessage))
	}
	if countEvents(session, EventRequestContext) != 3 || countEvents(session, EventError) != 2 || countEvents(session, EventRetry) != 2 {
		t.Fatalf("retry diagnostics requests=%d errors=%d retries=%d", countEvents(session, EventRequestContext), countEvents(session, EventError), countEvents(session, EventRetry))
	}
}

func TestDriverSteerAtCompletionStartsNextStep(t *testing.T) {
	life := &driverLifecycle{}
	started := make(chan struct{})
	release := make(chan struct{})
	provider := &scriptedProvider{chat: func(ctx context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		if n == 0 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &providers.Completion{Text: "first"}, nil
		}
		return &providers.Completion{Text: "steered"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	done := make(chan TurnResult, 1)
	go func() {
		text, err := worker.Run(context.Background(), session, "start")
		done <- TurnResult{Text: text, Err: err}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}
	worker.Steer(models.Message{Role: models.RoleUser, Content: "steer"})
	close(release)
	result := <-done
	if result.Err != nil || result.Text != "steered" {
		t.Fatalf("result=%#v", result)
	}
	if provider.calls != 2 || countEvents(session, EventStepStart) != 2 {
		t.Fatalf("calls=%d steps=%d", provider.calls, countEvents(session, EventStepStart))
	}
}

func TestDriverFollowupIsOwnTurn(t *testing.T) {
	life := &driverLifecycle{}
	started := make(chan struct{})
	release := make(chan struct{})
	provider := &scriptedProvider{chat: func(ctx context.Context, n int, prompt *models.Prompt) (*providers.Completion, error) {
		if n == 0 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return &providers.Completion{Text: "one"}, nil
		}
		return &providers.Completion{Text: "two"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	first := make(chan string, 1)
	second := make(chan string, 1)
	go func() {
		text, err := worker.Run(context.Background(), session, "a")
		if err != nil {
			t.Errorf("first: %v", err)
		}
		first <- text
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn did not start")
	}
	go func() {
		text, err := worker.Run(context.Background(), session, "b")
		if err != nil {
			t.Errorf("second: %v", err)
		}
		second <- text
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if <-first != "one" || <-second != "two" {
		t.Fatal("followup did not receive its own result")
	}
	if countEvents(session, EventTurnStart) != 2 {
		t.Fatalf("turns=%d", countEvents(session, EventTurnStart))
	}
}

func TestDriverSideQuestionQueuesBehindActiveRun(t *testing.T) {
	life := &driverLifecycle{}
	started := make(chan struct{})
	release := make(chan struct{})
	var inflight, max atomic.Int32
	provider := &scriptedProvider{chat: func(ctx context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		cur := inflight.Add(1)
		for {
			old := max.Load()
			if cur <= old || max.CompareAndSwap(old, cur) {
				break
			}
		}
		if n == 0 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				inflight.Add(-1)
				return nil, ctx.Err()
			}
			inflight.Add(-1)
			return &providers.Completion{Text: "run-done"}, nil
		}
		inflight.Add(-1)
		return &providers.Completion{Text: "side-done"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	first := make(chan string, 1)
	second := make(chan string, 1)
	go func() {
		text, err := worker.Run(context.Background(), session, "run question")
		if err != nil {
			t.Errorf("run: %v", err)
		}
		first <- text
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not start")
	}
	go func() {
		text, err := worker.AnswerSideQuestion(context.Background(), session.ID, "side question")
		if err != nil {
			t.Errorf("side: %v", err)
		}
		second <- text
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	if got := <-first; got != "run-done" {
		t.Fatalf("run result=%q", got)
	}
	if got := <-second; got != "side-done" {
		t.Fatalf("side result=%q", got)
	}
	if max.Load() != 1 {
		t.Fatalf("overlapping provider calls=%d", max.Load())
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls=%d", provider.calls)
	}
	reloaded := &Session{ID: session.ID}
	if err := worker.syncSessionSurface(context.Background(), reloaded); err != nil {
		t.Fatal(err)
	}
	if countEvents(reloaded, EventTurnStart) != 2 {
		t.Fatalf("turns=%d", countEvents(reloaded, EventTurnStart))
	}
}

func TestDriverSingleDriverNoOverlappingChat(t *testing.T) {
	life := &driverLifecycle{}
	var inflight, max atomic.Int32
	gate := make(chan struct{})
	provider := &scriptedProvider{chat: func(ctx context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		cur := inflight.Add(1)
		for {
			old := max.Load()
			if cur <= old || max.CompareAndSwap(old, cur) {
				break
			}
		}
		if n == 0 {
			<-gate
		}
		inflight.Add(-1)
		return &providers.Completion{Text: "ok"}, nil
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = worker.Run(context.Background(), session, "a")
	}()
	time.Sleep(30 * time.Millisecond)
	go func() {
		defer wg.Done()
		_, _ = worker.Run(context.Background(), session, "b")
	}()
	time.Sleep(30 * time.Millisecond)
	close(gate)
	wg.Wait()
	if max.Load() != 1 {
		t.Fatalf("overlapping chats=%d", max.Load())
	}
}

func TestDriverCancelAbortsTurn(t *testing.T) {
	life := &driverLifecycle{}
	started := make(chan struct{})
	provider := &scriptedProvider{chat: func(ctx context.Context, _ int, _ *models.Prompt) (*providers.Completion, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	worker, session := driverAgent(t, provider, &probeTool{}, life)
	errCh := make(chan error, 1)
	go func() {
		_, err := worker.Run(context.Background(), session, "x")
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("chat did not start")
	}
	worker.Cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not finish")
	}
}

func TestRepairSessionTailClosesDanglingToolAndBoundaries(t *testing.T) {
	events := []SessionEvent{
		{Seq: 0, Type: EventTurnStart},
		{Seq: 1, Type: EventStepStart},
		{Seq: 2, Type: EventAssistantMessage, Message: models.Message{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{{ID: "c1", Name: "probe"}}}},
	}
	extra := repairSessionTail(events)
	if len(extra) != 3 || extra[0].Type != EventToolResult || extra[0].Message.ToolCallID != "c1" || extra[1].Type != EventStepEnd || extra[2].Type != EventTurnEnd {
		t.Fatalf("extra=%#v", extra)
	}
	session := &Session{ID: "repair"}
	applyAll(t, session, events)
	worker := &Agent{}
	if err := worker.repairAndFold(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	msgs := session.DeriveMessages()
	if len(msgs) != 2 || msgs[1].Role != models.RoleTool || msgs[1].ToolCallID != "c1" {
		t.Fatalf("derived=%#v", msgs)
	}
}
