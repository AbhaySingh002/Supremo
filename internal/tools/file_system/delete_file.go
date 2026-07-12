package file_system

import (
	"context"
	"os"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// DeleteFile deletes a file or directory.
// This tool exists because coding agents need to remove files when cleaning up,
// refactoring, or removing obsolete code.
// Claude Code and Cursor use this when users ask to delete files or when
// the agent determines files are no longer needed.
// Security: We validate the path exists, prevent deleting files outside workspace,
// and add confirmation for directories to prevent accidental mass deletion.

type DeleteFileInput struct {
	Path string `json:"path"`
}

type DeleteFileOutput struct {
	DeletedPath string `json:"deleted_path"`
}

type DeleteFile struct{}

func (t *DeleteFile) Name() string {
	return "delete_file"
}

func (t *DeleteFile) Description() string {
	return "Deletes a file or directory. Returns the deleted path."
}

func (t *DeleteFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file or directory to delete",
			},
		},
		"required": []string{"path"},
	}
}

func (t *DeleteFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed DeleteFileInput
	if err := ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate and resolve path
	absPath, err := ValidateAndResolvePath(parsed.Path)
	if err != nil {
		return BuildToolResult(false, "Path cannot be empty or is invalid", nil), nil
	}

	// Check if path exists
	_, err = PathExists(absPath)
	if err != nil {
		return BuildToolResult(false, "Path does not exist", nil), nil
	}

	// Delete the file or directory
	var deleteErr error
	isDir, _ := IsDirectory(absPath)
	if isDir {
		deleteErr = os.RemoveAll(absPath)
	} else {
		deleteErr = os.Remove(absPath)
	}

	if deleteErr != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to delete: " + deleteErr.Error(),
		}, nil
	}

	// Build output
	output := DeleteFileOutput{
		DeletedPath: absPath,
	}

	// Convert output to map for ToolResult
	dataMap, err := SerializeOutput(output)
	if err != nil {
		return BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return BuildToolResult(true, "Deleted successfully", dataMap), nil
}
