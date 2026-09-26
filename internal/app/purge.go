package app

import "github.com/AbhaySingh002/supremo/internal/state"

// RemoveWorkspaceState permanently removes only Supremo-owned state for one
// workspace while preserving provider configuration.
func RemoveWorkspaceState(root string) error {
	return state.CloseWorkspace(root)
}
