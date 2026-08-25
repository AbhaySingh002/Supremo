package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func todoSession(t *testing.T) (string, *state.Store, *Session, context.Context) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	session := &Session{ID: "todo-session", Name: "Todo"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: session.ID})
	return root, store, session, ctx
}

func TestTodoProjectionReplaysResetsAndStaysOffModelSurface(t *testing.T) {
	_, store, session, ctx := todoSession(t)
	tool := &tools.TodoWrite{}
	for _, input := range []any{
		map[string]any{"todos": []map[string]any{
			{"content": "Task 1", "status": "completed"},
			{"content": "Task 2", "status": "pending"},
		}},
		map[string]any{"todos": []map[string]any{
			{"content": "Task 2", "status": "in_progress"},
			{"content": "Task 3", "status": "pending"},
		}},
	} {
		result, err := tool.Execute(ctx, input)
		if err != nil || !result.Success {
			t.Fatalf("todo write result=%#v err=%v", result, err)
		}
	}

	reloaded := &Session{ID: session.ID}
	if err := reloaded.AttachSurface(ctx, store); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.CurrentTodos(); len(got) != 2 || got[0].Content != "Task 2" || got[0].Status != TodoInProgress || got[1].Content != "Task 3" {
		t.Fatalf("replayed todos = %#v", got)
	}
	for _, message := range reloaded.DeriveMessages() {
		if strings.Contains(message.Content, "Task 2") || strings.Contains(message.Content, "Task 3") {
			t.Fatalf("todo projection leaked onto model surface: %#v", message)
		}
	}

	turn, err := sessionlog.New(sessionlog.EventTurnStart, sessionlog.TurnStartPayload{Turn: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionlog.Append(ctx, store, session.ID, turn); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.AttachSurface(ctx, store); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.CurrentTodos(); len(got) != 0 {
		t.Fatalf("new turn did not clear standing todos: %#v", got)
	}
	events, err := store.Events(ctx, state.EventQuery{SessionID: session.ID, Type: string(sessionlog.EventTodoWrite)})
	if err != nil || len(events) != 2 {
		t.Fatalf("historical todo events=%d err=%v", len(events), err)
	}
}

func TestTodoPendingDoesNotForceAgentContinuation(t *testing.T) {
	provider := &scriptedProvider{chat: func(_ context.Context, n int, _ *models.Prompt) (*providers.Completion, error) {
		if n == 0 {
			arguments, _ := json.Marshal(map[string]any{"todos": []map[string]any{{"content": "Task still pending", "status": "pending"}}})
			return &providers.Completion{
				FinishReason: string(providers.FinishToolCalls),
				ToolCalls:    []models.ToolCall{{ID: "todo", Name: "todo_write", Arguments: arguments}},
			}, nil
		}
		return &providers.Completion{FinishReason: string(providers.FinishStop), Text: "Work completed."}, nil
	}}
	worker, session := driverAgent(t, provider, &tools.TodoWrite{}, &driverLifecycle{activeTools: []string{"todo_write"}})
	session.ApprovalMode = tools.ApprovalSuperman

	answer, err := worker.Run(context.Background(), session, "Do work")
	if err != nil || answer != "Work completed." || provider.calls != 2 {
		t.Fatalf("answer=%q calls=%d err=%v", answer, provider.calls, err)
	}
}
