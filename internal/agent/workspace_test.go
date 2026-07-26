package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeWorkspacePreservesExistingMemory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".memory"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".memory", "MEMORY.md")
	if err := os.WriteFile(path, []byte("# Custom\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InitializeWorkspace(root); err != nil {
		t.Fatal(err)
	}
	memory, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(memory), "# Custom") || !strings.Contains(string(memory), "example.com/project") {
		t.Fatalf("unexpected memory: %q, %v", memory, err)
	}
	if output, err := InitializeWorkspace(root); err != nil || !strings.Contains(output, "already") {
		t.Fatalf("second initialization was not a no-op: %q, %v", output, err)
	}
}
