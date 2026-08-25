package filesystem

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

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

func (t *DeleteFile) Capabilities() tools.CapabilitySet { return tools.CapabilityWriteWorkspace }

func (t *DeleteFile) Description() string {
	return "Deletes a file. The file must be read first. Directories cannot be deleted."
}

func (t *DeleteFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or workspace-relative path to the file to delete",
			},
		},
		"required": []string{"path"},
	}
}

func (t *DeleteFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var parsed DeleteFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	target, err := ResolveTarget(ctx, parsed.Path)
	if err != nil {
		return recoverableResult("Path cannot be empty or is invalid", nil), nil
	}
	sessionID := tools.ProgressScopeFromContext(ctx).SessionID

	info, err := tools.PathExists(target.AbsPath)
	if err != nil {
		return recoverableResult("Path does not exist", map[string]any{"path": target.RelPath}), nil
	}
	if info.IsDir() {
		return directoryMutationUnsupported(target.RelPath, "deleted"), nil
	}

	expectedHash, err := requireTrustedPresent(ctx, sessionID, target, "deletion")
	if err != nil {
		return SafetyErrorResult(err), nil
	}

	_, err = ValidateAndExecuteMutation(ctx, sessionID, target, "delete_file", MutationIntent{ExpectedHash: expectedHash, Delete: true}, nil)
	if err != nil {
		return SafetyErrorResult(err), nil
	}
	return tools.BuildSerializedToolResult(true, "Deleted successfully", DeleteFileOutput{DeletedPath: target.AbsPath}), nil
}
