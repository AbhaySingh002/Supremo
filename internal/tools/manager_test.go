package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

type managerTestTool struct {
	name  string
	calls int
}

func (t *managerTestTool) Name() string        { return t.name }
func (t *managerTestTool) Description() string { return t.name }
func (t *managerTestTool) Schema() any         { return map[string]any{} }
func (t *managerTestTool) Execute(context.Context, any) (*ToolResult, error) {
	t.calls++
	return BuildToolResult(true, "done", nil), nil
}

func TestManagerMutationNeedsApproval(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(string) { waiting <- struct{}{} })
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
	if len(activity) != 3 || activity[0].Status != "waiting approval" || activity[1].Status != "approved" || activity[2].Status != "completed" {
		t.Fatalf("unexpected approval activity: %#v", activity)
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
	manager.SetReporter(func(string) { waiting <- struct{}{} })
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
	manager.SetReporter(func(string) { waiting <- struct{}{} })
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
	if len(activity) != 1 || activity[0].Tool != "read_file" || activity[0].Status != "completed" {
		t.Fatalf("unexpected activity: %#v", activity)
	}
}
