package filesystem

import (
	"os"
	"path/filepath"
)

// EnsureParentDirectoryExists checks if the parent directory of a path exists.
func EnsureParentDirectoryExists(path string) error {
	_, err := os.Stat(filepath.Dir(path))
	return err
}
