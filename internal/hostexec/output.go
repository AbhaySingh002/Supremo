// Package hostexec runs explicit host commands with bounded output and
// process-tree cancellation. It is shared by backend tools and local UI
// actions without coupling either layer to the other.
package hostexec

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

const (
	MaxOutputBytes   = 1 << 20
	commandWaitDelay = 2 * time.Second
)

type Output struct {
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
	remaining := MaxOutputBytes - b.buffer.Len()
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

func Run(ctx context.Context, cmd *exec.Cmd) (Output, error) {
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	prepareCommand(cmd)
	err := cmd.Run()
	cleanupErr := cleanupCommand(cmd)
	output := Output{
		Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes(),
		StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
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
	if cleanupErr != nil {
		return output, cleanupErr
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
