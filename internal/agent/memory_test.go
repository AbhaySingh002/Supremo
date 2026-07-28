package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

func TestMemoryPrunesAndRestoresWindow(t *testing.T) {
	root := t.TempDir()
	memory := newInMemoryMemory(root)
	ctx := context.Background()
	if err := memory.Append(ctx, "session", models.Message{Role: models.RoleUser, Content: "first request"}); err != nil {
		t.Fatal(err)
	}
	if err := memory.Append(ctx, "session", models.Message{Role: models.RoleTool, Content: strings.Repeat("output ", 1_000)}); err != nil {
		t.Fatal(err)
	}
	if err := memory.Append(ctx, "session", models.Message{Role: models.RoleAssistant, Content: strings.Repeat("answer ", 100)}); err != nil {
		t.Fatal(err)
	}

	window, err := memory.GetWindow(ctx, "session", 200, 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(window) == 0 {
		t.Fatal("expected a recent history window")
	}
	summary, err := memory.GetSummary(ctx, "session", summaryTokens)
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" {
		t.Fatal("expected older history to be summarized")
	}
	if _, err := os.Stat(filepath.Join(root, ".session", "session.json")); err != nil {
		t.Fatalf("expected session checkpoint: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".scratchpad", "session"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected full tool output in scratchpad: %v, entries=%d", err, len(entries))
	}
	persistent, err := memory.PersistentContext(1_000)
	if err != nil || !strings.Contains(persistent, "Codebase Memory") {
		t.Fatalf("expected persistent memory context: %v", err)
	}
}

func TestMemoryKeepsRecentProgress(t *testing.T) {
	root := t.TempDir()
	memory := newInMemoryMemory(root)
	memory.mu.Lock()
	if err := memory.ensureStorageLocked(); err != nil {
		memory.mu.Unlock()
		t.Fatal(err)
	}
	for i := 0; i < maxProgressLines+20; i++ {
		if err := memory.appendProgressLocked("session", fmt.Sprintf("entry-%d", i)); err != nil {
			memory.mu.Unlock()
			t.Fatal(err)
		}
	}
	memory.mu.Unlock()
	progress, err := os.ReadFile(filepath.Join(root, ".memory", "progress.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(progress) > maxProgressBytes || !strings.Contains(string(progress), "entry-119") || strings.Contains(string(progress), "entry-0") {
		t.Fatalf("progress was not bounded to recent entries: %d bytes", len(progress))
	}
	context, err := memory.PersistentContext(100)
	if err != nil || !strings.Contains(context, "entry-119") {
		t.Fatalf("expected recent progress in context: %q, %v", context, err)
	}
}

func TestMemoryKeepsOversizedNewestMessageAndPrunesScratchpad(t *testing.T) {
	root := t.TempDir()
	memory := newInMemoryMemory(root)
	ctx := context.Background()
	if err := memory.Append(ctx, "session", models.Message{Role: models.RoleUser, Content: "older"}); err != nil {
		t.Fatal(err)
	}
	if err := memory.Append(ctx, "session", models.Message{Role: models.RoleAssistant, Content: strings.Repeat("newest ", 100)}); err != nil {
		t.Fatal(err)
	}
	window, err := memory.GetWindow(ctx, "session", 10, 10)
	if err != nil || len(window) != 1 || !strings.Contains(window[0].Content, "newest") || !strings.Contains(window[0].Content, "[truncated]") {
		t.Fatalf("newest oversized message was lost: %#v, %v", window, err)
	}
	summary, err := memory.GetSummary(ctx, "session", summaryTokens)
	if err != nil || !strings.Contains(summary, "older") {
		t.Fatalf("same-turn summary missing: %q, %v", summary, err)
	}

	dir := filepath.Join(root, ".scratchpad", "session")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(old, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(old, maxScratchpadBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := memory.pruneScratchpadLocked(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("oversized scratchpad entry was retained: %v", err)
	}
}
