package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/state"
)

// RemoveWorkspaceState permanently removes only Supremo-owned state for one
// workspace while preserving provider configuration.
func RemoveWorkspaceState(root string) error {
	_ = state.CloseWorkspace(root)
	if workspaceID, err := state.ResolveWorkspaceIdentity(context.Background(), root); err == nil && workspaceID != "" {
		_ = os.RemoveAll(state.WorkspaceDir(workspaceID))
	}
	for _, name := range []string{".session", ".sessions", ".scratchpad"} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}
	for _, name := range []string{"state", "objects"} {
		if err := os.RemoveAll(filepath.Join(root, ".supremo", name)); err != nil {
			return fmt.Errorf("remove .supremo/%s: %w", name, err)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".supremo")); err == nil && len(entries) == 0 {
		if err := os.Remove(filepath.Join(root, ".supremo")); err != nil {
			return fmt.Errorf("remove empty .supremo: %w", err)
		}
	}
	return nil
}
