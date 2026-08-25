package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

type mockSummarizerProvider struct {
	summaryText  string
	finishReason string
	usage        providers.Usage
	err          error
	calls        int
	prompt       *models.Prompt
	beforeReply  func()
}

func (m *mockSummarizerProvider) Chat(ctx context.Context, prompt *models.Prompt) (*providers.Completion, error) {
	m.calls++
	m.prompt = prompt
	if m.beforeReply != nil {
		m.beforeReply()
	}
	if m.err != nil {
		return nil, m.err
	}
	return &providers.Completion{Text: m.summaryText, FinishReason: m.finishReason, Usage: m.usage}, nil
}

func TestCompactionRangeToolBoundaryBalance(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "compact-boundary-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	// Seq 0: User Msg
	e0 := SessionEvent{Seq: 0, Type: EventUserMessage, Message: models.Message{Role: models.RoleUser, Content: strings.Repeat("u0 ", 20)}}
	_ = session.applyEvent(e0)

	// Seq 1: Assistant Msg with 2 tool calls
	e1 := SessionEvent{Seq: 1, Type: EventAssistantMessage, Message: models.Message{
		Role: models.RoleAssistant,
		ToolCalls: []models.ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: []byte(`{"path":"a.go"}`)},
			{ID: "call_2", Name: "read_file", Arguments: []byte(`{"path":"b.go"}`)},
		},
	}}
	_ = session.applyEvent(e1)

	// Seq 2: Tool Result 1
	e2 := SessionEvent{Seq: 2, Type: EventToolResult, Message: models.Message{Role: models.RoleTool, ToolCallID: "call_1", ToolName: "read_file", Content: strings.Repeat("res1 ", 50)}}
	_ = session.applyEvent(e2)

	// Seq 3: Tool Result 2
	e3 := SessionEvent{Seq: 3, Type: EventToolResult, Message: models.Message{Role: models.RoleTool, ToolCallID: "call_2", ToolName: "read_file", Content: strings.Repeat("res2 ", 50)}}
	_ = session.applyEvent(e3)

	// Seq 4: Assistant Msg (recent work)
	e4 := SessionEvent{Seq: 4, Type: EventAssistantMessage, Message: models.Message{Role: models.RoleAssistant, Content: strings.Repeat("recent work ", 20)}}
	_ = session.applyEvent(e4)

	// Measure tokens
	meter := NewDefaultTokenMeter()
	meas := meter.Measure(session, nil, 1000)

	// If retainTokens requires retaining through Seq 3, the boundary cannot cut between Seq 1 and Seq 3!
	// balanceToolBoundary must keep Seq 1..3 together in prefix or together in tail.
	candidateSeqs, err := SelectCompactionRange(session, meas)
	if err != nil {
		t.Fatalf("SelectCompactionRange error: %v", err)
	}

	// If candidateSeqs is non-empty, verify it never ends with Seq 1 or Seq 2 (incomplete tool batch)
	if len(candidateSeqs) > 0 {
		lastSeq := candidateSeqs[len(candidateSeqs)-1]
		if lastSeq == 1 || lastSeq == 2 {
			t.Fatalf("tool boundary was split at seq %d! Candidates: %v", lastSeq, candidateSeqs)
		}
	}
}

func TestCompactionLifecycleSuccessAndSurfaceReplacement(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "compact-lifecycle-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	// Build large history
	for i := 0; i < 6; i++ {
		e := SessionEvent{
			Seq:  int64(i),
			Type: EventUserMessage,
			Message: models.Message{
				Role:    models.RoleUser,
				Content: fmt.Sprintf("Turn %d: %s", i, strings.Repeat("detailed user specifications and context ", 30)),
			},
		}
		_ = persistSessionEvent(context.Background(), store, session.ID, e)
		_ = session.applyEvent(e)
	}

	nodesBefore := session.Nodes()
	if len(nodesBefore) != 6 {
		t.Fatalf("expected 6 nodes before compaction, got %d", len(nodesBefore))
	}

	meter := NewDefaultTokenMeter()
	meas := meter.Measure(session, nil, 500) // Small limit -> triggers retention calculation

	summaryContent := "## Primary Request and Intent\n- Goal was achieved\n\n## Next Step\n- Continue testing"
	mockProv := &mockSummarizerProvider{summaryText: summaryContent}
	engine := NewDefaultCompactionEngine()

	success, err := engine.Compact(context.Background(), store, session, mockProv, nil, meas)
	if err != nil || !success {
		t.Fatalf("compaction failed: success=%t, err=%v", success, err)
	}

	// Verify surface nodes were replaced
	nodesAfter := session.Nodes()
	if len(nodesAfter) >= len(nodesBefore) {
		t.Fatalf("expected surface nodes to shrink, before=%d, after=%d (%v)", len(nodesBefore), len(nodesAfter), nodesAfter)
	}

	// Verify first derived message is now the framed summary checkpoint
	derived := session.DeriveMessages()
	if len(derived) == 0 || !strings.Contains(derived[0].Content, "<compacted-summary>") {
		t.Fatalf("first derived message missing <compacted-summary>: %#v", derived[0])
	}
	if !strings.Contains(derived[0].Content, "## Primary Request and Intent") {
		t.Fatalf("derived checkpoint missing summary content")
	}
}

func TestCompactionRejectsOversizedSummary(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "compact-reject-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	for i := 0; i < 6; i++ {
		e := SessionEvent{
			Seq:     int64(i),
			Type:    EventUserMessage,
			Message: models.Message{Role: models.RoleUser, Content: fmt.Sprintf("message %d: %s", i, strings.Repeat("content payload ", 20))},
		}
		_ = persistSessionEvent(context.Background(), store, session.ID, e)
		_ = session.applyEvent(e)
	}

	nodesBefore := session.Nodes()
	meas := NewDefaultTokenMeter().Measure(session, nil, 1000)

	// Mock summary that is larger than the original messages
	oversizedSummary := strings.Repeat("Enormous summary text that fails token reduction ", 500)
	mockProv := &mockSummarizerProvider{summaryText: oversizedSummary}
	engine := NewDefaultCompactionEngine()

	success, err := engine.Compact(context.Background(), store, session, mockProv, nil, meas)
	if success || err == nil {
		t.Fatalf("expected compaction to return error when summary is oversized: success=%t, err=%v", success, err)
	}

	// Verify surface nodes were NOT modified on failure
	nodesAfter := session.Nodes()
	if len(nodesAfter) != len(nodesBefore) {
		t.Fatalf("surface nodes mutated despite failed compaction: %v vs %v", nodesBefore, nodesAfter)
	}
}

func TestCompactionUsesExactFrozenRequestAndRejectsMaxTokens(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()
	session := &Session{ID: "compact-max-token", Provider: "mock", Model: "model"}
	_ = session.AttachSurface(context.Background(), store)
	for i := 0; i < 6; i++ {
		event := SessionEvent{Seq: int64(i), Type: EventUserMessage, Message: models.Message{Role: models.RoleUser, Content: fmt.Sprintf("message %d %s", i, strings.Repeat("context ", 40))}}
		_ = persistSessionEvent(context.Background(), store, session.ID, event)
		_ = session.applyEvent(event)
	}
	prompt := &models.Prompt{
		System: "exact system", Messages: session.DeriveMessages(),
		ToolDefinitions: []models.ToolDefinition{{Name: "probe", InputSchema: []byte(`{"type":"object"}`)}},
	}
	provider := &mockSummarizerProvider{summaryText: "short summary", finishReason: string(providers.FinishMaxTokens), usage: providers.Usage{InputTokens: 123, OutputTokens: 45}}
	before := session.Nodes()
	success, err := NewDefaultCompactionEngine().Compact(context.Background(), store, session, provider, prompt, NewDefaultTokenMeter().Measure(session, prompt, 600))
	if success || err == nil || !strings.Contains(err.Error(), "max_tokens") {
		t.Fatalf("max-token compaction success=%t err=%v", success, err)
	}
	if provider.prompt == nil || provider.prompt.System != prompt.System || len(provider.prompt.ToolDefinitions) != 1 || len(provider.prompt.Messages) != len(prompt.Messages)+1 {
		t.Fatalf("compaction request did not retain exact envelope: %#v", provider.prompt)
	}
	if instruction := provider.prompt.Messages[len(provider.prompt.Messages)-1].Content; !strings.Contains(instruction, "oldest prefix") || !strings.Contains(instruction, "retained tail") {
		t.Fatalf("compaction boundary instruction = %q", instruction)
	}
	if after := session.Nodes(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("max-token summary changed surface: %v -> %v", before, after)
	}
	var terminal sessionlog.CompactionPayload
	for _, event := range session.events {
		if event.Type == EventCompactionEnd {
			terminal, _ = event.Data.(sessionlog.CompactionPayload)
		}
	}
	if terminal.FinishReason != string(providers.FinishMaxTokens) || terminal.InputTokens != 123 || terminal.OutputTokens != 45 || terminal.Provider != "mock" || terminal.Model != "model" {
		t.Fatalf("terminal compaction diagnostics = %#v", terminal)
	}
}

func TestCompactionRejectsSummaryAfterTargetSurfaceChanges(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()
	session := &Session{ID: "compact-shift", Provider: "mock", Model: "model"}
	_ = session.AttachSurface(context.Background(), store)
	for i := 0; i < 6; i++ {
		event := SessionEvent{Seq: int64(i), Type: EventUserMessage, Message: models.Message{Role: models.RoleUser, Content: fmt.Sprintf("message %d %s", i, strings.Repeat("context ", 40))}}
		_ = persistSessionEvent(context.Background(), store, session.ID, event)
		_ = session.applyEvent(event)
	}
	var mutationErr error
	provider := &mockSummarizerProvider{summaryText: "short summary", beforeReply: func() {
		node := session.Nodes()[0]
		replacement, _ := sessionlog.New(EventUserMessage, models.Message{Role: models.RoleUser, Content: "concurrent replacement"})
		replacement.Message = models.Message{Role: models.RoleUser, Content: "concurrent replacement"}
		replacement.SurfaceOp = &SurfaceOp{Kind: surfaceOpReplace, StartSeq: node, EndSeq: node}
		replacement.SourceEventSeqs = []int64{node}
		mutationErr = appendAndApplySessionEvent(context.Background(), store, session, replacement)
	}}
	prompt := &models.Prompt{System: "system", Messages: session.DeriveMessages()}
	success, err := NewDefaultCompactionEngine().Compact(context.Background(), store, session, provider, prompt, NewDefaultTokenMeter().Measure(session, prompt, 600))
	if mutationErr != nil {
		t.Fatal(mutationErr)
	}
	if success || err == nil || !strings.Contains(err.Error(), "surface modified") {
		t.Fatalf("shifted compaction success=%t err=%v", success, err)
	}
}
