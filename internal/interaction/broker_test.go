package interaction

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type approvalTool struct{ calls int }

func (*approvalTool) Name() string                      { return "write_file" }
func (*approvalTool) Description() string               { return "write" }
func (*approvalTool) Schema() any                       { return map[string]any{} }
func (*approvalTool) Capabilities() tools.CapabilitySet { return tools.CapabilityWriteWorkspace }
func (t *approvalTool) Execute(context.Context, any) (*tools.ToolResult, error) {
	t.calls++
	return tools.BuildToolResult(true, "done", nil), nil
}

func brokerTestStore(t *testing.T) *state.Store {
	t.Helper()
	t.Setenv("SUPREMO_DATA_DIR", t.TempDir())
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	if _, err := store.SaveSession(context.Background(), state.SessionInput{ID: "chat", Name: "Chat", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestApprovalPersistsBeforeToolRelease(t *testing.T) {
	store := brokerTestStore(t)
	broker := NewBroker(store)
	registry := tools.NewRegistry()
	tool := &approvalTool{}
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	manager := tools.NewManager(registry)
	manager.SetApprovalRecorder(broker)
	ctx := tools.WithProgressScope(tools.WithApprovalMode(context.Background(), tools.ApprovalStrict), tools.ProgressScope{SessionID: "chat", RunID: "run-1", CallID: "call-1"})
	done := make(chan error, 1)
	go func() { _, err := manager.Execute(ctx, tool.Name(), map[string]any{"path": "a"}); done <- err }()
	interactionID := waitForInteraction(t, store, sessionlog.EventInteractionRequest)
	if tool.calls != 0 {
		t.Fatal("tool ran before approval")
	}
	if err := manager.ResolveApproval(interactionID, tools.ApprovalResolution{Decision: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if tool.calls != 1 {
		t.Fatalf("tool calls = %d", tool.calls)
	}
	_ = waitForInteraction(t, store, sessionlog.EventInteractionResolve)
}

func TestQuestionPersistsAndResolvesThroughBroker(t *testing.T) {
	store := brokerTestStore(t)
	broker := NewBroker(store)
	done := make(chan questions.AnswerSet, 1)
	go func() {
		answers, _ := broker.Ask(context.Background(), questions.Request{SessionID: "chat", RunID: "run-1", Questions: []questions.Question{{ID: "q", Question: "Continue?"}}})
		done <- answers
	}()
	id := waitForInteraction(t, store, sessionlog.EventInteractionRequest)
	want := questions.AnswerSet{Answers: []questions.Answer{{ID: "q", Selected: []string{"yes"}}}}
	if err := broker.ResolveQuestion(context.Background(), "chat", id, want); err != nil {
		t.Fatal(err)
	}
	if got := <-done; len(got.Answers) != 1 || got.Answers[0].Selected[0] != "yes" {
		t.Fatalf("answers = %#v", got)
	}
}

func waitForInteraction(t *testing.T, store *state.Store, kind sessionlog.EventType) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events, err := store.Events(context.Background(), state.EventQuery{SessionID: "chat", Type: string(kind)})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) > 0 {
			var envelope struct {
				Data struct {
					InteractionID string `json:"interaction_id"`
				} `json:"data"`
			}
			if json.Unmarshal(events[len(events)-1].Payload, &envelope) == nil && envelope.Data.InteractionID != "" {
				return envelope.Data.InteractionID
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", kind)
	return ""
}
