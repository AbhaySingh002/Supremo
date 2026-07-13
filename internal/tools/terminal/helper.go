package terminal

import (
	"context"
	"os/exec"
	"syscall"
)

// CommandOutput captures stdout and stderr from a command execution.
type CommandOutput struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// ExecuteCommandWithOutput executes a command and captures stdout/stderr.
func ExecuteCommandWithOutput(ctx context.Context, cmd *exec.Cmd) (CommandOutput, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CommandOutput{}, err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return CommandOutput{}, err
	}

	if err := cmd.Start(); err != nil {
		return CommandOutput{}, err
	}

	stdoutBytes := make([]byte, 0, 1024)
	stderrBytes := make([]byte, 0, 1024)

	done := make(chan error, 2)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				stdoutBytes = append(stdoutBytes, buf[:n]...)
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				stderrBytes = append(stderrBytes, buf[:n]...)
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()

	err = cmd.Wait()
	<-done
	<-done

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			}
		} else if err == context.DeadlineExceeded {
			exitCode = -1
		}
	}

	return CommandOutput{
		ExitCode: exitCode,
		Stdout:   stdoutBytes,
		Stderr:   stderrBytes,
	}, nil
}
