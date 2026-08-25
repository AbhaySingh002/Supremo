package terminal

import (
	"context"
	"os/exec"
	"sort"
	"strconv"
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
// It is intentionally not a sandbox; the manager requires explicit approval.

type ExecuteCommandInput struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Directory   string            `json:"directory"`
	Timeout     int               `json:"timeout"` // timeout in seconds
	Environment map[string]string `json:"environment,omitempty"`
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

func (t *ExecuteCommand) Capabilities() tools.CapabilitySet { return tools.CapabilityExecuteProcess }

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
			"environment": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
				"description":          "Environment variables to add or override for this command",
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
	if len(parsed.Environment) > 0 {
		keys := make([]string, 0, len(parsed.Environment))
		for key, value := range parsed.Environment {
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
				return tools.BuildToolResult(false, "Environment contains an invalid name or value", nil), nil
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		cmd.Env = cmd.Environ()
		for _, key := range keys {
			cmd.Env = append(cmd.Env, key+"="+parsed.Environment[key])
		}
	}

	// Execute command and capture output
	cmdOutput, err := ExecuteCommandWithOutput(cmdCtx, cmd)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to execute command: "+err.Error(), nil), nil
	}

	exitCode := cmdOutput.ExitCode
	output := diagnoseCommand(parsed.Command, parsed.Args, cmdOutput)
	success := exitCode == 0
	message := "Command executed"
	if cmdOutput.TimedOut {
		message = "Command timed out"
	} else if cmdOutput.Canceled {
		message = "Command canceled"
	} else if exitCode != 0 {
		message = "Command failed with exit code " + strconv.Itoa(exitCode)
	}

	return tools.BuildSerializedToolResult(success, message, output), nil
}
