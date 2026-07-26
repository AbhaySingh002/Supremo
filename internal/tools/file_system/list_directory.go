package file_system

import (
	"context"
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ListDirectory lists files and directories in a given path.
// This tool exists because coding agents need to explore the project structure
// to understand codebase organization and find relevant files.
// Claude Code and Cursor use this to navigate directories when users ask about
// project structure or when the agent needs to locate specific files.
// Security: We validate the path is within the allowed workspace and prevent
// directory traversal attacks by resolving absolute paths.

type ListDirectoryInput struct {
	Path string `json:"path"`
}

type ListDirectoryOutput struct {
	Entries []DirEntry `json:"entries"`
}

type DirEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Type     string `json:"type"` // "file" or "directory"
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

type ListDirectory struct{}

func (t *ListDirectory) Name() string {
	return "list_directory"
}

func (t *ListDirectory) Description() string {
	return "Lists files and directories in a given path. Returns name, path, type, size, and modification time for each entry."
}

func (t *ListDirectory) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the directory to list",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ListDirectory) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed ListDirectoryInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate and resolve path
	absPath, err := tools.ValidateAndResolvePath(ctx, parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Path cannot be empty or is invalid", nil), nil
	}

	// Check if path exists and is a directory
	_, err = tools.PathExists(absPath)
	if err != nil {
		return tools.BuildToolResult(false, "Path does not exist", nil), nil
	}

	isDir, err := IsDirectory(absPath)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to check path type: "+err.Error(), nil), nil
	}
	if !isDir {
		return tools.BuildToolResult(false, "Path is not a directory", nil), nil
	}

	// Read directory entries
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to read directory: " + err.Error(),
		}, nil
	}

	// Build output
	output := ListDirectoryOutput{
		Entries: make([]DirEntry, 0, len(entries)),
	}

	for _, entry := range entries {
		entryPath := filepath.Join(absPath, entry.Name())
		entryInfo, err := entry.Info()
		if err != nil {
			// Skip entries we can't get info for
			continue
		}

		entryType := "file"
		if entry.IsDir() {
			entryType = "directory"
		}

		output.Entries = append(output.Entries, DirEntry{
			Name:     entry.Name(),
			Path:     entryPath,
			Type:     entryType,
			Size:     entryInfo.Size(),
			Modified: entryInfo.ModTime().Unix(),
		})
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Directory listed successfully", dataMap), nil
}
