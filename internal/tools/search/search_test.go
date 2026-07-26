package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestSearchTextBoundsAndSkips(t *testing.T) {
	root := t.TempDir()
	write := func(name, text string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("visible.txt", "needle")
	write(".hidden/ignored.txt", "needle")
	write("binary.txt", "needle\x00")
	write("too-large.txt", "needle")
	if err := os.Truncate(filepath.Join(root, "too-large.txt"), tools.MaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	deep := root
	for i := 0; i < tools.MaxSearchDepth+1; i++ {
		deep = filepath.Join(deep, "nested")
	}
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "too-deep.txt"), []byte("needle"), 0600); err != nil {
		t.Fatal(err)
	}

	ctx := tools.WithWorkspace(context.Background(), root)
	result, err := (&SearchText{}).Execute(ctx, map[string]any{"path": ".", "pattern": "needle"})
	if err != nil || !result.Success {
		t.Fatalf("search failed: result=%#v err=%v", result, err)
	}
	matches := result.Data["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("expected only visible match, got %#v", matches)
	}

	write("many.txt", strings.Repeat("needle\n", tools.MaxSearchResults+1))
	result, err = (&SearchText{}).Execute(ctx, map[string]any{"path": ".", "pattern": "needle"})
	if err != nil || !result.Success || !result.Data["truncated"].(bool) || len(result.Data["matches"].([]interface{})) != tools.MaxSearchResults {
		t.Fatalf("result limit was not enforced: result=%#v err=%v", result, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	result, err = (&SearchText{}).Execute(canceled, map[string]any{"path": ".", "pattern": "needle"})
	if err != nil || result.Success || !strings.Contains(result.Message, context.Canceled.Error()) {
		t.Fatalf("canceled search: result=%#v err=%v", result, err)
	}
}

func TestSearchFileNameUsesFilepathGlob(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithWorkspace(context.Background(), root)
	result, err := (&SearchFileName{}).Execute(ctx, map[string]any{"path": ".", "pattern": "["})
	if err != nil || result.Success {
		t.Fatalf("invalid glob was accepted: result=%#v err=%v", result, err)
	}
}
