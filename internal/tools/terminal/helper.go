package terminal

import (
	"context"
	"os/exec"

	"github.com/AbhaySingh002/supremo/internal/hostexec"
)

const maxCommandOutputBytes = hostexec.MaxOutputBytes

// CommandOutput captures bounded stdout and stderr from a command execution.
type CommandOutput = hostexec.Output

// ExecuteCommandWithOutput executes a command and captures up to 1 MiB per stream.
func ExecuteCommandWithOutput(ctx context.Context, cmd *exec.Cmd) (CommandOutput, error) {
	return hostexec.Run(ctx, cmd)
}
