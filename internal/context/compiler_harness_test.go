package contextcompiler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func TestCanonicalMessageReconstruction(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	if _, err := store.SaveSession(ctx, state.SessionInput{ID: "sess-1", Name: "Session"}); err != nil {
		t.Fatal(err)
	}

	call := models.ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"app/page.tsx"}`)}
	callBytes, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}

	progress := models.TurnProgress{Progress: "Inspected layout", NextGoal: "Check page.tsx"}
	progBytes, err := json.Marshal(progress)
	if err != nil {
		t.Fatal(err)
	}

	// Assistant message with empty text, 1 tool call, and turn progress metadata
	asstMsg, err := store.AppendMessage(ctx, state.MessageInput{
		ID:        "msg-asst-1",
		SessionID: "sess-1",
		Role:      "assistant",
		TaskID:    "task-1",
		Parts: []state.MessagePartInput{
			{Kind: "turn_progress", Metadata: progBytes},
			{Kind: "assistant_tool_call", Metadata: callBytes},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Tool result message
	metaBytes, err := json.Marshal(map[string]string{"tool_call_id": "call-1", "tool_name": "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	toolMsg, err := store.AppendMessage(ctx, state.MessageInput{
		ID:        "msg-tool-1",
		SessionID: "sess-1",
		Role:      "tool",
		TaskID:    "task-1",
		Parts: []state.MessagePartInput{
			{Kind: "tool_result", Text: "export default function Page() {}", Metadata: metaBytes},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Verify state.ToModelMessage reconstruction
	m1 := asstMsg.ToModelMessage()
	if m1.Role != models.RoleAssistant {
		t.Fatalf("expected RoleAssistant, got %s", m1.Role)
	}
	if len(m1.ToolCalls) != 1 || m1.ToolCalls[0].Name != "read_file" {
		t.Fatalf("expected 1 tool call 'read_file', got %#v", m1.ToolCalls)
	}
	if m1.TurnProgress == nil || m1.TurnProgress.Progress != "Inspected layout" {
		t.Fatalf("expected TurnProgress 'Inspected layout', got %#v", m1.TurnProgress)
	}

	m2 := toolMsg.ToModelMessage()
	if m2.Role != models.RoleTool || m2.ToolCallID != "call-1" || m2.ToolName != "read_file" {
		t.Fatalf("tool message reconstruction failed: %#v", m2)
	}

	// 2. Verify Compiler assembles them into causal interactions
	compiler := New(store, nil)
	prompt, err := compiler.Compile(ctx, Request{
		SessionID:    "sess-1",
		TaskID:       "task-1",
		Control:      "control instructions",
		ContextLimit: 4096,
		PromptMetadata: models.PromptMetadata{
			Profile: "plan_research",
		},
		History: []models.Message{m1, m2},
	})
	if err != nil {
		t.Fatal(err)
	}

	if prompt.Request == nil {
		t.Fatal("expected Canonical Request IR (prompt.Request) to be populated")
	}
	if prompt.Request.RequestID == "" || prompt.Request.SessionID != "sess-1" {
		t.Fatalf("unexpected prompt.Request metadata: %#v", prompt.Request)
	}
	if prompt.Request.TurnID == "" {
		t.Fatal("expected TurnID to be set from working-set generation")
	}
	if len(prompt.Request.Interactions) != 1 {
		t.Fatalf("expected 1 interaction, got %d", len(prompt.Request.Interactions))
	}
	inter := prompt.Request.Interactions[0]
	if len(inter.Assistant.ToolCalls) != 1 || len(inter.ToolResults) != 1 {
		t.Fatalf("expected causal pair of 1 tool call and 1 tool result, got %#v", inter)
	}
	if inter.ToolResults[0].ToolCallID != "call-1" {
		t.Fatalf("expected tool_call_id 'call-1', got %s", inter.ToolResults[0].ToolCallID)
	}
}

func TestCausalAtomicInteractionPruning(t *testing.T) {
	ctx, store := context.Background(), openStore(t)
	if _, err := store.SaveSession(ctx, state.SessionInput{ID: "sess-atomic", Name: "Session"}); err != nil {
		t.Fatal(err)
	}

	call := models.ToolCall{ID: "call-a", Name: "search_text", Arguments: json.RawMessage(`{"query":"test"}`)}
	callBytes, _ := json.Marshal(call)
	metaBytes, _ := json.Marshal(map[string]string{"tool_call_id": "call-a", "tool_name": "search_text"})

	// Append paired assistant call and tool result
	_, _ = store.AppendMessage(ctx, state.MessageInput{
		ID:        "msg-a1",
		SessionID: "sess-atomic",
		Role:      "assistant",
		TaskID:    "task-atomic",
		Parts: []state.MessagePartInput{
			{Kind: "assistant_tool_call", Metadata: callBytes},
		},
	})
	_, _ = store.AppendMessage(ctx, state.MessageInput{
		ID:        "msg-t1",
		SessionID: "sess-atomic",
		Role:      "tool",
		TaskID:    "task-atomic",
		Parts: []state.MessagePartInput{
			{Kind: "tool_result", Text: "search match 1", Metadata: metaBytes},
		},
	})

	compiler := New(store, nil)
	prompt, err := compiler.Compile(ctx, Request{
		SessionID:    "sess-atomic",
		TaskID:       "task-atomic",
		Control:      "control instructions",
		ContextLimit: 4096,
		History: []models.Message{
			{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{call}},
			{Role: models.RoleTool, Content: "search match 1", ToolCallID: "call-a", ToolName: "search_text"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify both assistant call and tool result are in prompt.Messages
	if len(prompt.Messages) != 2 {
		t.Fatalf("expected exactly 2 messages in prompt, got %d: %#v", len(prompt.Messages), prompt.Messages)
	}
	if prompt.Messages[0].Role != models.RoleAssistant || len(prompt.Messages[0].ToolCalls) != 1 {
		t.Fatalf("expected assistant message with 1 tool call, got %#v", prompt.Messages[0])
	}
	if prompt.Messages[1].Role != models.RoleTool || prompt.Messages[1].ToolCallID != "call-a" {
		t.Fatalf("expected tool message with ID 'call-a', got %#v", prompt.Messages[1])
	}
}
