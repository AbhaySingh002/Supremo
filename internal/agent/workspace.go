package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/state"
)

// InitializeWorkspace records a small, local snapshot without replacing user-maintained memory.
func InitializeWorkspace(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	store, err := state.Open(root)
	if err != nil {
		return "", err
	}
	memory, err := store.WorkspaceMemory(context.Background())
	if err != nil {
		return "", err
	}
	if strings.Contains(memory, "## Workspace Snapshot") {
		return "Workspace memory is already initialized.", nil
	}

	var snapshot strings.Builder
	if len(memory) == 0 {
		snapshot.WriteString("# Codebase Memory\n")
	}
	fmt.Fprintf(&snapshot, "\n## Workspace Snapshot\n- Root: `%s`\n", filepath.Base(root))
	if module, ok := goModule(root); ok {
		fmt.Fprintf(&snapshot, "- Go module: `%s`\n", module)
	}
	if exists(root, "Makefile") {
		snapshot.WriteString("- Validation: `make precommit`\n")
	}
	if exists(root, "SUPREMO.md") || exists(root, "AGENTS.md") {
		snapshot.WriteString("- Repository instructions are loaded into agent context.\n")
	}
	if err := store.SetWorkspaceMemory(context.Background(), memory+snapshot.String()); err != nil {
		return "", err
	}
	return fmt.Sprintf("Workspace memory initialized for %s.", filepath.Base(root)), nil
}

func goModule(root string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if module, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(module), module != ""
		}
	}
	return "", false
}

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}
