package git_tools

import (
	"os/exec"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// IsGitRepository checks if a directory is a git repository.
func IsGitRepository(directory string) error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = directory
	if err := cmd.Run(); err != nil {
		return tools.ErrToolNotFound
	}
	return nil
}
