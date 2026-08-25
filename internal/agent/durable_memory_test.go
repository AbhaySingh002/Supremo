package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/filesystem"
)

func TestDurableMemoryRoundTripsNativeToolCallsWithoutReparsingText(t *testing.T) {
	root := t.TempDir()
	if err := (&Session{ID: "session", Name: "Memory"}).Save(root); err != nil {
		t.Fatal(err)
	}
	memory, err := NewDurableMemory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	ctx := context.Background()
	call := models.ToolCall{ID: "provider-call-7", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}
	if err := memory.Append(ctx, "session", models.Message{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{call}}); err != nil {
		t.Fatal(err)
	}
	if err := memory.Append(ctx, "session", models.Message{Role: models.RoleTool, Content: "contents", ToolCallID: call.ID, ToolName: call.Name}); err != nil {
		t.Fatal(err)
	}
	if _, err := memory.store.AppendMessage(ctx, state.MessageInput{SessionID: "session", Role: string(models.RoleAssistant), Parts: []state.MessagePartInput{{Kind: "text", Text: `{"tool_calls":[{"id":"legacy"}]}`}}}); err != nil {
		t.Fatal(err)
	}

	messages, err := memory.ReadAllTranscript(ctx, "session")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || len(messages[0].ToolCalls) != 1 || messages[0].ToolCalls[0].ID != call.ID || messages[0].ToolCalls[0].Name != call.Name || messages[0].ToolCalls[0].Synthetic {
		t.Fatalf("assistant call did not round-trip: %#v", messages)
	}
	if messages[1].Role != models.RoleTool || messages[1].ToolCallID != call.ID || messages[1].ToolName != call.Name || messages[1].Content != "contents" {
		t.Fatalf("tool result correlation did not round-trip: %#v", messages[1])
	}
	if len(messages[2].ToolCalls) != 0 || messages[2].Content != `{"tool_calls":[{"id":"legacy"}]}` {
		t.Fatalf("legacy text was interpreted as an executable call: %#v", messages[2])
	}
}

func TestDurableMemoryUsesAcceptedMessageAndRunIdentity(t *testing.T) {
	root := t.TempDir()
	if err := (&Session{ID: "accepted", Name: "Accepted"}).Save(root); err != nil {
		t.Fatal(err)
	}
	memory, err := NewDurableMemory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	ctx := sessionlog.WithEventMeta(context.Background(), sessionlog.EventMeta{CorrelationID: "run-1", CausationID: "message-1"})
	if err := memory.Append(ctx, "accepted", models.Message{LocalID: "message-1", Role: models.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	messages, err := memory.store.Messages(ctx, "accepted", false)
	if err != nil || len(messages) != 1 || messages[0].ID != "message-1" {
		t.Fatalf("messages = %#v, %v", messages, err)
	}
	events, err := memory.store.Events(ctx, state.EventQuery{SessionID: "accepted"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == string(sessionlog.EventUserMessage) && (event.CorrelationID != "run-1" || event.CausationID != "message-1") {
			t.Fatalf("surface correlation = %#v", event)
		}
	}
}

func TestObservationCacheHitRestoresFullContent(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.Open(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(tempDir) })
	filePath := filepath.Join(tempDir, "sample.txt")
	fileContent := "Hello, Supremo SWE-agent ACI Harness!"
	if err := os.WriteFile(filePath, []byte(fileContent), 0644); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry()
	if err := registry.Register(&filesystem.ListDirectory{}); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{workspace: tempDir, toolManager: tools.NewManager(registry), hooks: observationHooks(tempDir)}
	ctx := tools.WithLifecycleRecorder(tools.WithWorkspace(context.Background(), tempDir), &stateRecorder{store: store, root: tempDir, sessionID: "sess-cache"})
	call := models.ToolCall{
		ID:        "call-list-1",
		Name:      "list_directory",
		Arguments: json.RawMessage(`{"path":"."}`),
	}
	sum1 := agent.executeAll(ctx, &Session{ID: "sess-cache"}, []models.ToolCall{call}, ToolExecutionOptions{TaskID: "task-cache"})
	if sum1.Err != nil {
		t.Fatal(sum1.Err)
	}
	if sum1.ReusedCount != 0 {
		t.Fatal("expected first call to be physical execution (reused=false)")
	}
	if len(sum1.Observations) != 1 || !sum1.Observations[0].Success || !strings.Contains(sum1.Observations[0].Output, "sample.txt") {
		t.Fatalf("first execution missing expected file content: %#v", sum1.Observations)
	}
	sum2 := agent.executeAll(ctx, &Session{ID: "sess-cache"}, []models.ToolCall{call}, ToolExecutionOptions{TaskID: "task-cache"})
	if sum2.Err != nil {
		t.Fatal(sum2.Err)
	}
	if sum2.ReusedCount != 1 {
		t.Fatal("expected second call to hit durable cache (reused=true)")
	}
	if len(sum2.Observations) != 1 || !sum2.Observations[0].Success || !strings.Contains(sum2.Observations[0].Output, "sample.txt") {
		t.Fatalf("cached observation failed to restore real content: got %#v", sum2.Observations)
	}
}

func TestLongHorizon30TurnTrajectorySimulation(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.Open(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(tempDir) })

	sessionID := "sess-trajectory-30"
	taskID := "task-trajectory-30"
	if _, err := store.SaveSession(context.Background(), state.SessionInput{ID: sessionID, Name: "Session"}); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 5; i++ {
		p := filepath.Join(tempDir, fmt.Sprintf("file_%d.go", i))
		_ = os.WriteFile(p, fmt.Appendf(nil, "package main\n// File %d contents\nfunc Step%d() {}\n", i, i), 0644)
	}

	durableMem := newDurableMemory(store)

	for turn := 1; turn <= 30; turn++ {
		targetFile := fmt.Sprintf("file_%d.go", ((turn-1)%5)+1)
		callID := fmt.Sprintf("call-%d", turn)

		call := models.ToolCall{
			ID:        callID,
			Name:      "read_file",
			Arguments: json.RawMessage(fmt.Sprintf(`{"path":%q}`, targetFile)),
		}

		progress := &models.TurnProgress{
			Progress: fmt.Sprintf("Completed turn %d inspection", turn),
			NextGoal: fmt.Sprintf("Inspect turn %d targets", turn+1),
		}

		asstMsg := models.Message{
			Role:         models.RoleAssistant,
			Content:      fmt.Sprintf("Turn %d: inspecting %s", turn, targetFile),
			ToolCalls:    []models.ToolCall{call},
			TurnProgress: progress,
			TaskID:       taskID,
		}

		if err := durableMem.Append(context.Background(), sessionID, asstMsg); err != nil {
			t.Fatalf("turn %d append assistant failed: %v", turn, err)
		}

		toolResMsg := models.Message{
			Role:       models.RoleTool,
			Content:    fmt.Sprintf("package main\n// File %d contents\nfunc Step%d() {}\n", ((turn-1)%5)+1, ((turn-1)%5)+1),
			ToolCallID: callID,
			ToolName:   "read_file",
			TaskID:     taskID,
		}

		if err := durableMem.Append(context.Background(), sessionID, toolResMsg); err != nil {
			t.Fatalf("turn %d append tool result failed: %v", turn, err)
		}
	}

	transcript, err := durableMem.ReadAllTranscript(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript) != 60 {
		t.Fatalf("expected 60 messages in durable transcript (30 assistant + 30 tool), got %d", len(transcript))
	}

	for i := 0; i < len(transcript); i += 2 {
		asst := transcript[i]
		if asst.Role != models.RoleAssistant || len(asst.ToolCalls) != 1 || asst.TurnProgress == nil {
			t.Fatalf("transcript index %d malformed: %#v", i, asst)
		}
		tool := transcript[i+1]
		if tool.Role != models.RoleTool || tool.ToolCallID != asst.ToolCalls[0].ID {
			t.Fatalf("transcript tool result index %d mismatched: %#v", i+1, tool)
		}
	}
}
