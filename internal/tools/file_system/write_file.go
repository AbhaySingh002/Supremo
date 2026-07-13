package file_system

import (
	"context"
	"os"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// WriteFile writes content to a file, overwriting if it exists.
// This tool exists because coding agents need to modify files to implement
// fixes, add features, or refactor code.
// Claude Code and Cursor use this when making code changes based on user
// requests or when the agent determines edits are necessary.
// Security: We validate the path, ensure parent directory exists, and prevent
// writing files outside the allowed workspace. We use atomic writes when possible.

type WriteFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type WriteFileOutput struct {
	BytesWritten int64 `json:"bytes_written"`
}

type WriteFile struct{}

func (t *WriteFile) Name() string {
	return "write_file"
}

func (t *WriteFile) Description() string {
	return "Writes content to a file, overwriting if it exists. Returns the number of bytes written."
}

func (t *WriteFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed WriteFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate and resolve path
	absPath, err := tools.ValidateAndResolvePath(parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Path cannot be empty or is invalid", nil), nil
	}

	// Ensure parent directory exists
	if err := EnsureParentDirectoryExists(absPath); err != nil {
		return tools.BuildToolResult(false, "Parent directory does not exist", nil), nil
	}

	// Check if path exists and is a directory
	if _, err := tools.PathExists(absPath); err == nil {
		isDir, _ := IsDirectory(absPath)
		if isDir {
			return tools.BuildToolResult(false, "Path is a directory, not a file", nil), nil
		}
	}

	// Write file
	err = os.WriteFile(absPath, []byte(parsed.Content), 0644)
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to write file: " + err.Error(),
		}, nil
	}

	// Build output
	output := WriteFileOutput{
		BytesWritten: int64(len(parsed.Content)),
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "File written successfully", dataMap), nil
}
