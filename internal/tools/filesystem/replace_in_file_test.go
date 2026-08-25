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

func replaceCtx(t *testing.T, root, sessionID string) context.Context {
	t.Helper()
	return tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})
}

func TestReplaceInFileFreshHash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	before := "package a\nfunc F() {}\n"
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := replaceCtx(t, root, "sess-replace-fresh")
	if res, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "a.go"}); err != nil || !res.Success {
		t.Fatalf("read: %#v %v", res, err)
	}
	result, err := (&ReplaceInFile{}).Execute(ctx, map[string]any{
		"path": "a.go", "old_string": "func F() {}", "new_string": "func F() { return }",
	})
	if err != nil || !result.Success {
		t.Fatalf("replace: %#v %v", result, err)
	}
	if result.Data["new_hash"] == nil || result.Data["diff"] == "" {
		t.Fatalf("expected mutation feedback, got %#v", result.Data)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "return") {
		t.Fatalf("file not updated: %q", got)
	}
}

func TestReplaceInFileStaleHash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := replaceCtx(t, root, "sess-replace-stale")
	if res, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "a.go"}); err != nil || !res.Success {
		t.Fatalf("read: %#v %v", res, err)
	}
	if err := os.WriteFile(path, []byte("hello-external"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := (&ReplaceInFile{}).Execute(ctx, map[string]any{
		"path": "a.go", "old_string": "hello", "new_string": "bye",
	})
	if err != nil || result.Success || result.Error == nil || result.Error.Class != "conflict" {
		t.Fatalf("stale: %#v %v", result, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello-external" {
		t.Fatalf("mutated on conflict: %q", got)
	}
}

func TestReplaceInFileZeroAndManyMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("aa aa"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := replaceCtx(t, root, "sess-replace-matches")
	if res, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "a.go"}); err != nil || !res.Success {
		t.Fatalf("read: %#v %v", res, err)
	}
	zero, err := (&ReplaceInFile{}).Execute(ctx, map[string]any{
		"path": "a.go", "old_string": "zz", "new_string": "yy",
	})
	if err != nil || zero.Success {
		t.Fatalf("zero: %#v %v", zero, err)
	}
	if fmt.Sprint(zero.Data["matches"]) != "0" {
		t.Fatalf("zero matches: %#v", zero.Data["matches"])
	}
	many, err := (&ReplaceInFile{}).Execute(ctx, map[string]any{
		"path": "a.go", "old_string": "aa", "new_string": "bb",
	})
	if err != nil || many.Success || many.Error == nil || many.Error.Message != "multiple matches" {
		t.Fatalf("many: %#v %v", many, err)
	}
}
