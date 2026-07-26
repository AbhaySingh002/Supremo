package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/storage"
)

// InitializeWorkspace records a small, local snapshot without replacing user-maintained memory.
func InitializeWorkspace(root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	memoryDir := filepath.Join(root, ".memory")
	if err := os.MkdirAll(memoryDir, 0700); err != nil {
		return "", err
	}
	memoryPath := filepath.Join(memoryDir, "MEMORY.md")
	memory, err := os.ReadFile(memoryPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if strings.Contains(string(memory), "## Workspace Snapshot") {
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
	if err := storage.WriteFileAtomic(memoryPath, append(memory, snapshot.String()...), 0600); err != nil {
		return "", err
	}

	progressPath := filepath.Join(memoryDir, "progress.md")
	progress, err := os.ReadFile(progressPath)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(progress) == 0 {
		progress = []byte("# Progress\n\n")
	}
	entry := fmt.Sprintf("- %s: initialized workspace memory\n", time.Now().UTC().Format(time.RFC3339))
	if err := storage.WriteFileAtomic(progressPath, append(progress, entry...), 0600); err != nil {
		return "", err
	}
	return "Workspace memory initialized in .memory/MEMORY.md.", nil
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
