package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	models "github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/filesystem"
)

func newTestAgentWithFSTools(t *testing.T, workspace string) *Agent {
	t.Helper()
	reg := tools.NewRegistry()
	_ = reg.Register(&filesystem.ReadFile{})
	_ = reg.Register(&filesystem.WriteFile{})
	_ = reg.Register(&filesystem.ReplaceInFile{})
	_ = reg.Register(&filesystem.DeleteFile{})
	_ = reg.Register(&filesystem.RenameFile{})

	mgr := tools.NewManager(reg)
	return &Agent{
		workspace:   workspace,
		toolManager: mgr,
	}
}

func testSafetyCtx(root string) context.Context {
	return tools.WithApprovalMode(tools.WithWorkspace(context.Background(), root), tools.ApprovalSuperman)
}

func TestAgentReadThenEditWorkflow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "service.go")
	if err := os.WriteFile(path, []byte("func OldService() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	readCallArgs, _ := json.Marshal(map[string]any{"path": "service.go"})
	editCallArgs, _ := json.Marshal(map[string]any{
		"path":       "service.go",
		"old_string": "func OldService() {}",
		"new_string": "func NewService() {}",
	})

	agent := newTestAgentWithFSTools(t, root)
	session := &Session{ID: "test-read-edit-flow"}
	ctx := testSafetyCtx(root)

	// Execute read
	sum1 := agent.executeAll(ctx, session, []models.ToolCall{
		{ID: "call-read", Name: "read_file", Arguments: readCallArgs},
	}, ToolExecutionOptions{TaskID: "task-workflow"})
	if sum1.Outcome != tools.ToolOutcomeSuccess {
		t.Fatalf("read step failed: %#v", sum1)
	}

	// Execute edit
	sum2 := agent.executeAll(ctx, session, []models.ToolCall{
		{ID: "call-edit", Name: "replace_in_file", Arguments: editCallArgs},
	}, ToolExecutionOptions{TaskID: "task-workflow"})
	if sum2.Outcome != tools.ToolOutcomeSuccess {
		t.Fatalf("edit step failed: %#v", sum2)
	}

	// Verify disk modification
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "func NewService() {}") {
		t.Fatalf("disk not updated: %s", string(data))
	}
}

func TestAgentExternalMutationForcesReread(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	readCallArgs, _ := json.Marshal(map[string]any{"path": "state.txt"})
	editCallArgs, _ := json.Marshal(map[string]any{
		"path":       "state.txt",
		"old_string": "v1",
		"new_string": "v2",
	})
	edit2CallArgs, _ := json.Marshal(map[string]any{
		"path":       "state.txt",
		"old_string": "v1_external",
		"new_string": "v3",
	})

	agent := newTestAgentWithFSTools(t, root)
	session := &Session{ID: "test-external-mutation"}
	ctx := testSafetyCtx(root)

	// 1. Read
	sum1 := agent.executeAll(ctx, session, []models.ToolCall{
		{ID: "c1", Name: "read_file", Arguments: readCallArgs},
	}, ToolExecutionOptions{TaskID: "task-ext"})
	if sum1.Outcome != tools.ToolOutcomeSuccess {
		t.Fatalf("read failed: %#v", sum1)
	}

	// 2. External modification occurs behind agent's back
	if err := os.WriteFile(path, []byte("v1_external\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Edit fails with stale version error
	sum2 := agent.executeAll(ctx, session, []models.ToolCall{
		{ID: "c2", Name: "replace_in_file", Arguments: editCallArgs},
	}, ToolExecutionOptions{TaskID: "task-ext"})
	if len(sum2.Results) == 0 || sum2.Results[0].Success {
		t.Fatalf("stale edit succeeded unexpectedly: %#v", sum2)
	}
	if !strings.Contains(sum2.Results[0].Output, "changed since it was read") {
		t.Fatalf("unexpected message: %s", sum2.Results[0].Output)
	}

	// 4. Model re-reads fresh content
	sum3 := agent.executeAll(ctx, session, []models.ToolCall{
		{ID: "c3", Name: "read_file", Arguments: readCallArgs},
	}, ToolExecutionOptions{TaskID: "task-ext"})
	if sum3.Outcome != tools.ToolOutcomeSuccess {
		t.Fatalf("reread failed: %#v", sum3)
	}

	// 5. Model retries edit with new base -> succeeds
	sum4 := agent.executeAll(ctx, session, []models.ToolCall{
		{ID: "c4", Name: "replace_in_file", Arguments: edit2CallArgs},
	}, ToolExecutionOptions{TaskID: "task-ext"})
	if sum4.Outcome != tools.ToolOutcomeSuccess {
		t.Fatalf("retried edit failed: %#v", sum4)
	}

	// Disk has final content
	data, _ := os.ReadFile(path)
	if string(data) != "v3\n" {
		t.Fatalf("final content mismatch: %q", string(data))
	}
}
