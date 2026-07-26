package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// RunBuild builds the project using the appropriate build tool.
// This tool exists because coding agents need to build projects to verify
// changes compile correctly and to produce deployable artifacts.
// Claude Code and Cursor use this when users ask to build the project or
// when the agent needs to verify code compiles before suggesting changes.
// Security: We detect the project type, use appropriate build commands,
// limit execution time, and run in a controlled environment.

type RunBuildInput struct {
	Directory string   `json:"directory"`
	Args      []string `json:"args"`
	Timeout   int      `json:"timeout"` // timeout in seconds
}

type RunBuildOutput struct {
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	BuildTool       string `json:"build_tool"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

type RunBuild struct{}

func (t *RunBuild) Name() string {
	return "run_build"
}

func (t *RunBuild) Description() string {
	return "Builds the project with the detected tool and executes repository code automatically. Returns bounded stdout and stderr."
}

func (t *RunBuild) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Project directory to build in",
			},
			"args": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Additional arguments to pass to the build command",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default: 120)",
			},
		},
		"required": []string{"directory"},
	}
}

func (t *RunBuild) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed RunBuildInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate directory
	directory, err := tools.ValidateDirectory(ctx, parsed.Directory)
	if err != nil {
		return tools.BuildToolResult(false, "Directory cannot be empty", nil), nil
	}
	parsed.Directory = directory

	// Detect build tool based on project files
	buildTool := detectBuildTool(parsed.Directory)
	if buildTool == "" {
		return &tools.ToolResult{
			Success: false,
			Message: "Could not detect build tool. Supported: go, npm, cargo, make",
		}, nil
	}

	// Build command based on tool
	var command string
	var args []string

	switch buildTool {
	case "go":
		command = "go"
		args = []string{"build", "./..."}
		if len(parsed.Args) > 0 {
			args = append(args, parsed.Args...)
		}
	case "npm":
		command = "npm"
		args = []string{"run", "build"}
		if len(parsed.Args) > 0 {
			args = append(args, parsed.Args...)
		}
	case "cargo":
		command = "cargo"
		args = []string{"build"}
		if len(parsed.Args) > 0 {
			args = append(args, parsed.Args...)
		}
	case "make":
		command = "make"
		args = parsed.Args
		if len(args) == 0 {
			args = []string{}
		}
	}

	// Set default timeout
	timeout := 120 * time.Second
	if parsed.Timeout > 0 {
		timeout = time.Duration(parsed.Timeout) * time.Second
	}

	// Create command with context for timeout
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, command, args...)
	cmd.Dir = parsed.Directory

	// Execute command and capture output
	cmdOutput, err := ExecuteCommandWithOutput(cmdCtx, cmd)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to execute command: "+err.Error(), nil), nil
	}

	exitCode := cmdOutput.ExitCode
	// Build output
	output := RunBuildOutput{
		ExitCode:        exitCode,
		Stdout:          string(cmdOutput.Stdout),
		Stderr:          string(cmdOutput.Stderr),
		BuildTool:       buildTool,
		StdoutTruncated: cmdOutput.StdoutTruncated,
		StderrTruncated: cmdOutput.StderrTruncated,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	success := exitCode == 0
	message := "Build succeeded"
	if cmdOutput.TimedOut {
		message = "Build timed out"
	} else if cmdOutput.Canceled {
		message = "Build canceled"
	} else if exitCode != 0 {
		message = "Build failed"
	}

	return tools.BuildToolResult(success, message, dataMap), nil
}

// detectBuildTool detects the build tool based on project files
func detectBuildTool(dir string) string {
	// Check for Go project
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go"
	}

	// Check for Node.js project
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return "npm"
	}

	// Check for Rust project
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "cargo"
	}

	// Check for Makefile
	if _, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil {
		return "make"
	}

	return ""
}
