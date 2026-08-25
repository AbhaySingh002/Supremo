package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/filesystem"
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
	result, err = (&SearchText{}).Execute(ctx, map[string]any{"path": ".", "pattern": "needle", "max_results": tools.MaxSearchResults})
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

func TestSymbolSearchesShareTraversalPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "visible.go"), []byte("func needle() {}\nneedle()\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "hidden.go"), []byte("func needle() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.go"), []byte("func needle() {}\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	deep := root
	for i := 0; i < tools.MaxSearchDepth+1; i++ {
		deep = filepath.Join(deep, "nested")
	}
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "deep.go"), []byte("func needle() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	ctx := tools.WithWorkspace(context.Background(), root)
	input := map[string]any{"directory": ".", "symbol": "needle", "language": "go"}
	references, err := (&FindReferences{}).Execute(ctx, input)
	if err != nil || !references.Success || len(references.Data["references"].([]interface{})) != 2 {
		t.Fatalf("references = %#v, %v", references, err)
	}
	symbols, err := (&FindSymbol{}).Execute(ctx, input)
	if err != nil || !symbols.Success || len(symbols.Data["matches"].([]interface{})) != 1 {
		t.Fatalf("symbols = %#v, %v", symbols, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	symbols, err = (&FindSymbol{}).Execute(canceled, input)
	if err != nil || symbols.Success || !strings.Contains(symbols.Message, context.Canceled.Error()) {
		t.Fatalf("canceled symbols = %#v, %v", symbols, err)
	}
}

func TestSearchTextFileAndContext(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 230; i++ {
		b.WriteString("padding\n")
	}
	b.WriteString("target-needle\n")
	if err := os.WriteFile(filepath.Join(root, "src.go"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithWorkspace(context.Background(), root)
	result, err := (&SearchText{}).Execute(ctx, map[string]any{"path": "src.go", "pattern": "target-needle", "context_lines": 1})
	if err != nil || !result.Success {
		t.Fatalf("search: %#v %v", result, err)
	}
	matches := result.Data["matches"].([]interface{})
	if len(matches) != 1 {
		t.Fatalf("matches %#v", matches)
	}
	row := matches[0].(map[string]interface{})
	if int(row["line"].(float64)) != 231 {
		t.Fatalf("line %#v", row["line"])
	}
	if row["context"] == "" {
		t.Fatal("expected context")
	}
}

func TestSearchHitThenRangedRead(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 250; i++ {
		if i == 220 {
			b.WriteString("hit-target\n")
			continue
		}
		b.WriteString("padding\n")
	}
	if err := os.WriteFile(filepath.Join(root, "big.go"), []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithWorkspace(context.Background(), root)
	search, err := (&SearchText{}).Execute(ctx, map[string]any{"path": "big.go", "pattern": "hit-target"})
	if err != nil || !search.Success {
		t.Fatalf("search: %#v %v", search, err)
	}
	line := int(search.Data["matches"].([]interface{})[0].(map[string]interface{})["line"].(float64))
	if line != 220 {
		t.Fatalf("line %d", line)
	}
	read, err := (&filesystem.ReadFile{}).Execute(ctx, map[string]any{"path": "big.go", "start_line": 190, "end_line": 250})
	if err != nil || !read.Success {
		t.Fatalf("read: %#v %v", read, err)
	}
	content, _ := read.Data["content"].(string)
	if !strings.Contains(content, "220 | hit-target") || strings.Contains(content, "189 |") {
		t.Fatalf("ranged read: %q", content)
	}
}
