package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/filesystem"
)

type dummyCustomTool struct {
	name     string
	err      error
	executed *int
}

func (d *dummyCustomTool) Name() string                      { return d.name }
func (d *dummyCustomTool) Description() string               { return "dummy tool" }
func (d *dummyCustomTool) Schema() any                       { return map[string]any{"type": "object"} }
func (d *dummyCustomTool) Capabilities() tools.CapabilitySet { return 0 }
func (d *dummyCustomTool) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	if d.executed != nil {
		*d.executed++
	}
	if d.err != nil {
		return nil, d.err
	}
	return &tools.ToolResult{
		Success: true,
		Status:  tools.ToolStatusCompleted,
		Message: "custom execution completed",
		Data:    map[string]any{"executed": true},
	}, nil
}

func TestToolExecutor_SingleCallSuccess(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })

	filePath := filepath.Join(root, "test.txt")
	if err := os.WriteFile(filePath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	registry := tools.NewRegistry()
	if err := registry.Register(&filesystem.ReadFile{}); err != nil {
		t.Fatal(err)
	}

	agent := &Agent{workspace: root, toolManager: tools.NewManager(registry), transcript: newDurableMemory(store)}
	session := &Session{ID: "sess-1", Name: "Test Session"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}

	ctx := tools.WithLifecycleRecorder(tools.WithWorkspace(context.Background(), root), &stateRecorder{store: store, root: root, sessionID: session.ID})
	call := models.ToolCall{ID: "call_abc_123", Name: "read_file", Arguments: json.RawMessage(`{"path":"test.txt"}`)}

	summary := agent.executeAll(ctx, session, []models.ToolCall{call}, ToolExecutionOptions{TaskID: "task-1"})
	if summary.Err != nil {
		t.Fatalf("unexpected error: %v", summary.Err)
	}
	if summary.Outcome != tools.ToolOutcomeSuccess {
		t.Fatalf("expected outcome SUCCESS, got %v", summary.Outcome)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	res := summary.Results[0]
	if res.CallID != "call_abc_123" {
		t.Fatalf("expected CallID call_abc_123, got %s", res.CallID)
	}
	if !res.Success || !strings.Contains(res.Output, "hello world") {
		t.Fatalf("expected output to contain hello world, got: %s", res.Output)
	}

	// Verify surface messages match
	messages, err := agent.ReadAllTranscript(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundToolResult := false
	for _, msg := range messages {
		if msg.Role == models.RoleTool {
			foundToolResult = true
			if msg.ToolCallID != "call_abc_123" {
				t.Fatalf("Tool message missing correct ToolCallID: %#v", msg)
			}
			if msg.ToolName != "read_file" {
				t.Fatalf("Tool message missing correct ToolName: %#v", msg)
			}
		}
	}
	if !foundToolResult {
		t.Fatal("expected Tool result message in transcript")
	}
}

func TestToolExecutor_UnknownToolProducesRecoverableResult(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })

	registry := tools.NewRegistry()
	agent := &Agent{workspace: root, toolManager: tools.NewManager(registry), transcript: newDurableMemory(store)}
	session := &Session{ID: "sess-unknown", Name: "Unknown"}
	_ = session.Save(root)

	ctx := tools.WithLifecycleRecorder(tools.WithWorkspace(context.Background(), root), &stateRecorder{store: store, root: root, sessionID: session.ID})
	call := models.ToolCall{ID: "call_unk", Name: "non_existent_tool", Arguments: json.RawMessage(`{}`)}

	summary := agent.executeAll(ctx, session, []models.ToolCall{call}, ToolExecutionOptions{TaskID: "task-unk"})
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	res := summary.Results[0]
	if res.CallID != "call_unk" {
		t.Fatalf("expected CallID call_unk, got %s", res.CallID)
	}
	if res.Success {
		t.Fatal("expected failure for unknown tool")
	}
	if res.Outcome != tools.ToolOutcomeRecoverable {
		t.Fatalf("expected ToolOutcomeRecoverable, got %v", res.Outcome)
	}
}

func TestToolExecutor_CancellationBeforeDispatchSynthesizesAborted(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })

	registry := tools.NewRegistry()
	_ = registry.Register(&filesystem.ReadFile{})

	agent := &Agent{workspace: root, toolManager: tools.NewManager(registry), transcript: newDurableMemory(store)}
	session := &Session{ID: "sess-cancel", Name: "Cancel"}
	_ = session.Save(root)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before execution

	calls := []models.ToolCall{
		{ID: "call_c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
		{ID: "call_c2", Name: "read_file", Arguments: json.RawMessage(`{"path":"b.txt"}`)},
	}

	summary := agent.executeAll(ctx, session, calls, ToolExecutionOptions{TaskID: "task-cancel"})
	if summary.Outcome != tools.ToolOutcomeCancelled {
		t.Fatalf("expected ToolOutcomeCancelled, got %v", summary.Outcome)
	}
	if len(summary.Results) != 2 {
		t.Fatalf("expected 2 synthetic results, got %d", len(summary.Results))
	}
	for i, res := range summary.Results {
		if res.CallID != calls[i].ID {
			t.Fatalf("result %d ID mismatch: expected %s, got %s", i, calls[i].ID, res.CallID)
		}
		if !strings.Contains(res.Output, "aborted before dispatch") {
			t.Fatalf("result %d missing aborted message: %s", i, res.Output)
		}
	}
}

func TestToolExecutor_CrashRepairDistinguishesNotStartedFromUnknown(t *testing.T) {
	events := []SessionEvent{
		{Seq: 1, Type: EventTurnStart},
		{Seq: 2, Type: EventStepStart},
		{
			Seq:  3,
			Type: EventAssistantMessage,
			Message: models.Message{
				Role: models.RoleAssistant,
				ToolCalls: []models.ToolCall{
					{ID: "call_dispatched", Name: "write_file"},
					{ID: "call_unstarted", Name: "execute_command"},
				},
			},
		},
		{
			Seq:  4,
			Type: EventToolCall,
			Data: map[string]any{"call_id": "call_dispatched"},
		},
		// Crash happened here: no EventToolResult for either call, step & turn left open
	}

	repaired := repairSessionTail(events)
	if len(repaired) < 2 {
		t.Fatalf("expected at least 2 repaired events, got %d", len(repaired))
	}

	// First repair must be for call_dispatched with UNKNOWN
	rep1 := repaired[0]
	if rep1.Type != EventToolResult || rep1.Message.ToolCallID != "call_dispatched" {
		t.Fatalf("rep1 unexpected: %#v", rep1)
	}
	if !strings.Contains(rep1.Message.Content, "unknown outcome") {
		t.Fatalf("rep1 expected unknown outcome message, got: %s", rep1.Message.Content)
	}

	// Second repair must be for call_unstarted with NOT_STARTED
	rep2 := repaired[1]
	if rep2.Type != EventToolResult || rep2.Message.ToolCallID != "call_unstarted" {
		t.Fatalf("rep2 unexpected: %#v", rep2)
	}
	if !strings.Contains(rep2.Message.Content, "not started") {
		t.Fatalf("rep2 expected not started message, got: %s", rep2.Message.Content)
	}
}

func TestToolExecutor_ApprovalRejectionPreservesID(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })

	registry := tools.NewRegistry()
	_ = registry.Register(&filesystem.WriteFile{})

	manager := tools.NewManager(registry)
	agent := &Agent{workspace: root, toolManager: manager, transcript: newDurableMemory(store)}
	session := &Session{ID: "sess-approval", Name: "Approval"}
	_ = session.Save(root)

	// Set approval mode strict
	ctx := tools.WithApprovalMode(tools.WithWorkspace(context.Background(), root), tools.ApprovalStrict)
	ctx = tools.WithLifecycleRecorder(ctx, &stateRecorder{store: store, root: root, sessionID: session.ID})

	call := models.ToolCall{ID: "call_appr_1", Name: "write_file", Arguments: json.RawMessage(`{"path":"test.txt","content":"data"}`)}

	// Spawn goroutine to deny approval when prompted
	go func() {
		for {
			if manager.Deny("User rejected file edit") {
				return
			}
		}
	}()

	summary := agent.executeAll(ctx, session, []models.ToolCall{call}, ToolExecutionOptions{TaskID: "task-approval"})
	if summary.Outcome != tools.ToolOutcomePermissionBlocked {
		t.Fatalf("expected ToolOutcomePermissionBlocked on denial, got %v", summary.Outcome)
	}
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	if summary.Results[0].CallID != "call_appr_1" {
		t.Fatalf("expected CallID call_appr_1, got %s", summary.Results[0].CallID)
	}
	if summary.Results[0].Success {
		t.Fatal("expected failure on denial")
	}
}

func TestToolExecutor_InvalidArgumentsProduceRecoverableResult(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })

	registry := tools.NewRegistry()
	_ = registry.Register(&filesystem.ReadFile{})

	agent := &Agent{workspace: root, toolManager: tools.NewManager(registry), transcript: newDurableMemory(store)}
	session := &Session{ID: "sess-inv-args", Name: "Invalid Args"}
	_ = session.Save(root)

	ctx := tools.WithLifecycleRecorder(tools.WithWorkspace(context.Background(), root), &stateRecorder{store: store, root: root, sessionID: session.ID})
	call := models.ToolCall{ID: "call_bad_args", Name: "read_file", Arguments: json.RawMessage(`{"invalid_json`)}

	summary := agent.executeAll(ctx, session, []models.ToolCall{call}, ToolExecutionOptions{TaskID: "task-inv-args"})
	if len(summary.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(summary.Results))
	}
	res := summary.Results[0]
	if res.CallID != "call_bad_args" {
		t.Fatalf("expected CallID call_bad_args, got %s", res.CallID)
	}
	if res.Success {
		t.Fatal("expected failure on invalid args")
	}
	if res.Outcome != tools.ToolOutcomeRecoverable {
		t.Fatalf("expected ToolOutcomeRecoverable, got %v", res.Outcome)
	}
}
