package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func TestPruneTextBoundariesAndUnicode(t *testing.T) {
	// 1. Result <= 8192 chars unchanged
	short := strings.Repeat("a", 8192)
	pShort, changed := PruneText(short)
	if changed || pShort != short {
		t.Fatalf("expected 8192 chars to remain unpruned")
	}

	// 2. Result 8193 chars gets pruned
	long := strings.Repeat("x", 8193)
	pLong, changed := PruneText(long)
	if !changed {
		t.Fatalf("expected 8193 chars to be pruned")
	}
	if !strings.Contains(pLong, PruneMarker) {
		t.Fatalf("expected prune marker in pruned output")
	}
	if len([]rune(pLong)) != 4096+len([]rune(PruneMarker))+1024 {
		t.Fatalf("expected pruned length %d runes, got %d", 4096+len([]rune(PruneMarker))+1024, len([]rune(pLong)))
	}

	// 3. Unicode safety (multi-byte runes)
	unicodeInput := strings.Repeat("🚀", 5000) + strings.Repeat("世界", 2000) // 9000 runes total
	pUnicode, changed := PruneText(unicodeInput)
	if !changed {
		t.Fatalf("expected unicode input to be pruned")
	}
	runes := []rune(pUnicode)
	if len(runes) != 4096+len([]rune(PruneMarker))+1024 {
		t.Fatalf("expected %d runes, got %d", 4096+len([]rune(PruneMarker))+1024, len(runes))
	}
	if !strings.HasPrefix(pUnicode, strings.Repeat("🚀", 4096)) {
		t.Fatalf("expected prefix to start with 4096 rocket emojis")
	}

	// 4. Idempotence
	pIdempotent, changed2 := PruneText(pUnicode)
	if changed2 || pIdempotent != pUnicode {
		t.Fatalf("expected second pass to be no-op")
	}
}

func TestToolResultPrunerSurfaceReplacementAndProvenance(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "prune-session", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	// Append user message (seq 0)
	e0 := SessionEvent{
		Seq:     0,
		Type:    EventUserMessage,
		Message: models.Message{Role: models.RoleUser, Content: "Run search"},
	}
	_ = session.applyEvent(e0)
	_ = persistSessionEvent(context.Background(), store, session.ID, e0)

	// Append assistant tool call (seq 1)
	e1 := SessionEvent{
		Seq:  1,
		Type: EventAssistantMessage,
		Message: models.Message{
			Role: models.RoleAssistant,
			ToolCalls: []models.ToolCall{
				{ID: "call_abc123", Name: "grep_search", Arguments: []byte(`{"query":"test"}`)},
			},
		},
	}
	_ = session.applyEvent(e1)
	_ = persistSessionEvent(context.Background(), store, session.ID, e1)

	// Append oversized tool result (seq 2)
	oversizedContent := strings.Repeat("LOG_LINE: data payload here\n", 400) // ~11,200 chars
	e2 := SessionEvent{
		Seq:  2,
		Type: EventToolResult,
		Message: models.Message{
			Role:       models.RoleTool,
			ToolCallID: "call_abc123",
			ToolName:   "grep_search",
			Content:    oversizedContent,
		},
	}
	_ = session.applyEvent(e2)
	_ = persistSessionEvent(context.Background(), store, session.ID, e2)

	// Surface before pruning has [0, 1, 2]
	nodesBefore := session.Nodes()
	if len(nodesBefore) != 3 || nodesBefore[2] != 2 {
		t.Fatalf("unexpected nodes before pruning: %v", nodesBefore)
	}

	pruner := NewDefaultToolResultPruner()
	count, err := pruner.Prune(context.Background(), store, session)
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 result pruned, got %d", count)
	}

	// Verify Surface after pruning:
	// The replacement receives SQLite's durable event sequence. The old local
	// sequence was an implementation detail, so only its position and monotonic
	// identity are part of the surface contract.
	nodesAfter := session.Nodes()
	if len(nodesAfter) != 3 || nodesAfter[0] != 0 || nodesAfter[1] != 1 || nodesAfter[2] <= 2 {
		t.Fatalf("expected replacement after original surface nodes, got %v", nodesAfter)
	}

	// Verify derived messages
	derived := session.DeriveMessages()
	if len(derived) != 3 {
		t.Fatalf("expected 3 derived messages, got %d", len(derived))
	}
	toolMsg := derived[2]
	if toolMsg.ToolCallID != "call_abc123" || toolMsg.ToolName != "grep_search" {
		t.Fatalf("tool identity mutated: callID=%s name=%s", toolMsg.ToolCallID, toolMsg.ToolName)
	}
	if !strings.Contains(toolMsg.Content, PruneMarker) {
		t.Fatalf("derived tool message missing prune marker")
	}

	// Verify original event seq 2 remains immutable in events log
	origEvent, ok := session.eventBySeq(2)
	if !ok || origEvent.Message.Content != oversizedContent {
		t.Fatalf("original durable event was mutated or missing")
	}

	// Verify second pruning pass is idempotent
	count2, err2 := pruner.Prune(context.Background(), store, session)
	if err2 != nil || count2 != 0 {
		t.Fatalf("expected 0 prunes on second pass, got %d (err: %v)", count2, err2)
	}
}
