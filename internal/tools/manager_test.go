package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type managerTestTool struct {
	name         string
	capabilities CapabilitySet
	schema       any
	calls        int
	input        any
	result       *ToolResult
	panicValue   any
}

type fileWritingManagerTool struct{ root string }

func (t fileWritingManagerTool) Name() string        { return "write_file" }
func (t fileWritingManagerTool) Description() string { return "write file" }
func (t fileWritingManagerTool) Schema() any         { return map[string]any{} }
func (t fileWritingManagerTool) Capabilities() CapabilitySet {
	return CapabilityWriteWorkspace
}
func (t fileWritingManagerTool) Execute(_ context.Context, input any) (*ToolResult, error) {
	content, _ := input.(map[string]any)["content"].(string)
	return BuildToolResult(os.WriteFile(filepath.Join(t.root, "notes.txt"), []byte(content), 0600) == nil, "done", nil), nil
}

func (t *managerTestTool) Name() string        { return t.name }
func (t *managerTestTool) Description() string { return t.name }
func (t *managerTestTool) Capabilities() CapabilitySet {
	if t.capabilities != 0 {
		return t.capabilities
	}
	switch t.name {
	case "read_file":
		return CapabilityReadWorkspace
	case "git_status", "git_diff":
		return CapabilityReadWorkspace | CapabilityExecuteProcess
	case "web_fetch":
		return CapabilityUseNetwork
	default:
		return CapabilityWriteWorkspace
	}
}
func (t *managerTestTool) Schema() any {
	if t.schema != nil {
		return t.schema
	}
	return map[string]any{}
}
func (t *managerTestTool) Execute(_ context.Context, input any) (*ToolResult, error) {
	t.calls++
	t.input = input
	if t.panicValue != nil {
		panic(t.panicValue)
	}
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
	if _, err := manager.Execute(WithApprovalMode(context.Background(), ApprovalSuperman), "read_file", map[string]any{"path": "README.md"}); err != nil {
		t.Fatal(err)
	}
	completed := events[len(events)-1]
	if completed.Status != "completed" || !strings.Contains(completed.Arguments, "README.md") || !strings.Contains(completed.Output, "output truncated") || len(completed.Output) > 12_100 {
		t.Fatalf("unexpected bounded completion event: %#v", completed)
	}
}

func TestManagerReportsProgressScope(t *testing.T) {
	read := &managerTestTool{name: "read_file"}
	registry := NewRegistry()
	if err := registry.Register(read); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	var events []Event
	manager.SetReporter(func(event Event) { events = append(events, event) })
	ctx := WithProgressScope(WithApprovalMode(context.Background(), ApprovalSuperman), ProgressScope{SessionID: "session-1", TaskID: "inspect-ui"})
	if _, err := manager.Execute(ctx, "read_file", map[string]any{"path": "ui/view.go"}); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("expected tool progress")
	}
	for _, event := range events {
		if event.SessionID != "session-1" || event.TaskID != "inspect-ui" {
			t.Fatalf("event lost scope: %#v", event)
		}
	}
}

func TestManagerRejectsUnactivatedToolBeforeExecution(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	ctx := WithActiveTools(WithApprovalMode(context.Background(), ApprovalSuperman), []string{"read_file"})
	result, err := manager.Execute(ctx, "write_file", map[string]any{"path": "x"})
	if err != nil || result == nil || result.Status != ToolStatusDenied || write.calls != 0 {
		t.Fatalf("unactivated result=%#v err=%v calls=%d", result, err, write.calls)
	}
}

func TestManagerNormalizesLargeOutputWithArtifactReference(t *testing.T) {
	read := &managerTestTool{name: "read_file", result: BuildToolResult(true, "done", map[string]any{"content": strings.Repeat("x", 20_000)})}
	registry := NewRegistry()
	if err := registry.Register(read); err != nil {
		t.Fatal(err)
	}
	result, err := NewManager(registry).Execute(WithApprovalMode(context.Background(), ApprovalSuperman), "read_file", map[string]any{"path": "README.md"})
	if err != nil || result == nil || result.Status != ToolStatusCompleted || result.ArtifactID == "" || result.Data != nil || len(result.Preview) > toolPreviewBytes {
		t.Fatalf("normalized result=%#v err=%v", result, err)
	}
}

func TestManagerRejectsBlankRequiredInputBeforeExecution(t *testing.T) {
	read := &managerTestTool{name: "read_file", schema: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"},
	}}
	registry := NewRegistry()
	if err := registry.Register(read); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	var events []Event
	manager.SetReporter(func(event Event) { events = append(events, event) })
	ctx := WithApprovalMode(context.Background(), ApprovalSuperman)

	result, err := manager.Execute(ctx, "read_file", map[string]any{"path": "  "})
	if err != nil || result == nil || result.Success || read.calls != 0 {
		t.Fatalf("blank required input ran: result=%#v err=%v calls=%d", result, err, read.calls)
	}
	if len(events) != 1 || events[0].Status != "failed" || strings.Contains(events[0].Status, "running") {
		t.Fatalf("invalid input should emit one failed event: %#v", events)
	}
}

func TestManagerReportsVisibleTextFileDiff(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.Register(fileWritingManagerTool{root: root}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	var completed Event
	manager.SetReporter(func(event Event) {
		if event.Status == "completed" {
			completed = event
		}
	})
	ctx := WithWorkspace(WithApprovalMode(context.Background(), ApprovalSuperman), root)
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "notes.txt", "content": "after\n"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--- a/notes.txt", "+++ b/notes.txt", "-before", "+after"} {
		if !strings.Contains(completed.Diff, want) {
			t.Fatalf("completion event diff missing %q: %#v", want, completed)
		}
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

func TestManagerRejectsMutationInReadOnlySubagent(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	_, err := NewManager(registry).Execute(WithReadOnly(context.Background()), "write_file", map[string]any{"path": "x"})
	if err == nil || !strings.Contains(err.Error(), "not allowed in a read-only execution context") || write.calls != 0 {
		t.Fatalf("read-only mutation was not rejected before execution: err=%v calls=%d", err, write.calls)
	}
}

func TestManagerRejectsWorkingTreeGitInspectionAndNetworkInReadOnlySubagent(t *testing.T) {
	for _, name := range []string{"git_status", "git_diff", "web_fetch"} {
		t.Run(name, func(t *testing.T) {
			tool := &managerTestTool{name: name}
			registry := NewRegistry()
			if err := registry.Register(tool); err != nil {
				t.Fatal(err)
			}
			_, err := NewManager(registry).Execute(WithReadOnly(context.Background()), name, map[string]any{"directory": "."})
			if err == nil || !strings.Contains(err.Error(), "not allowed in a read-only execution context") || tool.calls != 0 {
				t.Fatalf("read-only Git inspection ran: err=%v calls=%d", err, tool.calls)
			}
		})
	}
}

func TestManagerReadOnlyUsesCapabilitiesInsteadOfToolNames(t *testing.T) {
	read := &managerTestTool{name: "renamed_reader", capabilities: CapabilityReadWorkspace, result: BuildToolResult(true, "read", nil)}
	write := &managerTestTool{name: "read_file", capabilities: CapabilityWriteWorkspace}

	registry := NewRegistry()
	if err := registry.Register(read); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	ctx := WithApprovalMode(WithReadOnly(context.Background()), ApprovalSuperman)
	if _, err := manager.Execute(ctx, "renamed_reader", map[string]any{}); err != nil || read.calls != 1 {
		t.Fatalf("renamed reader should be permitted by its capability: err=%v calls=%d", err, read.calls)
	}
	if _, err := manager.Execute(ctx, "read_file", map[string]any{}); err == nil {
		t.Fatal("write capability should be rejected despite a read-looking name")
	}
}

func TestManagerPlanResearchAllowsOnlyLocalInspection(t *testing.T) {
	registry := NewRegistry()
	registered := map[string]*managerTestTool{}
	for name, caps := range map[string]CapabilitySet{
		"read_file":       CapabilityReadWorkspace,
		"write_file":      CapabilityWriteWorkspace,
		"execute_command": CapabilityReadWorkspace | CapabilityExecuteProcess,
		"git_status":      CapabilityReadWorkspace | CapabilityExecuteProcess,
		"web_fetch":       CapabilityReadWorkspace | CapabilityUseNetwork,
	} {
		tool := &managerTestTool{name: name, capabilities: caps}
		registered[name] = tool
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewManager(registry)
	ctx := WithActiveTools(WithResearchOnly(WithApprovalMode(context.Background(), ApprovalSuperman)), []string{"read_file", "write_file", "execute_command", "git_status", "web_fetch"})
	if _, err := manager.Execute(ctx, "read_file", map[string]any{"path": "README.md"}); err != nil || registered["read_file"].calls != 1 {
		t.Fatalf("local read was not allowed: err=%v calls=%d", err, registered["read_file"].calls)
	}
	for _, name := range []string{"write_file", "execute_command", "git_status", "web_fetch"} {
		if _, err := manager.Execute(ctx, name, map[string]any{}); err == nil || !strings.Contains(err.Error(), "not allowed during local plan research") || registered[name].calls != 0 {
			t.Fatalf("plan research ran %s: err=%v calls=%d", name, err, registered[name].calls)
		}
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

func TestManagerRevalidatesEditedApprovalInput(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(event Event) {
		if event.Status == "waiting approval" {
			waiting <- struct{}{}
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), "write_file", map[string]any{"path": "old.txt"})
		done <- err
	}()
	<-waiting
	if !manager.ApproveWithInput(map[string]any{"path": "new.txt"}) {
		t.Fatal("edited approval was not accepted")
	}
	if err := <-done; err != nil || write.calls != 1 || inputValue(write.input, "path") != "new.txt" {
		t.Fatalf("edited approval result: err=%v calls=%d input=%#v", err, write.calls, write.input)
	}
}

func TestManagerRejectsInvalidEditedApprovalInput(t *testing.T) {
	write := &managerTestTool{name: "write_file", schema: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}}, "required": []string{"path", "content"},
	}}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(event Event) {
		if event.Status == "waiting approval" {
			waiting <- struct{}{}
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := manager.Execute(context.Background(), "write_file", map[string]any{"path": "notes.txt", "content": "original"})
		done <- err
	}()
	<-waiting
	if !manager.ApproveWithInput(map[string]any{"path": 7}) {
		t.Fatal("edited approval was not accepted")
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "revised tool arguments") || write.calls != 0 {
		t.Fatalf("invalid edited input ran: err=%v calls=%d", err, write.calls)
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

func TestManagerCodeExecutionPolicy(t *testing.T) {
	registry := NewRegistry()
	inputs := map[string]any{
		"execute_command": map[string]any{"command": "go", "args": []string{"test", "./..."}},
	}
	registered := make(map[string]*managerTestTool, len(inputs))
	for name := range inputs {
		registered[name] = &managerTestTool{name: name}
		meta := ToolMetadata{CanonicalName: name, Family: "shell", Access: ToolAccessDestructive, SideEffect: ToolSideEffectProcess, RequiresApproval: true}
		if err := registry.Register(registered[name], meta); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewManager(registry)
	for name, input := range inputs {
		result, err := manager.Execute(WithDryRun(context.Background(), true), name, input)
		if err != nil || result == nil || !result.Success || registered[name].calls != 0 {
			t.Fatalf("%s dry run executed code: result=%#v err=%v calls=%d", name, result, err, registered[name].calls)
		}
		awaitApproval(t, manager, WithApprovalMode(context.Background(), ApprovalBatman), name, input)
	}
	for _, subcommand := range []string{"build", "test", "vet", "fmt"} {
		awaitApproval(t, manager, WithApprovalMode(context.Background(), ApprovalBatman), "execute_command", map[string]any{"command": "go", "args": []string{subcommand, "./..."}})
	}
}

func TestManagerRejectsUnknownArguments(t *testing.T) {
	tool := &managerTestTool{name: "read_file", schema: map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
		"required":   []string{"path"},
	}}
	registry := NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	result, err := NewManager(registry).Execute(context.Background(), "read_file", map[string]any{"path": "README.md", "typo": true})
	if err != nil || result == nil || result.Success || tool.calls != 0 || !strings.Contains(result.Message, "unknown field") {
		t.Fatalf("unknown argument result=%#v err=%v calls=%d", result, err, tool.calls)
	}
}

func TestManagerRecoversToolPanic(t *testing.T) {
	panicking := &managerTestTool{name: "read_file", panicValue: "boom"}
	registry := NewRegistry()
	if err := registry.Register(panicking); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	var events []Event
	manager.SetReporter(func(event Event) { events = append(events, event) })
	result, err := manager.Execute(WithApprovalMode(context.Background(), ApprovalSuperman), "read_file", nil)
	if result != nil || err == nil || !strings.Contains(err.Error(), `tool "read_file" panicked: boom`) {
		t.Fatalf("panic result=%#v err=%v", result, err)
	}
	if len(events) != 2 || events[0].Status != "running" || events[1].Status != "failed" || !strings.Contains(events[1].Message, "panicked") {
		t.Fatalf("panic was not reported as a tool failure: %#v", events)
	}
}

func TestWorkspaceAllowsUnlimitedToolCalls(t *testing.T) {
	root := t.TempDir()
	ctx := WithApprovalMode(WithWorkspace(context.Background(), root), ApprovalSuperman)
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
	for range 24 {
		if _, err := manager.Execute(ctx, "read_file", nil); err != nil {
			t.Fatal(err)
		}
	}
	if read.calls != 24 {
		t.Fatalf("tool calls = %d, want 24", read.calls)
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
	if err := <-done; !errors.Is(err, context.Canceled) || errorClass(err) != ErrorClassPermission || write.calls != 0 {
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
	if _, err := manager.Execute(WithApprovalMode(context.Background(), ApprovalSuperman), "read_file", nil); err != nil {
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

	if _, err := manager.Execute(WithApprovalMode(context.Background(), ApprovalStrict), "read_file", nil); err != nil {
		t.Fatalf("strict mode should run read-only work internally: %v", err)
	}
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

func TestApprovalModeCanPromoteAnActiveTask(t *testing.T) {
	write := &managerTestTool{name: "write_file"}
	registry := NewRegistry()
	if err := registry.Register(write); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(registry)
	ctx := WithApprovalMode(context.Background(), ApprovalStrict)
	waiting := make(chan struct{}, 1)
	manager.SetReporter(func(event Event) {
		if event.Status == "waiting approval" {
			waiting <- struct{}{}
		}
	})
	first := make(chan error, 1)
	go func() {
		_, err := manager.Execute(ctx, "write_file", map[string]any{"path": "first.txt"})
		first <- err
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("strict task did not wait for its first mutation")
	}
	if !SetApprovalMode(ctx, ApprovalSuperman) || !manager.Approve() {
		t.Fatal("could not promote and approve the active task")
	}
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "second.txt"}); err != nil || write.calls != 2 {
		t.Fatalf("promoted task still asked before later mutations: err=%v calls=%d", err, write.calls)
	}
}

func TestDetachedApprovalModeDoesNotChangeParent(t *testing.T) {
	parent := WithApprovalMode(context.Background(), ApprovalStrict)
	child := WithDetachedApprovalMode(parent, ApprovalSuperman)

	if got := ApprovalModeFromContext(child); got != ApprovalSuperman {
		t.Fatalf("child approval mode = %q, want %q", got, ApprovalSuperman)
	}
	if got := ApprovalModeFromContext(parent); got != ApprovalStrict {
		t.Fatalf("detached child changed parent approval mode: got %q, want %q", got, ApprovalStrict)
	}
	if !RequiresApprovalFor(parent, ToolDescriptor{Name: "write_file", RequiresApproval: true}, map[string]any{"path": "notes.txt"}) {
		t.Fatal("strict parent no longer requires approval")
	}
}

func TestApprovalPolicyLabelsMatchExecutionModes(t *testing.T) {
	for _, test := range []struct {
		mode ApprovalMode
		desc ToolDescriptor
		want string
	}{
		{ApprovalStrict, ToolDescriptor{Name: "read_file"}, "runs automatically"},
		{ApprovalBatman, ToolDescriptor{Name: "read_file"}, "runs automatically"},
		{ApprovalBatman, ToolDescriptor{Name: "write_file", RequiresApproval: true, BatmanManifest: true}, "dependency files ask"},
		{ApprovalBatman, ToolDescriptor{Name: "delete_file", RequiresApproval: true}, "approval required"},
		{ApprovalSuperman, ToolDescriptor{Name: "delete_file", RequiresApproval: true}, "runs automatically"},
	} {
		if got := ApprovalPolicyLabel(test.mode, test.desc); got != test.want {
			t.Errorf("%s %s = %q, want %q", test.mode, test.desc.Name, got, test.want)
		}
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
