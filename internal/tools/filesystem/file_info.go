package filesystem

import (
	"context"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// FileInfo returns detailed information about a file or directory.
// This tool exists because coding agents need to understand file metadata
// like size, permissions, and modification times to make informed decisions.
// Claude Code and Cursor use this when analyzing project structure or
// determining file characteristics before operations.
// Security: We validate the path exists and prevent accessing files outside
// the allowed workspace.

type FileInfoInput struct {
	Path string `json:"path"`
}

type FileInfoOutput struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Type     string `json:"type"` // "file" or "directory"
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Mode     string `json:"mode"`
	IsHidden bool   `json:"is_hidden"`
}

type FileInfo struct{}

func (t *FileInfo) Name() string {
	return "file_info"
}

func (t *FileInfo) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (t *FileInfo) Description() string {
	return "Returns detailed information about a file or directory including size, type, permissions, and modification time."
}

func (t *FileInfo) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file or directory",
			},
		},
		"required": []string{"path"},
	}
}

func (t *FileInfo) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed FileInfoInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate and resolve path
	absPath, err := tools.ValidateAndResolvePath(ctx, parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Path cannot be empty or is invalid", nil), nil
	}

	// Check if path exists
	info, err := tools.PathExists(absPath)
	if err != nil {
		return tools.BuildToolResult(false, "Path does not exist", nil), nil
	}

	// Determine type
	fileType := "file"
	if info.IsDir() {
		fileType = "directory"
	}

	// Check if hidden
	baseName := filepath.Base(absPath)
	isHidden := tools.IsHidden(baseName)

	// Build output
	output := FileInfoOutput{
		Path:     absPath,
		Name:     baseName,
		Type:     fileType,
		Size:     info.Size(),
		Modified: info.ModTime().Unix(),
		Mode:     info.Mode().String(),
		IsHidden: isHidden,
	}

	return tools.BuildSerializedToolResult(true, "File info retrieved successfully", output), nil
}
