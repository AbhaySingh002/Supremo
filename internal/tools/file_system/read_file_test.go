package file_system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestReadFileRejectsOversizedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.txt")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, tools.MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	result, err := (&ReadFile{}).Execute(tools.WithWorkspace(context.Background(), root), map[string]any{"path": "large.txt"})
	if err != nil || result.Success || !strings.Contains(result.Message, "1 MiB") {
		t.Fatalf("oversized read: result=%#v err=%v", result, err)
	}
}
