package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func todoWriteContext(t *testing.T) (context.Context, *state.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	const sessionID = "todo-tool"
	if _, err := store.SaveSession(context.Background(), state.SessionInput{ID: sessionID, Name: "Todo tool"}); err != nil {
		t.Fatal(err)
	}
	ctx := WithProgressScope(WithWorkspace(context.Background(), root), ProgressScope{SessionID: sessionID})
	return ctx, store, sessionID
}

func TestTodoWriteRejectsInvalidListsWithoutPersisting(t *testing.T) {
	tests := []struct {
		name    string
		tool    TodoWrite
		input   any
		message string
	}{
		{"empty content", TodoWrite{}, map[string]any{"todos": []map[string]any{{"content": "  ", "status": "pending"}}}, "cannot be empty"},
		{"trimmed duplicate", TodoWrite{}, map[string]any{"todos": []map[string]any{{"content": "Fix parser", "status": "pending"}, {"content": " Fix parser ", "status": "in_progress"}}}, "duplicate"},
		{"invalid status", TodoWrite{}, map[string]any{"todos": []map[string]any{{"content": "Fix parser", "status": "started"}}}, "invalid status"},
		{"multiple active", TodoWrite{}, map[string]any{"todos": []map[string]any{{"content": "One", "status": "in_progress"}, {"content": "Two", "status": "in_progress"}}}, "at most one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, store, sessionID := todoWriteContext(t)
			result, err := test.tool.Execute(ctx, test.input)
			if err != nil || result.Success || !strings.Contains(result.Message, test.message) {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			events, err := store.Events(ctx, state.EventQuery{SessionID: sessionID, Type: string(sessionlog.EventTodoWrite)})
			if err != nil || len(events) != 0 {
				t.Fatalf("validation persisted %d events: %v", len(events), err)
			}
		})
	}
}

func TestTodoWritePersistsAuthoritativeTrimmedList(t *testing.T) {
	ctx, store, sessionID := todoWriteContext(t)
	tool := &TodoWrite{AllowParallelInProgress: true}

	first, err := tool.Execute(ctx, map[string]any{"todos": []map[string]any{
		{"content": "  Inspect parser  ", "status": "completed"},
		{"content": "Fix parser", "status": "in_progress"},
		{"content": "Write test", "status": "in_progress"},
	}})
	if err != nil || !first.Success || !strings.Contains(first.Message, "0 pending, 2 in progress, 1 completed") {
		t.Fatalf("first result=%#v err=%v", first, err)
	}
	second, err := tool.Execute(ctx, map[string]any{"todos": []map[string]any{
		{"content": "Fix parser", "status": "completed"},
		{"content": "Ship", "status": "pending"},
	}})
	if err != nil || !second.Success {
		t.Fatalf("second result=%#v err=%v", second, err)
	}

	records, err := sessionlog.Load(ctx, store, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := sessionlog.Replay(records)
	if err != nil {
		t.Fatal(err)
	}
	if got := replay.Todos; len(got) != 2 || got[0].Content != "Fix parser" || got[0].Status != "completed" || got[1].Content != "Ship" {
		t.Fatalf("authoritative replacement = %#v", got)
	}

	cleared, err := tool.Execute(ctx, map[string]any{"todos": []map[string]any{}})
	if err != nil || !cleared.Success || !strings.Contains(cleared.Message, "0 pending, 0 in progress, 0 completed") {
		t.Fatalf("clear result=%#v err=%v", cleared, err)
	}
	events, err := store.Events(ctx, state.EventQuery{SessionID: sessionID, Type: string(sessionlog.EventTodoWrite)})
	if err != nil || len(events) != 3 {
		t.Fatalf("todo history=%d err=%v", len(events), err)
	}
}

func TestTodoWriteRequiresRuntimeScope(t *testing.T) {
	tool := &TodoWrite{}
	input := map[string]any{"todos": []map[string]any{}}
	result, err := tool.Execute(context.Background(), input)
	if err != nil || result.Success || !strings.Contains(result.Message, "session is required") {
		t.Fatalf("missing scope result=%#v err=%v", result, err)
	}
	result, err = tool.Execute(WithProgressScope(context.Background(), ProgressScope{SessionID: "session"}), input)
	if err != nil || result.Success || !strings.Contains(result.Message, "workspace is required") {
		t.Fatalf("missing workspace result=%#v err=%v", result, err)
	}
}
