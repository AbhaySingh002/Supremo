package repository

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/state"
)

type testEmbeddings struct{}

func (testEmbeddings) Model() string { return "test-embeddings" }

func (testEmbeddings) Embed(_ context.Context, input []string) ([][]float32, error) {
	vectors := make([][]float32, len(input))
	for index := range input {
		vectors[index] = []float32{1, 0}
	}
	return vectors, nil
}

func TestScanIndexesGoSymbolsRelationsAndUpdates(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package sample\n\n// Alpha adds one.\nfunc Alpha() int { return 1 }\n")
	write("b.go", "package sample\n\nfunc Beta() int { return Alpha() }\n")
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	index, err := New(root, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats, err := index.Scan(context.Background()); err != nil || stats.Indexed != 2 {
		t.Fatalf("scan = %#v, %v", stats, err)
	}
	result, err := index.Query(context.Background(), Query{Text: "Alpha", Kind: "symbol", Exact: true, Limit: 10})
	if err != nil || len(result.Candidates) == 0 {
		t.Fatalf("symbol query = %#v, %v", result, err)
	}
	if result.Candidates[0].Name != "Alpha" {
		t.Fatalf("symbol = %#v", result.Candidates[0])
	}
	if len(result.Relations) == 0 {
		t.Fatalf("expected CALLS graph relation, got %#v", result.Relations)
	}
	if text, err := index.Query(context.Background(), Query{Text: "adds one", FullText: true, Limit: 10}); err != nil || len(text.Candidates) == 0 {
		t.Fatalf("FTS query = %#v, %v", text, err)
	}
	before := result.Candidates[0].Hash
	time.Sleep(time.Millisecond) // filesystems with coarse mtime still re-hash on size change.
	write("a.go", "package sample\n\n// Alpha adds two.\nfunc Alpha() int { return 22 }\n")
	if err := index.IndexPath(context.Background(), "a.go", "", state.EventInput{Type: "tool.completed"}); err != nil {
		t.Fatal(err)
	}
	updated, err := index.Query(context.Background(), Query{Text: "Alpha", Kind: "symbol", Exact: true, Limit: 10})
	if err != nil || len(updated.Candidates) == 0 || updated.Candidates[0].Hash == before {
		t.Fatalf("updated symbol = %#v, %v", updated, err)
	}
	if stale, err := index.Query(context.Background(), Query{Text: "adds one", FullText: true, Limit: 10}); err != nil || len(stale.Candidates) != 0 {
		t.Fatalf("stale FTS result = %#v, %v", stale, err)
	}
}

func TestScanPreservesIdentityForGitRename(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package sample\nfunc Alpha() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "a.go")
	run("commit", "-m", "initial")
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	index, err := New(root, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := store.RepositoryFiles(context.Background())
	if err != nil || len(before) != 1 {
		t.Fatalf("files before rename = %#v, %v", before, err)
	}
	run("mv", "a.go", "renamed.go")
	if _, err := index.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := store.RepositoryFiles(context.Background())
	if err != nil || len(after) != 1 || after[0].Path != "renamed.go" || after[0].FileID != before[0].FileID {
		t.Fatalf("files after rename = %#v, %v", after, err)
	}
}

func TestSemanticLookupIsOptInAndUnavailableProviderDoesNotBreakSearch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package sample\n// concept implementation\nfunc Alpha() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	index, err := New(root, store, testEmbeddings{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := index.SetSemantic(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		embeddings, err := store.RepositoryEmbeddings(context.Background(), "test-embeddings")
		if err == nil && len(embeddings) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	result, err := index.Query(context.Background(), Query{Text: "conceptual behavior", FullText: true, Limit: 10})
	if err != nil || len(result.Candidates) == 0 || result.Candidates[0].SemanticSimilarity == 0 {
		t.Fatalf("semantic result = %#v, %v", result, err)
	}
	index.SetEmbeddingProvider(nil)
	if _, err := index.Query(context.Background(), Query{Text: "Alpha", Kind: "symbol", Exact: true, Limit: 10}); err != nil {
		t.Fatalf("deterministic lookup failed without embeddings: %v", err)
	}
}

func BenchmarkWarmScan(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package sample\nfunc Alpha() {}\n"), 0600); err != nil {
		b.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = state.CloseWorkspace(root) })
	index, err := New(root, store, nil)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := index.Scan(context.Background()); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := index.Scan(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExactQuery(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package sample\nfunc Alpha() {}\n"), 0600); err != nil {
		b.Fatal(err)
	}
	store, err := state.Open(root)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = state.CloseWorkspace(root) })
	index, err := New(root, store, nil)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := index.Scan(context.Background()); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := index.Query(context.Background(), Query{Text: "Alpha", Kind: "symbol", Exact: true, Limit: 10}); err != nil {
			b.Fatal(err)
		}
	}
}
