package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestTwoSessionsFromOneObservedVersionProduceOneWriterAndOneNamedConflict(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shared.txt"), []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	contexts := map[string]context.Context{}
	for _, sessionID := range []string{"child-a", "child-b"} {
		ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})
		contexts[sessionID] = ctx
		if result, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "shared.txt"}); err != nil || !result.Success {
			t.Fatalf("%s read = %#v, %v", sessionID, result, err)
		}
	}

	type outcome struct {
		session string
		result  *tools.ToolResult
		err     error
	}
	ready := make(chan struct{})
	finished := make(chan outcome, 2)
	for _, sessionID := range []string{"child-a", "child-b"} {
		go func(sessionID string) {
			<-ready
			result, err := (&ReplaceInFile{}).Execute(contexts[sessionID], map[string]any{
				"path": "shared.txt", "old_string": "original", "new_string": sessionID,
			})
			finished <- outcome{session: sessionID, result: result, err: err}
		}(sessionID)
	}
	close(ready)
	results := []outcome{<-finished, <-finished}
	winner := ""
	for _, result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.result.Success {
			winner = result.session
		}
	}
	if winner == "" {
		t.Fatalf("neither child wrote: %#v", results)
	}
	for _, result := range results {
		if result.session != winner && (result.result.Success || !strings.Contains(result.result.Message, winner)) {
			t.Fatalf("loser conflict does not name writer %q: %#v", winner, result.result)
		}
	}
}

func TestSafetyReadThenEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	initialContent := "package main\n\nfunc main() {\n\tprintln(\"v1\")\n}\n"
	if err := os.WriteFile(path, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-1"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Step 1: Read file
	readTool := &ReadFile{}
	readRes, err := readTool.Execute(ctx, map[string]any{"path": "main.go"})
	if err != nil || !readRes.Success {
		t.Fatalf("read failed: %#v, %v", readRes, err)
	}

	// Verify observation established
	target, _ := ResolveTarget(ctx, "main.go")
	obs, found, err := GetTrustedObservation(ctx, sessionID, target)
	if err != nil || !found || obs.Negative {
		t.Fatalf("expected present observation, got found=%v, obs=%#v, err=%v", found, obs, err)
	}
	v1Hash := obs.SourceHash

	// Step 2: Edit file
	editTool := &ReplaceInFile{}
	editRes, err := editTool.Execute(ctx, map[string]any{
		"path":       "main.go",
		"old_string": "println(\"v1\")",
		"new_string": "println(\"v2\")",
	})
	if err != nil || !editRes.Success {
		t.Fatalf("edit failed: %#v, %v", editRes, err)
	}

	// Verify observation updated to v2
	obs2, found2, _ := GetTrustedObservation(ctx, sessionID, target)
	if !found2 || obs2.Negative || obs2.SourceHash == v1Hash {
		t.Fatalf("observation not updated to v2: %#v", obs2)
	}

	// Verify physical content
	diskContent, _ := os.ReadFile(path)
	if !strings.Contains(string(diskContent), "println(\"v2\")") {
		t.Fatalf("disk content not updated: %s", string(diskContent))
	}
}

func TestSafetyEditWithoutReadBlocked(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "foo.go")
	if err := os.WriteFile(path, []byte("package foo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-2"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Attempt edit on existing unread file without read
	editTool := &ReplaceInFile{}
	editRes, err := editTool.Execute(ctx, map[string]any{
		"path":       "foo.go",
		"old_string": "package foo",
		"new_string": "package bar",
	})

	if err != nil {
		t.Fatalf("unexpected fatal execution error: %v", err)
	}
	if editRes == nil || editRes.Success {
		t.Fatalf("expected unread edit to fail, got success: %#v", editRes)
	}
	if !strings.Contains(editRes.Message, "must be read before editing") {
		t.Fatalf("unexpected error message: %s", editRes.Message)
	}

	// Verify file was NOT modified
	diskContent, _ := os.ReadFile(path)
	if string(diskContent) != "package foo\n" {
		t.Fatalf("file modified despite unread block: %s", string(diskContent))
	}
}

func TestSafetyBlindOverwriteExistingFileBlocked(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte(`{"env":"production"}`), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-3"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Blind write to existing file without read
	writeTool := &WriteFile{}
	writeRes, err := writeTool.Execute(ctx, map[string]any{
		"path":    "config.json",
		"content": `{"env":"dev"}`,
	})

	if err != nil {
		t.Fatalf("unexpected fatal execution error: %v", err)
	}
	if writeRes == nil || writeRes.Success {
		t.Fatalf("expected blind overwrite to fail, got success: %#v", writeRes)
	}
	if !strings.Contains(writeRes.Message, "was not read in this session") {
		t.Fatalf("unexpected error message: %s", writeRes.Message)
	}

	// Verify file content unchanged
	diskContent, _ := os.ReadFile(path)
	if string(diskContent) != `{"env":"production"}` {
		t.Fatalf("file clobbered: %s", string(diskContent))
	}
}

func TestSafetyNewFileCreationAllowed(t *testing.T) {
	root := t.TempDir()
	sessionID := "sess-safety-4"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Writing a brand new absent file succeeds without prior read
	writeTool := &WriteFile{}
	writeRes, err := writeTool.Execute(ctx, map[string]any{
		"path":    "new_script.py",
		"content": "print('hello')",
	})

	if err != nil || !writeRes.Success {
		t.Fatalf("new file creation failed: %#v, %v", writeRes, err)
	}

	createdPath := filepath.Join(root, "new_script.py")
	diskContent, err := os.ReadFile(createdPath)
	if err != nil || string(diskContent) != "print('hello')" {
		t.Fatalf("file not created properly: %s, %v", string(diskContent), err)
	}
}

func TestSafetyWriteDoesNotOverwriteUnobservedFile(t *testing.T) {
	root := t.TempDir()
	sessionID := "sess-safety-5"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Pre-create file externally
	targetPath := filepath.Join(root, "race.txt")
	if err := os.WriteFile(targetPath, []byte("external content"), 0644); err != nil {
		t.Fatal(err)
	}

	writeTool := &WriteFile{}
	writeRes, err := writeTool.Execute(ctx, map[string]any{
		"path":    "race.txt",
		"content": "agent content",
	})

	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if writeRes == nil || writeRes.Success {
		t.Fatalf("expected write conflict on unobserved file, got success: %#v", writeRes)
	}
	if !strings.Contains(writeRes.Message, "not read") {
		t.Fatalf("unexpected conflict message: %s", writeRes.Message)
	}

	// External content must remain untouched
	diskContent, _ := os.ReadFile(targetPath)
	if string(diskContent) != "external content" {
		t.Fatalf("external file corrupted: %s", string(diskContent))
	}
}

func TestSafetyStaleEditDetectedAndRejected(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "calc.go")
	if err := os.WriteFile(path, []byte("func Add(a, b int) int { return a + b }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-6"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// 1. Read file
	readTool := &ReadFile{}
	if res, err := readTool.Execute(ctx, map[string]any{"path": "calc.go"}); err != nil || !res.Success {
		t.Fatalf("read failed: %#v, %v", res, err)
	}

	// 2. External mutation
	if err := os.WriteFile(path, []byte("func Add(a, b int) int { return a + b + 1 }\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Edit expecting original version
	editTool := &ReplaceInFile{}
	editRes, err := editTool.Execute(ctx, map[string]any{
		"path":       "calc.go",
		"old_string": "return a + b",
		"new_string": "return a * b",
	})

	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if editRes == nil || editRes.Success {
		t.Fatalf("expected stale edit to fail, got success: %#v", editRes)
	}
	if !strings.Contains(editRes.Message, "changed since it was read") {
		t.Fatalf("unexpected stale message: %s", editRes.Message)
	}

	// 4. File on disk still has external mutation
	diskContent, _ := os.ReadFile(path)
	if !strings.Contains(string(diskContent), "return a + b + 1") {
		t.Fatalf("disk content mutated despite stale failure: %s", string(diskContent))
	}
}

func TestSafetyExternalDeleteDetected(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "temp.go")
	if err := os.WriteFile(path, []byte("package temp\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-7"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Read
	readTool := &ReadFile{}
	if res, err := readTool.Execute(ctx, map[string]any{"path": "temp.go"}); err != nil || !res.Success {
		t.Fatalf("read failed: %v", err)
	}

	// External delete
	_ = os.Remove(path)

	// Attempt edit
	editTool := &ReplaceInFile{}
	editRes, err := editTool.Execute(ctx, map[string]any{
		"path":       "temp.go",
		"old_string": "package temp",
		"new_string": "package temp2",
	})

	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if editRes == nil || editRes.Success {
		t.Fatalf("expected edit on deleted file to fail, got success: %#v", editRes)
	}
}

func TestSafetyMultiEditWithoutReread(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "service.go")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-8"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Step 1: Read
	readTool := &ReadFile{}
	if res, err := readTool.Execute(ctx, map[string]any{"path": "service.go"}); err != nil || !res.Success {
		t.Fatalf("read failed: %v", err)
	}

	// Step 2: Edit 1
	editTool := &ReplaceInFile{}
	res1, err := editTool.Execute(ctx, map[string]any{
		"path":       "service.go",
		"old_string": "line1",
		"new_string": "LINE_ONE",
	})
	if err != nil || !res1.Success {
		t.Fatalf("first edit failed: %#v, %v", res1, err)
	}

	// Step 3: Edit 2 immediately without read_file
	res2, err := editTool.Execute(ctx, map[string]any{
		"path":       "service.go",
		"old_string": "line3",
		"new_string": "LINE_THREE",
	})
	if err != nil || !res2.Success {
		t.Fatalf("consecutive edit failed: %#v, %v", res2, err)
	}

	// Verify final content
	diskContent, _ := os.ReadFile(path)
	expected := "LINE_ONE\nline2\nLINE_THREE\n"
	if string(diskContent) != expected {
		t.Fatalf("content mismatch: got %q, want %q", string(diskContent), expected)
	}
}

func TestSafetyExternalChangeAfterFirstEdit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "multi.go")
	if err := os.WriteFile(path, []byte("A\nB\nC\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-9"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Read
	readTool := &ReadFile{}
	if res, err := readTool.Execute(ctx, map[string]any{"path": "multi.go"}); err != nil || !res.Success {
		t.Fatalf("read failed: %v", err)
	}

	// Edit 1
	editTool := &ReplaceInFile{}
	res1, err := editTool.Execute(ctx, map[string]any{"path": "multi.go", "old_string": "A", "new_string": "A1"})
	if err != nil || !res1.Success {
		t.Fatalf("edit 1 failed: %v", err)
	}

	// External mutation
	if err := os.WriteFile(path, []byte("A1\nB_EXTERNAL\nC\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Edit 2 should fail stale
	res2, err := editTool.Execute(ctx, map[string]any{"path": "multi.go", "old_string": "C", "new_string": "C1"})
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if res2 == nil || res2.Success {
		t.Fatalf("expected second edit to fail stale, got success: %#v", res2)
	}
	if !strings.Contains(res2.Message, "changed since it was read") {
		t.Fatalf("unexpected error message: %s", res2.Message)
	}
}

func TestSafetyPathAliasing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alias.go")
	if err := os.WriteFile(path, []byte("var X = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-safety-10"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Read with "./alias.go"
	readTool := &ReadFile{}
	if res, err := readTool.Execute(ctx, map[string]any{"path": "./alias.go"}); err != nil || !res.Success {
		t.Fatalf("read failed: %v", err)
	}

	// Edit with "alias.go"
	editTool := &ReplaceInFile{}
	editRes, err := editTool.Execute(ctx, map[string]any{
		"path":       "alias.go",
		"old_string": "var X = 1",
		"new_string": "var X = 2",
	})
	if err != nil || !editRes.Success {
		t.Fatalf("edit with aliased path failed: %#v, %v", editRes, err)
	}
}

func TestSafetyRestartPreservesObservationAcrossRestart(t *testing.T) {
	root := t.TempDir()
	if _, err := state.Open(root); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "app.go")
	if err := os.WriteFile(path, []byte("package app\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sessionID := "sess-restart"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})

	// Read file and record in store
	readTool := &ReadFile{}
	if res, err := readTool.Execute(ctx, map[string]any{"path": "app.go"}); err != nil || !res.Success {
		t.Fatal(err)
	}

	// Simulate process restart: re-open store
	store2, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}

	// Observation should be found in store2
	target, _ := ResolveTarget(ctx, "app.go")
	obs, found, err := store2.LatestFileObservation(ctx, sessionID, target.RelPath)
	if err != nil || !found || obs.Negative {
		t.Fatalf("observation lost after restart: found=%v obs=%#v err=%v", found, obs, err)
	}

	// Edit succeeds because disk is still at observed version
	editTool := &ReplaceInFile{}
	editRes, err := editTool.Execute(ctx, map[string]any{
		"path":       "app.go",
		"old_string": "package app",
		"new_string": "package app_v2",
	})
	if err != nil || !editRes.Success {
		t.Fatalf("edit after restart failed: %#v, %v", editRes, err)
	}
}

func TestSafetyUnreadDeleteBlocked(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "keep.go")
	if err := os.WriteFile(path, []byte("package keep\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sessionID := "sess-unread-del"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})
	res, err := (&DeleteFile{}).Execute(ctx, map[string]any{"path": "keep.go"})
	if err != nil || res == nil || res.Success {
		t.Fatalf("expected unread delete to fail: %#v %v", res, err)
	}
	if !strings.Contains(res.Message, "must be read before deletion") {
		t.Fatalf("message=%s", res.Message)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file changed: %v", err)
	}
}

func TestSafetyReadThenDelete(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "gone.go")
	if err := os.WriteFile(path, []byte("package gone\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sessionID := "sess-read-del"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})
	if res, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "gone.go"}); err != nil || !res.Success {
		t.Fatalf("read: %#v %v", res, err)
	}
	res, err := (&DeleteFile{}).Execute(ctx, map[string]any{"path": "gone.go"})
	if err != nil || !res.Success {
		t.Fatalf("delete: %#v %v", res, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still present: %v", err)
	}
	target, _ := ResolveTarget(ctx, "gone.go")
	obs, found, _ := GetTrustedObservation(ctx, sessionID, target)
	if !found || !obs.Negative {
		t.Fatalf("expected ABSENT observation, found=%v obs=%#v", found, obs)
	}
}

func TestSafetyStaleDeleteRejected(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stale.go")
	if err := os.WriteFile(path, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sessionID := "sess-stale-del"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})
	if res, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "stale.go"}); err != nil || !res.Success {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("v2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := (&DeleteFile{}).Execute(ctx, map[string]any{"path": "stale.go"})
	if err != nil || res.Success {
		t.Fatalf("expected stale delete to fail: %#v %v", res, err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "v2\n" {
		t.Fatalf("file mutated: %q", got)
	}
}

func TestSafetyUnreadRenameBlocked(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src.go"), []byte("package src\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: "sess-unread-ren"})
	res, err := (&RenameFile{}).Execute(ctx, map[string]any{"old_path": "src.go", "new_path": "dst.go"})
	if err != nil || res.Success {
		t.Fatalf("expected unread rename to fail: %#v %v", res, err)
	}
	if !strings.Contains(res.Message, "must be read before renaming") {
		t.Fatalf("message=%s", res.Message)
	}
}

func TestSafetyReadThenRename(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src.go"), []byte("package src\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sessionID := "sess-read-ren"
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: sessionID})
	if res, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "src.go"}); err != nil || !res.Success {
		t.Fatal(err)
	}
	res, err := (&RenameFile{}).Execute(ctx, map[string]any{"old_path": "src.go", "new_path": "dst.go"})
	if err != nil || !res.Success {
		t.Fatalf("rename: %#v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(root, "src.go")); !os.IsNotExist(err) {
		t.Fatal("source still present")
	}
	got, err := os.ReadFile(filepath.Join(root, "dst.go"))
	if err != nil || string(got) != "package src\n" {
		t.Fatalf("dest=%q err=%v", got, err)
	}
	src, _ := ResolveTarget(ctx, "src.go")
	dst, _ := ResolveTarget(ctx, "dst.go")
	srcObs, srcFound, _ := GetTrustedObservation(ctx, sessionID, src)
	dstObs, dstFound, _ := GetTrustedObservation(ctx, sessionID, dst)
	if !srcFound || !srcObs.Negative {
		t.Fatalf("source obs=%#v found=%v", srcObs, srcFound)
	}
	if !dstFound || dstObs.Negative || dstObs.SourceHash == "" {
		t.Fatalf("dest obs=%#v found=%v", dstObs, dstFound)
	}
}

func TestSafetyRenameDestinationCollision(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "src.go"), []byte("source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dst.go"), []byte("dest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: "sess-ren-collide"})
	if res, err := (&ReadFile{}).Execute(ctx, map[string]any{"path": "src.go"}); err != nil || !res.Success {
		t.Fatal(err)
	}
	res, err := (&RenameFile{}).Execute(ctx, map[string]any{"old_path": "src.go", "new_path": "dst.go"})
	if err != nil || res.Success {
		t.Fatalf("expected collision: %#v %v", res, err)
	}
	src, _ := os.ReadFile(filepath.Join(root, "src.go"))
	dst, _ := os.ReadFile(filepath.Join(root, "dst.go"))
	if string(src) != "source\n" || string(dst) != "dest\n" {
		t.Fatalf("clobbered src=%q dst=%q", src, dst)
	}
}

func TestSafetyDirectoryDeleteAndRenameRejected(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pkg")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithProgressScope(tools.WithWorkspace(context.Background(), root), tools.ProgressScope{SessionID: "sess-dir"})
	del, err := (&DeleteFile{}).Execute(ctx, map[string]any{"path": "pkg"})
	if err != nil || del.Success || !strings.Contains(del.Message, "cannot be deleted") {
		t.Fatalf("delete dir: %#v %v", del, err)
	}
	ren, err := (&RenameFile{}).Execute(ctx, map[string]any{"old_path": "pkg", "new_path": "pkg2"})
	if err != nil || ren.Success || !strings.Contains(ren.Message, "cannot be renamed") {
		t.Fatalf("rename dir: %#v %v", ren, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.go")); err != nil {
		t.Fatalf("directory mutated: %v", err)
	}
}
