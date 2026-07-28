package terminal

import (
	"context"
	"os/exec"
	"strconv"
	"time"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ExecuteCommand runs a shell command and returns its output.
// This tool exists because coding agents need to run build scripts, tests,
// package managers, and other terminal commands to validate changes and
// perform development tasks.
// Claude Code and Cursor use this when users ask to run commands or when
// the agent needs to execute build/test steps as part of the solution.
// It is intentionally not a sandbox; the manager requires explicit approval.

type ExecuteCommandInput struct {
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	Directory string   `json:"directory"`
	Timeout   int      `json:"timeout"` // timeout in seconds
}

type ExecuteCommandOutput struct {
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

type ExecuteCommand struct{}

func (t *ExecuteCommand) Name() string {
	return "execute_command"
}

func (t *ExecuteCommand) Description() string {
	return "Approved escape hatch for arbitrary commands; it is not sandboxed. Returns bounded stdout and stderr."
}

func (t *ExecuteCommand) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The command to execute (e.g., 'go', 'npm', 'python')",
			},
			"args": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Arguments to pass to the command",
			},
			"directory": map[string]any{
				"type":        "string",
				"description": "Working directory for the command",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default: 30)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *ExecuteCommand) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed ExecuteCommandInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate command is not empty
	if parsed.Command == "" {
		return tools.BuildToolResult(false, "Command cannot be empty", nil), nil
	}

	// Set default timeout
	timeout := 30 * time.Second
	if parsed.Timeout > 0 {
		timeout = time.Duration(parsed.Timeout) * time.Second
	}

	// Create command with context for timeout
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, parsed.Command, parsed.Args...)

	cmd.Dir = tools.Workspace(ctx)
	if cmd.Dir == "" {
		return tools.BuildToolResult(false, "Workspace is required", nil), nil
	}
	if parsed.Directory != "" {
		directory, err := tools.ValidateAndResolvePath(ctx, parsed.Directory)
		if err != nil {
			return tools.BuildToolResult(false, "Working directory must be inside the workspace", nil), nil
		}
		cmd.Dir = directory
	}

	// Execute command and capture output
	cmdOutput, err := ExecuteCommandWithOutput(cmdCtx, cmd)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to execute command: "+err.Error(), nil), nil
	}

	exitCode := cmdOutput.ExitCode
	// Build output
	output := ExecuteCommandOutput{
		ExitCode:        exitCode,
		Stdout:          string(cmdOutput.Stdout),
		Stderr:          string(cmdOutput.Stderr),
		StdoutTruncated: cmdOutput.StdoutTruncated,
		StderrTruncated: cmdOutput.StderrTruncated,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	success := exitCode == 0
	message := "Command executed"
	if cmdOutput.TimedOut {
		message = "Command timed out"
	} else if cmdOutput.Canceled {
		message = "Command canceled"
	} else if exitCode != 0 {
		message = "Command failed with exit code " + strconv.Itoa(exitCode)
	}

	return tools.BuildToolResult(success, message, dataMap), nil
}
