package terminal

import (
	"context"
	"encoding/json"
	"os/exec"
	"syscall"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ParseInput parses input into the target struct using JSON marshaling.
func ParseInput(input any, target any) error {
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return tools.ErrInvalidInput
	}
	if err := json.Unmarshal(inputBytes, target); err != nil {
		return tools.ErrInvalidInput
	}
	return nil
}

// ValidateDirectory validates that a directory is not empty.
func ValidateDirectory(directory string) error {
	if directory == "" {
		return tools.ErrInvalidInput
	}
	return nil
}

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
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(interface{ ExitStatus() int }); ok {
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

// SerializeOutput converts a struct to a map[string]interface{} for ToolResult.
func SerializeOutput(output any) (map[string]interface{}, error) {
	outputMap, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}

	var dataMap map[string]interface{}
	if err := json.Unmarshal(outputMap, &dataMap); err != nil {
		return nil, err
	}

	return dataMap, nil
}

// BuildToolResult creates a ToolResult with the given parameters.
func BuildToolResult(success bool, message string, data map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{
		Success: success,
		Message: message,
		Data:    data,
	}
}
