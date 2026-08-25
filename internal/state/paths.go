package state

import (
	"os"
	"path/filepath"
	"strings"
)

// DataDir returns the global application data directory for Supremo.
// It respects SUPREMO_DATA_DIR environment variable if set.
func DataDir() string {
	if custom := os.Getenv("SUPREMO_DATA_DIR"); strings.TrimSpace(custom) != "" {
		return filepath.Clean(custom)
	}
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = ".supremo-data"
		} else {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, "supremo")
}

// GlobalDBPath returns the path to the lightweight global workspace registry database.
func GlobalDBPath() string {
	return filepath.Join(DataDir(), "global.db")
}

// WorkspacesDir returns the root directory where all workspace state databases live.
func WorkspacesDir() string {
	return filepath.Join(DataDir(), "workspaces")
}

// WorkspaceDir returns the storage directory for a specific workspace ID.
func WorkspaceDir(workspaceID string) string {
	return filepath.Join(WorkspacesDir(), workspaceID)
}

// WorkspaceDBPath returns the path to the workspace SQLite database.
func WorkspaceDBPath(workspaceID string) string {
	return filepath.Join(WorkspaceDir(workspaceID), "state.db")
}

// WorkspaceObjectsDir returns the path to the workspace CAS objects directory.
func WorkspaceObjectsDir(workspaceID string) string {
	return filepath.Join(WorkspaceDir(workspaceID), "objects")
}

// WorkspaceCheckpointsDir returns the path to the workspace checkpoints directory.
func WorkspaceCheckpointsDir(workspaceID string) string {
	return filepath.Join(WorkspaceDir(workspaceID), "checkpoints")
}
