package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/state"
)

func TestInitializeWorkspacePersistsStateMemory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeWorkspace(root); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	memory, err := store.WorkspaceMemory(context.Background())
	if err != nil || !strings.Contains(memory, "# Codebase Memory") || !strings.Contains(memory, "example.com/project") {
		t.Fatalf("unexpected memory: %q, %v", memory, err)
	}
	if output, err := InitializeWorkspace(root); err != nil || !strings.Contains(output, "already") {
		t.Fatalf("second initialization was not a no-op: %q, %v", output, err)
	}
}
