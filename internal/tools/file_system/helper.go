package file_system

import (
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// EnsureParentDirectoryExists checks if the parent directory of a path exists.
func EnsureParentDirectoryExists(path string) error {
	parentDir := filepath.Dir(path)
	if _, err := os.Stat(parentDir); os.IsNotExist(err) {
		return tools.ErrToolNotFound
	}
	return nil
}
