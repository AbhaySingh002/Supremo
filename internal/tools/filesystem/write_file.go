package filesystem

import (
	"context"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

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

func (t *WriteFile) Capabilities() tools.CapabilitySet { return tools.CapabilityWriteWorkspace }

func (t *WriteFile) Description() string {
	return "Creates a file or overwrites its entire contents. Existing files must be read first; prefer replace_in_file for localized edits."
}

func (t *WriteFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or workspace-relative path to the file to write",
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
	var parsed WriteFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	target, err := ResolveTarget(ctx, parsed.Path)
	if err != nil {
		return recoverableResult("Path cannot be empty or is invalid", nil), nil
	}
	sessionID := tools.ProgressScopeFromContext(ctx).SessionID

	if info, err := tools.PathExists(target.AbsPath); err == nil && info.IsDir() {
		return recoverableResult("Path is a directory, not a file", map[string]any{"path": target.RelPath}), nil
	}

	_, exists, err := CurrentDiskHash(target.AbsPath)
	if err != nil {
		return recoverableResult("Failed to inspect file: "+err.Error(), nil), nil
	}

	intent := MutationIntent{}
	if !exists {
		intent.CreateIfAbsent = true
	} else {
		hash, err := requireTrustedPresent(ctx, sessionID, target, "overwriting")
		if err != nil {
			return SafetyErrorResult(&FileNotObservedError{
				Path:    target.RelPath,
				Message: fmt.Sprintf("File %q exists on disk but was not read in this session. Read the file before overwriting it.", target.RelPath),
			}), nil
		}
		intent.ExpectedHash = hash
	}

	out, err := ValidateAndExecuteMutation(ctx, sessionID, target, "write_file", intent, func(before []byte) ([]byte, error) {
		return []byte(parsed.Content), nil
	})
	if err != nil {
		return SafetyErrorResult(err), nil
	}
	return tools.BuildSerializedToolResult(true, "File written successfully", out), nil
}
