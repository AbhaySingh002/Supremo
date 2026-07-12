package search

import (
	"context"
	"encoding/json"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ListOpenFiles lists files currently open in the IDE.
// This tool exists because coding agents need to know which files are
// currently open to understand the user's context and focus.
// Claude Code and Cursor use this to track the user's working set and
// provide context-aware suggestions.
// Security: This tool requires IDE integration. The actual implementation
// would communicate with the IDE's API to get the list of open files.
// Note: This is a placeholder implementation. Real integration requires
// IDE-specific APIs (LSP, VS Code extension API, etc.).

type ListOpenFilesInput struct {
	// No input parameters needed - this queries IDE state
}

type ListOpenFilesOutput struct {
	Files []OpenFileInfo `json:"files"`
}

type OpenFileInfo struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Active   bool   `json:"active"` // whether this is the currently active file
}

type ListOpenFiles struct{}

func (t *ListOpenFiles) Name() string {
	return "list_open_files"
}

func (t *ListOpenFiles) Description() string {
	return "Lists files currently open in the IDE. Returns file path, language, and active status for each file."
}

func (t *ListOpenFiles) Schema() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListOpenFiles) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input (empty for this tool)
	var parsed ListOpenFilesInput
	inputBytes, err := json.Marshal(input)
	if err != nil {
		return nil, tools.ErrInvalidInput
	}
	if err := json.Unmarshal(inputBytes, &parsed); err != nil {
		return nil, tools.ErrInvalidInput
	}

	// Placeholder: In a real implementation, this would query the IDE's API
	// to get the list of open files. For now, return an empty list with a note.

	// Build output
	output := ListOpenFilesOutput{
		Files: []OpenFileInfo{},
	}

	// Convert output to map for ToolResult
	outputMap, err := json.Marshal(output)
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to serialize output: " + err.Error(),
		}, nil
	}

	var dataMap map[string]interface{}
	if err := json.Unmarshal(outputMap, &dataMap); err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to convert output: " + err.Error(),
		}, nil
	}

	return &tools.ToolResult{
		Success: true,
		Message: "Open files list retrieved (IDE integration required for actual data)",
		Data:    dataMap,
	}, nil
}
