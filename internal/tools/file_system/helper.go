package file_system

import (
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// IsDirectory checks if the given path is a directory.
func IsDirectory(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// EnsureParentDirectoryExists checks if the parent directory of a path exists.
func EnsureParentDirectoryExists(path string) error {
	parentDir := filepath.Dir(path)
	if _, err := os.Stat(parentDir); os.IsNotExist(err) {
		return tools.ErrToolNotFound
	}
	return nil
}
