package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/capabilities/repeat"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type countingExec struct {
	name string
	n    int
}

func (t *countingExec) Name() string                      { return t.name }
func (t *countingExec) Description() string               { return t.name }
func (t *countingExec) Schema() any                       { return map[string]any{"type": "object"} }
func (t *countingExec) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }
func (t *countingExec) Execute(context.Context, any) (*tools.ToolResult, error) {
	t.n++
	return &tools.ToolResult{Success: true, Status: tools.ToolStatusCompleted, Message: "ran", Data: map[string]any{"n": t.n}}, nil
}

type beforeStub struct {
	name string
	log  *[]string
	hit  *tools.ToolResult
}

func (s beforeStub) BeforeTool(runtime.BeforeToolEvent) (runtime.BeforeToolDecision, error) {
	*s.log = append(*s.log, s.name)
	if s.hit != nil {
		return runtime.BeforeToolDecision{Result: s.hit, Reused: true}, nil
	}
	return runtime.BeforeToolDecision{}, nil
}

type afterStub struct {
	name string
	log  *[]string
	msg  string
	err  error
}

func (s afterStub) AfterTool(runtime.AfterToolEvent) (runtime.AfterToolDecision, error) {
	if s.log != nil {
		*s.log = append(*s.log, s.name)
	}
	if s.err != nil {
		return runtime.AfterToolDecision{}, s.err
	}
	if s.msg == "" {
		return runtime.AfterToolDecision{}, nil
	}
	return runtime.AfterToolDecision{NextStep: []models.Message{{Role: models.RoleUser, Content: s.msg}}}, nil
}

func compositionSession(t *testing.T) (string, *Session, *state.Store) {
	t.Helper()
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	session := &Session{ID: "hooks", Name: "Hooks"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}
	return root, session, store
}

func TestEmptyHookSetExecutesTools(t *testing.T) {
	root, session, store := compositionSession(t)
	tool := &countingExec{name: "probe"}
	reg := tools.NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	agent := NewAgent(nil, tools.NewManager(reg), nil, newDurableMemory(store), runtime.NewHookSet())
	agent.workspace = root
	summary := agent.executeAll(context.Background(), session, []models.ToolCall{
		{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)},
	}, ToolExecutionOptions{})
	if summary.Err != nil || tool.n != 1 || !summary.Results[0].Success {
		t.Fatalf("empty hooks must still execute: %#v n=%d err=%v", summary, tool.n, summary.Err)
	}
}

func TestRepeatOmittedProducesNoReminder(t *testing.T) {
	root, session, store := compositionSession(t)
	tool := &countingExec{name: "read_file"}
	reg := tools.NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	agent := NewAgent(nil, tools.NewManager(reg), nil, newDurableMemory(store), runtime.NewHookSet())
	agent.workspace = root
	calls := []models.ToolCall{
		{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		{ID: "c2", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		{ID: "c3", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
	}
	summary := agent.executeAll(context.Background(), session, calls, ToolExecutionOptions{})
	if summary.Err != nil || tool.n != 3 {
		t.Fatalf("expected 3 physical calls: n=%d err=%v", tool.n, summary.Err)
	}
	if agent.inbox.HasNextStep() {
		t.Fatalf("repeat omitted must not stage a reminder: %#v", agent.inbox.ClaimNextStep())
	}
}

func TestRepeatMountedProducesReminder(t *testing.T) {
	root, session, store := compositionSession(t)
	tool := &countingExec{name: "read_file"}
	reg := tools.NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	hooks, _ := repeatHooks(repeat.Config{Thresholds: []int{3}})
	agent := NewAgent(nil, tools.NewManager(reg), nil, newDurableMemory(store), hooks)
	agent.workspace = root
	calls := []models.ToolCall{
		{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		{ID: "c2", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		{ID: "c3", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
	}
	summary := agent.executeAll(context.Background(), session, calls, ToolExecutionOptions{})
	if summary.Err != nil {
		t.Fatal(summary.Err)
	}
	if !agent.inbox.HasNextStep() {
		t.Fatal("mounted repeat must stage a reminder")
	}
	staged := agent.inbox.ClaimNextStep()
	if len(staged) != 1 || !strings.Contains(staged[0].Content, "You are repeating the exact same tool call") {
		t.Fatalf("reminder %#v", staged)
	}
}

func TestBeforeToolShortCircuitSkipsPhysicalTool(t *testing.T) {
	root, session, store := compositionSession(t)
	tool := &countingExec{name: "probe"}
	reg := tools.NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	var order []string
	hooks := runtime.NewHookSet()
	hooks.AddBeforeTool(beforeStub{name: "a", log: &order})
	hooks.AddBeforeTool(beforeStub{name: "b", log: &order, hit: &tools.ToolResult{Success: true, Status: tools.ToolStatusCompleted, Message: "reused"}})
	hooks.AddBeforeTool(beforeStub{name: "c", log: &order})
	agent := NewAgent(nil, tools.NewManager(reg), nil, newDurableMemory(store), hooks)
	agent.workspace = root
	summary := agent.executeAll(context.Background(), session, []models.ToolCall{
		{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)},
	}, ToolExecutionOptions{})
	if summary.Err != nil {
		t.Fatal(summary.Err)
	}
	if tool.n != 0 {
		t.Fatalf("physical tool ran %d times", tool.n)
	}
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("order %v", order)
	}
	if !summary.Results[0].Reused || summary.Results[0].Output == "" {
		t.Fatalf("expected reused result %#v", summary.Results[0])
	}
}

func TestAfterToolErrorPersistsResultAndStopsBatch(t *testing.T) {
	root, session, store := compositionSession(t)
	tool := &countingExec{name: "probe"}
	reg := tools.NewRegistry()
	if err := reg.Register(tool); err != nil {
		t.Fatal(err)
	}
	hooks := runtime.NewHookSet()
	hooks.AddAfterTool(afterStub{err: errors.New("after boom")})
	agent := NewAgent(nil, tools.NewManager(reg), nil, newDurableMemory(store), hooks)
	agent.workspace = root
	summary := agent.executeAll(context.Background(), session, []models.ToolCall{
		{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)},
		{ID: "c2", Name: "probe", Arguments: json.RawMessage(`{}`)},
	}, ToolExecutionOptions{})
	if summary.Err == nil || !strings.Contains(summary.Err.Error(), "after boom") {
		t.Fatalf("expected fatal after-tool error, got %v", summary.Err)
	}
	if tool.n != 1 {
		t.Fatalf("second call must not run, n=%d", tool.n)
	}
	if summary.Outcome != tools.ToolOutcomeFatal {
		t.Fatalf("outcome %v", summary.Outcome)
	}
	msgs := session.DeriveMessages()
	found := false
	for _, msg := range msgs {
		if msg.Role == models.RoleTool && msg.ToolCallID == "c1" && strings.Contains(msg.Content, "ran") {
			found = true
		}
		if msg.ToolCallID == "c1" && msg.Role == models.RoleTool && msg.Content == "" {
			t.Fatal("dangling empty tool result")
		}
	}
	if !found {
		t.Fatalf("tool/result for c1 missing: %#v", msgs)
	}
	if len(summary.Results) != 2 || summary.Results[1].CallID != "c2" {
		t.Fatalf("remaining batch must be skipped with a result: %#v", summary.Results)
	}
}
