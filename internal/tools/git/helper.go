package git

import (
	"context"
	"os/exec"

	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/terminal"
)

// IsGitRepository checks if a directory is a git repository.
func IsGitRepository(ctx context.Context, directory string) error {
	output, err := runGit(ctx, directory, "rev-parse", "--git-dir")
	if err != nil || output.ExitCode != 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return tools.ErrToolNotFound
	}
	return nil
}

func runGit(ctx context.Context, directory string, args ...string) (terminal.CommandOutput, error) {
	args = append([]string{"--no-optional-locks", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = directory
	output, err := terminal.ExecuteCommandWithOutput(ctx, cmd)
	if ctx.Err() != nil {
		return output, ctx.Err()
	}
	return output, err
}
