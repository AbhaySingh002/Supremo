package terminal

import (
	"bytes"
	"context"
	"os/exec"
)

const maxCommandOutputBytes = 1 << 20

// CommandOutput captures bounded stdout and stderr from a command execution.
type CommandOutput struct {
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	TimedOut        bool
	Canceled        bool
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := maxCommandOutputBytes - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = b.buffer.Write(data[:remaining])
		b.truncated = true
		return len(data), nil
	}
	return b.buffer.Write(data)
}

// ExecuteCommandWithOutput executes a command and captures up to 1 MiB per stream.
func ExecuteCommandWithOutput(ctx context.Context, cmd *exec.Cmd) (CommandOutput, error) {
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := CommandOutput{
		Stdout:          stdout.buffer.Bytes(),
		Stderr:          stderr.buffer.Bytes(),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	}
	if ctx.Err() != nil {
		output.TimedOut = ctx.Err() == context.DeadlineExceeded
		output.Canceled = ctx.Err() == context.Canceled
		if output.TimedOut {
			output.ExitCode = -1
		} else {
			output.ExitCode = -2
		}
		return output, nil
	}
	if err == nil {
		return output, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		output.ExitCode = exitErr.ExitCode()
		return output, nil
	}
	return output, err
}
