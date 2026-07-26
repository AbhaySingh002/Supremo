package file_system

import (
	"context"
	"os"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// RenameFile renames a file or directory.
// This tool exists because coding agents need to rename files when refactoring,
// reorganizing project structure, or fixing naming conventions.
// Claude Code and Cursor use this when users ask to rename files or when
// the agent determines renaming is part of the solution.
// Security: We validate both paths exist, prevent renaming across workspace
// boundaries, and ensure destination doesn't already exist.

type RenameFileInput struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

type RenameFileOutput struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

type RenameFile struct{}

func (t *RenameFile) Name() string {
	return "rename_file"
}

func (t *RenameFile) Description() string {
	return "Renames a file or directory from old_path to new_path. Returns both paths."
}

func (t *RenameFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"old_path": map[string]any{
				"type":        "string",
				"description": "Current absolute path of the file or directory",
			},
			"new_path": map[string]any{
				"type":        "string",
				"description": "New absolute path for the file or directory",
			},
		},
		"required": []string{"old_path", "new_path"},
	}
}

func (t *RenameFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed RenameFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate paths are not empty
	if parsed.OldPath == "" || parsed.NewPath == "" {
		return tools.BuildToolResult(false, "Paths cannot be empty", nil), nil
	}

	// Validate and resolve paths
	oldAbsPath, err := tools.ValidateAndResolvePath(ctx, parsed.OldPath)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to resolve old path: "+err.Error(), nil), nil
	}

	newAbsPath, err := tools.ValidateAndResolvePath(ctx, parsed.NewPath)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to resolve new path: "+err.Error(), nil), nil
	}

	// Check if old path exists
	if _, err := tools.PathExists(oldAbsPath); err != nil {
		return tools.BuildToolResult(false, "Old path does not exist", nil), nil
	}

	// Check if new path already exists
	if _, err := tools.PathExists(newAbsPath); err == nil {
		return tools.BuildToolResult(false, "New path already exists", nil), nil
	}

	// Ensure parent directory of new path exists
	if err := EnsureParentDirectoryExists(newAbsPath); err != nil {
		return tools.BuildToolResult(false, "Parent directory of new path does not exist", nil), nil
	}

	// Rename the file or directory
	err = os.Rename(oldAbsPath, newAbsPath)
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to rename: " + err.Error(),
		}, nil
	}

	// Build output
	output := RenameFileOutput{
		OldPath: oldAbsPath,
		NewPath: newAbsPath,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Renamed successfully", dataMap), nil
}
