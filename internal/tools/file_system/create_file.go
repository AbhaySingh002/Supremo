package file_system

import (
	"context"
	"os"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// CreateFile creates a new empty file.
// This tool exists because coding agents need to create new files when
// implementing features, adding tests, or scaffolding project structure.
// Claude Code and Cursor use this when users ask to create new files or
// when the agent determines new files are needed for the solution.
// Security: We validate the path, ensure parent directory exists, prevent
// overwriting existing files, and prevent creating files outside workspace.

type CreateFileInput struct {
	Path string `json:"path"`
}

type CreateFileOutput struct {
	CreatedPath string `json:"created_path"`
}

type CreateFile struct{}

func (t *CreateFile) Name() string {
	return "create_file"
}

func (t *CreateFile) Description() string {
	return "Creates a new empty file. Fails if the file already exists. Returns the created file path."
}

func (t *CreateFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path where the file should be created",
			},
		},
		"required": []string{"path"},
	}
}

func (t *CreateFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed CreateFileInput
	if err := ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate and resolve path
	absPath, err := ValidateAndResolvePath(parsed.Path)
	if err != nil {
		return BuildToolResult(false, "Path cannot be empty or is invalid", nil), nil
	}

	// Ensure parent directory exists
	if err := EnsureParentDirectoryExists(absPath); err != nil {
		return BuildToolResult(false, "Parent directory does not exist", nil), nil
	}

	// Check if file already exists
	if _, err := PathExists(absPath); err == nil {
		return BuildToolResult(false, "File already exists", nil), nil
	}

	// Create the file
	file, err := os.Create(absPath)
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to create file: " + err.Error(),
		}, nil
	}
	file.Close()

	// Build output
	output := CreateFileOutput{
		CreatedPath: absPath,
	}

	// Convert output to map for ToolResult
	dataMap, err := SerializeOutput(output)
	if err != nil {
		return BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return BuildToolResult(true, "File created successfully", dataMap), nil
}
