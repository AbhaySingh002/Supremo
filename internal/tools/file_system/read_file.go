package file_system

import (
	"context"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ReadFile reads the contents of a file.
// This tool exists because coding agents need to read source code to understand
// the codebase, analyze bugs, and make informed changes.
// Claude Code and Cursor use this extensively when users ask questions about
// specific files or when the agent needs to examine code before making edits.
// Security: We validate the path exists, ensure it's a file (not directory),
// and prevent reading files outside the allowed workspace.

type ReadFileInput struct {
	Path string `json:"path"`
}

type ReadFileOutput struct {
	Content string `json:"content"`
	Size    int64  `json:"size"`
}

type ReadFile struct{}

func (t *ReadFile) Name() string {
	return "read_file"
}

func (t *ReadFile) Description() string {
	return "Reads the contents of a file. Returns the file content and size in bytes."
}

func (t *ReadFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to read",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed ReadFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate and resolve path
	absPath, err := tools.ValidateAndResolvePath(ctx, parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Path cannot be empty or is invalid", nil), nil
	}

	// Check if path exists and is a file
	info, err := tools.PathExists(absPath)
	if err != nil {
		return tools.BuildToolResult(false, "File does not exist", nil), nil
	}

	if info.IsDir() {
		return tools.BuildToolResult(false, "Path is a directory, not a file", nil), nil
	}

	// Read file content
	if info.Size() > tools.MaxFileBytes {
		return tools.BuildToolResult(false, "File exceeds 1 MiB read limit", nil), nil
	}
	content, err := tools.ReadLimitedFile(absPath, tools.MaxFileBytes)
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to read file: " + err.Error(),
		}, nil
	}

	// Build output
	output := ReadFileOutput{
		Content: string(content),
		Size:    info.Size(),
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "File read successfully", dataMap), nil
}
