package filesystem

import (
	"context"
	"fmt"
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
	if err != nil || result.Success || !result.Retryable || !strings.Contains(result.Message, "start_line") {
		t.Fatalf("oversized read: result=%#v err=%v", result, err)
	}
	if result.Data["truncated"] != true {
		t.Fatalf("expected truncated, got %#v", result.Data)
	}
}

func TestReadFileRangeAndHash(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line-%d\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithWorkspace(context.Background(), root)
	result, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "big.go", "start_line": 190, "end_line": 250})
	if err != nil || !result.Success {
		t.Fatalf("range read: %#v %v", result, err)
	}
	content, _ := result.Data["content"].(string)
	if !strings.Contains(content, "190 | line-190") || strings.Contains(content, "189 |") || strings.Contains(content, "251 |") {
		t.Fatalf("expected numbered 190-250 without whole file, got %q", content)
	}
	if _, ok := result.Data["hash"].(string); !ok || result.Data["hash"] == "" {
		t.Fatal("missing hash")
	}
}
