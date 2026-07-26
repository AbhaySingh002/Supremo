package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/models"
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
