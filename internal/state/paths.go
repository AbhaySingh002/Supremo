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
