package terminal

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ExecuteCommand runs a shell command and returns its output.
// This tool exists because coding agents need to run build scripts, tests,
// package managers, and other terminal commands to validate changes and
// perform development tasks.
// Claude Code and Cursor use this when users ask to run commands or when
// the agent needs to execute build/test steps as part of the solution.
// Security: We validate the command, prevent dangerous commands, limit
// execution time, and run commands in a controlled environment.

type ExecuteCommandInput struct {
	Command   string   `json:"command"`
	Args      []string `json:"args"`
	Directory string   `json:"directory"`
	Timeout   int      `json:"timeout"` // timeout in seconds
}

type ExecuteCommandOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type ExecuteCommand struct{}

func (t *ExecuteCommand) Name() string {
	return "execute_command"
}

func (t *ExecuteCommand) Description() string {
	return "Executes a shell command with optional arguments. Returns exit code, stdout, and stderr."
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

	// Security: Block dangerous commands
	dangerousCommands := []string{"rm -rf", "mkfs", "dd if=", ":(){:|:&};:", "sudo rm"}
	cmdStr := parsed.Command + " " + strings.Join(parsed.Args, " ")
	for _, dangerous := range dangerousCommands {
		if strings.Contains(cmdStr, dangerous) {
			return &tools.ToolResult{
				Success: false,
				Message: "Command blocked for security reasons",
			}, nil
		}
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

	// Set working directory if specified
	if parsed.Directory != "" {
		cmd.Dir = parsed.Directory
	}

	// Execute command and capture output
	cmdOutput, err := ExecuteCommandWithOutput(cmdCtx, cmd)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to execute command: "+err.Error(), nil), nil
	}

	exitCode := cmdOutput.ExitCode
	stdoutBytes := cmdOutput.Stdout
	stderrBytes := cmdOutput.Stderr

	// Build output
	output := ExecuteCommandOutput{
		ExitCode: exitCode,
		Stdout:   string(stdoutBytes),
		Stderr:   string(stderrBytes),
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	success := exitCode == 0
	message := "Command executed"
	if exitCode == -1 {
		message = "Command timed out"
	} else if exitCode != 0 {
		message = "Command failed with exit code " + string(rune('0'+exitCode))
	}

	return tools.BuildToolResult(success, message, dataMap), nil
}
