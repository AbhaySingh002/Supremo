package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	contextcompiler "github.com/AbhaySingh002/supremo/internal/context"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type alwaysPressured struct{ calls int }

func (p *alwaysPressured) Measure(*Session, *models.Prompt, int) Measurement {
	return Measurement{TotalTokens: 100, ThresholdTokens: 80, ContextLimit: 100}
}

func (p *alwaysPressured) ResolvePressure(ctx context.Context, store state.EventStore, session *Session, _ providers.Provider, _ *models.Prompt, _ Measurement) (PressureResult, error) {
	p.calls++
	nodes := session.Nodes()
	if len(nodes) == 0 {
		return PressureResult{}, fmt.Errorf("test surface is empty")
	}
	before := surfaceGeneration(session)
	event, err := sessionlog.New(EventUserMessage, models.Message{Role: models.RoleUser, Content: fmt.Sprintf("replacement %d", p.calls)})
	if err != nil {
		return PressureResult{}, err
	}
	event.Message = models.Message{Role: models.RoleUser, Content: fmt.Sprintf("replacement %d", p.calls)}
	event.SurfaceOp = &SurfaceOp{Kind: surfaceOpReplace, StartSeq: nodes[0], EndSeq: nodes[0]}
	event.SourceEventSeqs = []int64{nodes[0]}
	if err := appendAndApplySessionEvent(ctx, store, session, event); err != nil {
		return PressureResult{}, err
	}
	after := surfaceGeneration(session)
	if after <= before {
		return PressureResult{}, fmt.Errorf("test surface generation did not advance")
	}
	return PressureResult{Changed: true, BeforeTokens: 100, AfterTokens: 99, SurfaceGeneration: after}, nil
}

func (p *alwaysPressured) RecoverOverflow(ctx context.Context, store state.EventStore, session *Session, provider providers.Provider, prompt *models.Prompt, _ int) (PressureResult, error) {
	return p.ResolvePressure(ctx, store, session, provider, prompt, Measurement{TotalTokens: 100})
}

func TestContextPressureManagerBelowThresholdNoOp(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "no-pressure-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	e0 := SessionEvent{Seq: 0, Type: EventUserMessage, Message: models.Message{Role: models.RoleUser, Content: "Hello world"}}
	_ = persistSessionEvent(context.Background(), store, session.ID, e0)
	_ = session.applyEvent(e0)

	mockProv := &mockSummarizerProvider{summaryText: "summary"}
	mgr := NewRealContextPressureManager(nil, nil, nil)

	_, err = mgr.BeforeStep(context.Background(), store, session, mockProv, nil, 100_000)
	if err != nil {
		t.Fatalf("BeforeStep failed: %v", err)
	}
	if mockProv.calls != 0 {
		t.Fatalf("expected 0 summarizer calls for low pressure request, got %d", mockProv.calls)
	}
}

func TestContextPressureManagerPruningSufficientNoCompaction(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "prune-sufficient-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	// User message
	e0 := SessionEvent{Seq: 0, Type: EventUserMessage, Message: models.Message{Role: models.RoleUser, Content: "Run tests"}}
	_ = persistSessionEvent(context.Background(), store, session.ID, e0)
	_ = session.applyEvent(e0)

	// Assistant call
	e1 := SessionEvent{Seq: 1, Type: EventAssistantMessage, Message: models.Message{
		Role:      models.RoleAssistant,
		ToolCalls: []models.ToolCall{{ID: "c1", Name: "execute_command", Arguments: []byte(`{"command":"go","args":["test","./..."]}`)}},
	}}
	_ = persistSessionEvent(context.Background(), store, session.ID, e1)
	_ = session.applyEvent(e1)

	// Huge tool result (~12,000 chars -> ~3000 tokens)
	e2 := SessionEvent{Seq: 2, Type: EventToolResult, Message: models.Message{
		Role:       models.RoleTool,
		ToolCallID: "c1",
		ToolName:   "execute_command",
		Content:    strings.Repeat("TEST LOG ENTRY LINE\n", 600),
	}}
	_ = persistSessionEvent(context.Background(), store, session.ID, e2)
	_ = session.applyEvent(e2)

	// Context Limit 3200 tokens (Threshold = 2560 tokens). Total before pruning = ~3050 tokens (pressured).
	// After pruning (~5120 chars -> ~1300 tokens), total = ~1350 tokens (well below 2560).
	mockProv := &mockSummarizerProvider{summaryText: "summary"}
	mgr := NewRealContextPressureManager(nil, nil, nil)

	result, err := mgr.BeforeStep(context.Background(), store, session, mockProv, nil, 3200)
	if err != nil {
		t.Fatalf("BeforeStep failed: %v", err)
	}

	if mockProv.calls != 0 {
		t.Fatalf("expected 0 summarizer calls because pruning was sufficient, got %d", mockProv.calls)
	}
	if !result.Changed || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("pressure result = %#v", result)
	}

	// Verify tool result was pruned on surface
	derived := session.DeriveMessages()
	if len(derived) != 3 || !strings.Contains(derived[2].Content, PruneMarker) {
		t.Fatalf("expected tool result to be pruned on surface")
	}
}

func TestContextPressureManagerCompactionWhenPruningInsufficient(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "compact-insufficient-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	// Many user messages that cannot be pruned (since only ToolResults > 8192 chars are pruned)
	for i := 0; i < 8; i++ {
		e := SessionEvent{
			Seq:  int64(i),
			Type: EventUserMessage,
			Message: models.Message{
				Role:    models.RoleUser,
				Content: fmt.Sprintf("Detailed instruction block %d: %s", i, strings.Repeat("text content ", 50)),
			},
		}
		_ = persistSessionEvent(context.Background(), store, session.ID, e)
		_ = session.applyEvent(e)
	}

	// Context Limit 1000 tokens (Threshold = 800 tokens). Messages = ~1400 tokens.
	mockProv := &mockSummarizerProvider{summaryText: "## Primary Request and Intent\n- All tasks consolidated."}
	mgr := NewRealContextPressureManager(nil, nil, nil)

	result, err := mgr.BeforeStep(context.Background(), store, session, mockProv, nil, 1000)
	if err != nil {
		t.Fatalf("BeforeStep failed: %v", err)
	}

	if mockProv.calls != 1 {
		t.Fatalf("expected exactly 1 summarizer call, got %d", mockProv.calls)
	}
	if !result.Changed || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("pressure result = %#v", result)
	}

	derived := session.DeriveMessages()
	if len(derived) == 0 || !strings.Contains(derived[0].Content, "<compacted-summary>") {
		t.Fatalf("expected compacted summary on surface")
	}
}

func TestContextPressureReplayAfterRestart(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "restart-replay-test"
	session := &Session{ID: sessionID, Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	for i := 0; i < 6; i++ {
		e := SessionEvent{
			Seq:  int64(i),
			Type: EventUserMessage,
			Message: models.Message{
				Role:    models.RoleUser,
				Content: fmt.Sprintf("Message %d: %s", i, strings.Repeat("history payload ", 40)),
			},
		}
		_ = persistSessionEvent(context.Background(), store, session.ID, e)
		_ = session.applyEvent(e)
	}

	mockProv := &mockSummarizerProvider{summaryText: "## Primary Request and Intent\n- Checkpoint established."}
	mgr := NewRealContextPressureManager(nil, nil, nil)
	_, _ = mgr.BeforeStep(context.Background(), store, session, mockProv, nil, 600)

	derivedBeforeClose := session.DeriveMessages()
	nodesBeforeClose := session.Nodes()

	// Close database
	_ = state.CloseWorkspace(root)

	// Reopen database and load session
	reopenedStore, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	reconstructedSession := &Session{ID: sessionID, Provider: "mock", Model: "m1"}
	if err := reconstructedSession.AttachSurface(context.Background(), reopenedStore); err != nil {
		t.Fatalf("AttachSurface after restart failed: %v", err)
	}

	nodesAfterReopen := reconstructedSession.Nodes()
	derivedAfterReopen := reconstructedSession.DeriveMessages()

	if len(nodesAfterReopen) != len(nodesBeforeClose) {
		t.Fatalf("nodes mismatch after reopen: %v vs %v", nodesBeforeClose, nodesAfterReopen)
	}
	for i := range nodesBeforeClose {
		if nodesBeforeClose[i] != nodesAfterReopen[i] {
			t.Fatalf("node at index %d mismatch: %d vs %d", i, nodesBeforeClose[i], nodesAfterReopen[i])
		}
	}

	if len(derivedAfterReopen) != len(derivedBeforeClose) {
		t.Fatalf("derived messages length mismatch: %d vs %d", len(derivedBeforeClose), len(derivedAfterReopen))
	}
	if derivedAfterReopen[0].Content != derivedBeforeClose[0].Content {
		t.Fatalf("compacted checkpoint content mismatch after reopen")
	}
}

func TestContextPressureManagerRecoverOverflow(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "overflow-recover-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	// User msg (seq 0)
	e0 := SessionEvent{Seq: 0, Type: EventUserMessage, Message: models.Message{Role: models.RoleUser, Content: "Inspect large files"}}
	_ = persistSessionEvent(context.Background(), store, session.ID, e0)
	_ = session.applyEvent(e0)

	// Assistant call (seq 1)
	e1 := SessionEvent{Seq: 1, Type: EventAssistantMessage, Message: models.Message{
		Role:      models.RoleAssistant,
		ToolCalls: []models.ToolCall{{ID: "c1", Name: "read_file", Arguments: []byte(`{"path":"a.txt"}`)}},
	}}
	_ = persistSessionEvent(context.Background(), store, session.ID, e1)
	_ = session.applyEvent(e1)

	// Oversized tool result (seq 2)
	e2 := SessionEvent{Seq: 2, Type: EventToolResult, Message: models.Message{
		Role:       models.RoleTool,
		ToolCallID: "c1",
		ToolName:   "read_file",
		Content:    strings.Repeat("SOURCE FILE CONTENT LINE\n", 500),
	}}
	_ = persistSessionEvent(context.Background(), store, session.ID, e2)
	_ = session.applyEvent(e2)

	mockProv := &mockSummarizerProvider{summaryText: "## Primary Request and Intent\n- Compacted."}
	mgr := NewRealContextPressureManager(nil, nil, nil)

	// Simulate overflow trigger
	recovered, err := mgr.RecoverOverflow(context.Background(), store, session, mockProv, nil, 3200)
	if err != nil || !recovered.Changed {
		t.Fatalf("expected RecoverOverflow to succeed: recovered=%#v, err=%v", recovered, err)
	}

	// Verify tool result was pruned
	derived := session.DeriveMessages()
	if len(derived) != 3 || !strings.Contains(derived[2].Content, PruneMarker) {
		t.Fatalf("expected tool result to be pruned during overflow recovery")
	}
}

func TestStepStopsAfterThreePressureRecoveryPassesWithoutCommittingRequests(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()
	session := &Session{ID: "three-pressure-passes", Name: "Pressure"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	compiler := contextcompiler.New(store, nil)
	builder, err := NewRealContextBuilder(registry, compiler, func() int { return 10_000 })
	if err != nil {
		t.Fatal(err)
	}
	provider := &mockSummarizerProvider{summaryText: "must not be called"}
	worker := NewAgent(provider, tools.NewManager(registry), builder, newDurableMemory(store), nil)
	worker.workspace = root
	pressure := &alwaysPressured{}
	worker.pressureManager = pressure

	_, err = worker.Run(context.Background(), session, "keep reducing")
	if !errors.Is(err, ErrContextNotConverging) || pressure.calls != 3 || provider.calls != 0 {
		t.Fatalf("passes=%d provider_calls=%d err=%v", pressure.calls, provider.calls, err)
	}
	manifest, manifestErr := compiler.LatestManifest(context.Background(), session.ID)
	if manifestErr != nil || manifest.RequestID != "" {
		t.Fatalf("discarded pressure candidates wrote manifest %#v err=%v", manifest, manifestErr)
	}
}
