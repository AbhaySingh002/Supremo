package filesystem

import (
	"context"
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// CreateDirectory creates a new directory.
// This tool exists because coding agents need to create directories when
// organizing project structure, adding new modules, or scaffolding.
// Claude Code and Cursor use this when users ask to create folders or when
// the agent determines new directories are needed for the solution.
// Security: We validate the path, ensure parent directory exists, prevent
// overwriting existing directories, and prevent creating directories outside workspace.

type CreateDirectoryInput struct {
	Path string `json:"path"`
}

type CreateDirectoryOutput struct {
	CreatedPath string `json:"created_path"`
}

type CreateDirectory struct{}

func (t *CreateDirectory) Name() string {
	return "create_directory"
}

func (t *CreateDirectory) Capabilities() tools.CapabilitySet { return tools.CapabilityWriteWorkspace }

func (t *CreateDirectory) Description() string {
	return "Creates a new directory. Fails if the directory already exists. Returns the created directory path."
}

func (t *CreateDirectory) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path where the directory should be created",
			},
		},
		"required": []string{"path"},
	}
}

func (t *CreateDirectory) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed CreateDirectoryInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate and resolve path
	absPath, err := tools.ValidateAndResolvePath(ctx, parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Path cannot be empty or is invalid", nil), nil
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(absPath)
	if parentDir != absPath { // Don't check if parent is root
		if err := EnsureParentDirectoryExists(absPath); err != nil {
			return tools.BuildToolResult(false, "Parent directory does not exist", nil), nil
		}
	}

	// Check if directory already exists
	if _, err := tools.PathExists(absPath); err == nil {
		return tools.BuildToolResult(false, "Directory already exists", nil), nil
	}

	// Create the directory
	err = os.Mkdir(absPath, 0755)
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to create directory: " + err.Error(),
		}, nil
	}

	// Build output
	output := CreateDirectoryOutput{
		CreatedPath: absPath,
	}

	return tools.BuildSerializedToolResult(true, "Directory created successfully", output), nil
}
