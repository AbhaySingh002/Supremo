package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

type managerTestTool struct {
	name   string
	calls  int
	result *ToolResult
}

func (t *managerTestTool) Name() string        { return t.name }
func (t *managerTestTool) Description() string { return t.name }
func (t *managerTestTool) Schema() any         { return map[string]any{} }
func (t *managerTestTool) Execute(context.Context, any) (*ToolResult, error) {
	t.calls++
	if t.result != nil {
		return t.result, nil
	}
	return BuildToolResult(true, "done", nil), nil
}

func TestManagerReportsBoundedToolOutput(t *testing.T) {
	read := &managerTestTool{name: "read_file", result: BuildToolResult(true, "done", map[string]interface{}{"content": strings.Repeat("x", 13_000)})}
	registry := NewRegistry()
	if err := registry.Register(read); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	var events []Event
	manager.SetReporter(func(event Event) { events = append(events, event) })
	if _, err := manager.Execute(context.Background(), "read_file", map[string]any{"path": "README.md"}); err != nil {
		t.Fatal(err)
	}
	completed := events[len(events)-1]
	if completed.Status != "completed" || !strings.Contains(completed.Arguments, "README.md") || !strings.Contains(completed.Output, "output truncated") || len(completed.Output) > 12_100 {
		t.Fatalf("unexpected bounded completion event: %#v", completed)
	}
}

func TestManagerMutationNeedsApproval(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(Event) {
		select {
		case waiting <- struct{}{}:
		default:
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), "write_file", map[string]any{"path": "x"})
		result <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("tool did not wait for approval")
	}
	if write.calls != 0 || !manager.Approve() {
		t.Fatal("tool ran before approval")
	}
	if err := <-result; err != nil || write.calls != 1 {
		t.Fatalf("approved tool result: err=%v calls=%d", err, write.calls)
	}
	activity := manager.Recent()
	if len(activity) != 4 || activity[0].Status != "waiting approval" || activity[1].Status != "approved" || activity[2].Status != "running" || activity[3].Status != "completed" {
		t.Fatalf("unexpected approval activity: %#v", activity)
	}
}

func TestManagerReportsApprovalPreview(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	events := make(chan Event, 4)
	manager.SetReporter(func(event Event) { events <- event })
	done := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), "write_file", map[string]any{"path": "notes.md"})
		done <- err
	}()
	event := <-events
	if event.Status != "waiting approval" || event.Tool != "write_file" || !strings.Contains(event.Arguments, "notes.md") {
		t.Fatalf("unexpected approval event: %#v", event)
	}
	if !manager.Approve() {
		t.Fatal("expected approval request")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerDryRunAndDeny(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	result, err := manager.Execute(WithDryRun(context.Background(), true), "write_file", nil)
	if err != nil || !result.Success || write.calls != 0 {
		t.Fatalf("dry run executed a mutation: result=%#v err=%v calls=%d", result, err, write.calls)
	}
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(Event) {
		select {
		case waiting <- struct{}{}:
		default:
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), "write_file", nil)
		done <- err
	}()
	<-waiting
	if !manager.Deny("not now") {
		t.Fatal("expected pending approval")
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "not now") || write.calls != 0 {
		t.Fatalf("denied tool result: err=%v calls=%d", err, write.calls)
	}
}

func TestWorkspaceAndBudget(t *testing.T) {
	root := t.TempDir()
	ctx := WithWorkspace(context.Background(), root)
	if _, err := ValidateAndResolvePath(ctx, "../outside"); err == nil {
		t.Fatal("expected traversal to be blocked")
	}
	if _, err := ValidateAndResolvePath(ctx, t.TempDir()); err == nil {
		t.Fatal("expected outside absolute path to be blocked")
	}
	if _, err := ValidateAndResolvePath(ctx, root); err != nil {
		t.Fatalf("expected workspace path: %v", err)
	}
	read := &managerTestTool{name: "read_file"}
	registry := NewRegistry()
	if err := registry.Register(read); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	ctx = WithToolBudget(ctx, 1)
	if _, err := manager.Execute(ctx, "read_file", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(ctx, "read_file", nil); err == nil {
		t.Fatal("expected tool budget to stop the second call")
	}
}

func TestManagerCancellationReleasesApproval(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(Event) {
		select {
		case waiting <- struct{}{}:
		default:
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.Execute(ctx, "write_file", nil)
		done <- err
	}()
	<-waiting
	cancel()
	if err := <-done; err != context.Canceled || write.calls != 0 {
		t.Fatalf("canceled tool result: err=%v calls=%d", err, write.calls)
	}
}

func TestManagerRecordsRecentActivity(t *testing.T) {
	read := &managerTestTool{name: "read_file"}
	registry := NewRegistry()
	if err := registry.Register(read); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	if _, err := manager.Execute(context.Background(), "read_file", nil); err != nil {
		t.Fatal(err)
	}
	activity := manager.Recent()
	if len(activity) != 2 || activity[0].Tool != "read_file" || activity[0].Status != "running" || activity[1].Status != "completed" {
		t.Fatalf("unexpected activity: %#v", activity)
	}
}

func TestManagerApprovalModes(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"read_file", "write_file", "delete_file", "execute_command"} {
		if err := registry.Register(&managerTestTool{name: name}); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewManager(registry)

	awaitApproval(t, manager, WithApprovalMode(context.Background(), ApprovalStrict), "read_file", nil)
	if _, err := manager.Execute(WithApprovalMode(context.Background(), ApprovalBatman), "read_file", nil); err != nil {
		t.Fatalf("batman should run read-only work: %v", err)
	}
	awaitApproval(t, manager, WithApprovalMode(context.Background(), ApprovalBatman), "delete_file", map[string]any{"path": "old.go"})
	awaitApproval(t, manager, WithApprovalMode(context.Background(), ApprovalBatman), "write_file", map[string]any{"path": "go.mod"})
	awaitApproval(t, manager, WithApprovalMode(context.Background(), ApprovalBatman), "execute_command", map[string]any{"command": "go", "args": []string{"get", "example.com/module"}})
	awaitApproval(t, manager, WithApprovalMode(context.Background(), ApprovalBatman), "execute_command", map[string]any{"command": "unknown-tool"})
	if _, err := manager.Execute(WithApprovalMode(context.Background(), ApprovalSuperman), "delete_file", map[string]any{"path": "old.go"}); err != nil {
		t.Fatalf("superman should auto-approve: %v", err)
	}
}

func awaitApproval(t *testing.T, manager *Manager, ctx context.Context, name string, input any) {
	t.Helper()
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(event Event) {
		if event.Status == "waiting approval" {
			waiting <- struct{}{}
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := manager.Execute(ctx, name, input)
		done <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatalf("%s did not request approval", name)
	}
	if !manager.Approve() {
		t.Fatalf("%s had no pending approval", name)
	}
	if err := <-done; err != nil {
		t.Fatalf("%s approval result: %v", name, err)
	}
}
