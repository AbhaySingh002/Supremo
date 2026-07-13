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

// RunFormatter runs code formatters on the project.
// This tool exists because coding agents need to format code to maintain
// consistent style and adhere to project formatting standards.
// Claude Code and Cursor use this when users ask to format code or when
// the agent needs to ensure code is properly formatted before committing.
// Security: We detect the project type, use appropriate formatters,
// limit execution time, and run in a controlled environment.

type RunFormatterInput struct {
	Directory string   `json:"directory"`
	Files     []string `json:"files"`   // specific files to format (empty for all)
	Timeout   int      `json:"timeout"` // timeout in seconds
}

type RunFormatterOutput struct {
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Formatter string `json:"formatter"`
}

type RunFormatter struct{}

func (t *RunFormatter) Name() string {
	return "run_formatter"
}

func (t *RunFormatter) Description() string {
	return "Runs code formatter on the project using the detected formatter (go fmt, prettier, black, etc). Returns exit code, stdout, and stderr."
}

func (t *RunFormatter) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Project directory to format",
			},
			"files": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Specific files to format (empty for all files)",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default: 30)",
			},
		},
		"required": []string{"directory"},
	}
}

func (t *RunFormatter) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed RunFormatterInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate directory
	if err := tools.ValidateDirectory(parsed.Directory); err != nil {
		return tools.BuildToolResult(false, "Directory cannot be empty", nil), nil
	}

	// Detect formatter based on project files
	formatter := detectFormatter(parsed.Directory)
	if formatter == "" {
		return &tools.ToolResult{
			Success: false,
			Message: "Could not detect formatter. Supported: go fmt, prettier, black, rustfmt",
		}, nil
	}

	// Build command based on formatter
	var command string
	var args []string

	switch formatter {
	case "go fmt":
		command = "go"
		args = []string{"fmt", "./..."}
	case "prettier":
		command = "npx"
		args = []string{"prettier", "--write", "."}
		if len(parsed.Files) > 0 {
			args = []string{"prettier", "--write"}
			args = append(args, parsed.Files...)
		}
	case "black":
		command = "black"
		args = []string{"."}
		if len(parsed.Files) > 0 {
			args = parsed.Files
		}
	case "rustfmt":
		command = "cargo"
		args = []string{"fmt"}
	}

	// Set default timeout
	timeout := 30 * time.Second
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
	stdoutBytes := cmdOutput.Stdout
	stderrBytes := cmdOutput.Stderr

	// Build output
	output := RunFormatterOutput{
		ExitCode:  exitCode,
		Stdout:    string(stdoutBytes),
		Stderr:    string(stderrBytes),
		Formatter: formatter,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	success := exitCode == 0
	message := "Formatting completed"
	if exitCode == -1 {
		message = "Formatting timed out"
	} else if exitCode != 0 {
		message = "Formatting failed"
	}

	return tools.BuildToolResult(success, message, dataMap), nil
}

// detectFormatter detects the formatter based on project files
func detectFormatter(dir string) string {
	// Check for Go project
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return "go fmt"
	}

	// Check for Node.js project with prettier
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		// Check if prettier is in package.json or .prettierrc exists
		if _, err := os.Stat(filepath.Join(dir, ".prettierrc")); err == nil {
			return "prettier"
		}
		if _, err := os.Stat(filepath.Join(dir, ".prettierrc.json")); err == nil {
			return "prettier"
		}
		if _, err := os.Stat(filepath.Join(dir, ".prettierrc.js")); err == nil {
			return "prettier"
		}
		// Check package.json for prettier
		content, _ := os.ReadFile(filepath.Join(dir, "package.json"))
		if strings.Contains(string(content), "prettier") {
			return "prettier"
		}
	}

	// Check for Python project with black
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		content, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
		if strings.Contains(string(content), "black") {
			return "black"
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".black")); err == nil {
		return "black"
	}

	// Check for Rust project
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return "rustfmt"
	}

	return ""
}
