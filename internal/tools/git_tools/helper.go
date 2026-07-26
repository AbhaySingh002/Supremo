package git_tools

import (
	"context"
	"os/exec"

	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/terminal"
)

// IsGitRepository checks if a directory is a git repository.
func IsGitRepository(ctx context.Context, directory string) error {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	cmd.Dir = directory
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return tools.ErrToolNotFound
	}
	return nil
}

func runGit(ctx context.Context, directory string, args ...string) (terminal.CommandOutput, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	output, err := terminal.ExecuteCommandWithOutput(ctx, cmd)
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	return output, err
}
