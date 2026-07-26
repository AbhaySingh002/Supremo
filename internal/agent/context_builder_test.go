package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProjectInstructionsPrefersSupremoFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agents instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SUPREMO.md"), []byte("supremo instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadProjectInstructions(root); got != "supremo instructions" {
		t.Fatalf("unexpected instructions: %q", got)
	}
}
