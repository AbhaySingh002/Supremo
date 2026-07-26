package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// RunTests runs project tests using the appropriate test framework.
// This tool exists because coding agents need to run tests to verify
// changes don't break existing functionality and to validate new features.
// Claude Code and Cursor use this when users ask to run tests or when
// the agent needs to verify changes before suggesting them.
// Security: We detect the project type, use appropriate test commands,
// limit execution time, and run in a controlled environment.

type RunTestsInput struct {
	Directory string   `json:"directory"`
	Args      []string `json:"args"`
	Timeout   int      `json:"timeout"` // timeout in seconds
}

type RunTestsOutput struct {
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	TestFramework   string `json:"test_framework"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

type RunTests struct{}

func (t *RunTests) Name() string {
	return "run_tests"
}

func (t *RunTests) Description() string {
	return "Runs project tests with the detected framework and executes repository code automatically. Returns bounded stdout and stderr."
}

func (t *RunTests) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Project directory to run tests in",
			},
			"args": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Additional arguments to pass to the test command",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default: 60)",
			},
		},
		"required": []string{"directory"},
	}
}

func (t *RunTests) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed RunTestsInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate directory
	directory, err := tools.ValidateDirectory(ctx, parsed.Directory)
	if err != nil {
		return tools.BuildToolResult(false, "Directory cannot be empty", nil), nil
	}
	parsed.Directory = directory

	// Detect test framework based on project files
	testFramework := detectTestFramework(parsed.Directory)
	if testFramework == "" {
		return &tools.ToolResult{
			Success: false,
			Message: "Could not detect test framework. Supported: go, npm, pytest, cargo",
		}, nil
	}

	// Build command based on framework
	var command string
	var args []string

	switch testFramework {
	case "go":
		command = "go"
		args = []string{"test", "./..."}
		if len(parsed.Args) > 0 {
			args = append(args, parsed.Args...)
		}
	case "npm":
		command = "npm"
		args = []string{"test"}
		if len(parsed.Args) > 0 {
			args = append(args, parsed.Args...)
		}
	case "pytest":
		command = "pytest"
		args = parsed.Args
		if len(args) == 0 {
			args = []string{}
		}
	case "cargo":
		command = "cargo"
		args = []string{"test"}
		if len(parsed.Args) > 0 {
			args = append(args, parsed.Args...)
		}
	}

	// Set default timeout
	timeout := 60 * time.Second
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
	output := RunTestsOutput{
		ExitCode:        exitCode,
		Stdout:          string(cmdOutput.Stdout),
		Stderr:          string(cmdOutput.Stderr),
		TestFramework:   testFramework,
		StdoutTruncated: cmdOutput.StdoutTruncated,
		StderrTruncated: cmdOutput.StderrTruncated,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	success := exitCode == 0
	message := "Tests passed"
	if cmdOutput.TimedOut {
		message = "Tests timed out"
	} else if cmdOutput.Canceled {
		message = "Tests canceled"
	} else if exitCode != 0 {
		message = "Tests failed"
	}

	return tools.BuildToolResult(success, message, dataMap), nil
}

// detectTestFramework detects the test framework based on project files
func detectTestFramework(dir string) string {
	// Check for Go project
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go"
	}

	// Check for Node.js project
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		return "npm"
	}

	// Check for Python project with pytest
	if _, err := os.Stat(filepath.Join(dir, "pytest.ini")); err == nil {
		return "pytest"
	}
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		content, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
		if strings.Contains(string(content), "pytest") {
			return "pytest"
		}
	}

	// Check for Rust project
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "cargo"
	}

	return ""
}
