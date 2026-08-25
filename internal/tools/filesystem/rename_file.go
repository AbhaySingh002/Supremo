package filesystem

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

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

func (t *RenameFile) Capabilities() tools.CapabilitySet { return tools.CapabilityWriteWorkspace }

func (t *RenameFile) Description() string {
	return "Renames a file. The source must be read first. Fails if the destination exists. Directories cannot be renamed."
}

func (t *RenameFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"old_path": map[string]any{
				"type":        "string",
				"description": "Current absolute or workspace-relative path of the file",
			},
			"new_path": map[string]any{
				"type":        "string",
				"description": "New absolute or workspace-relative path for the file",
			},
		},
		"required": []string{"old_path", "new_path"},
	}
}

func (t *RenameFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var parsed RenameFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	if parsed.OldPath == "" || parsed.NewPath == "" {
		return recoverableResult("Paths cannot be empty", nil), nil
	}

	oldTarget, err := ResolveTarget(ctx, parsed.OldPath)
	if err != nil {
		return recoverableResult("Failed to resolve old path: "+err.Error(), nil), nil
	}
	newTarget, err := ResolveTarget(ctx, parsed.NewPath)
	if err != nil {
		return recoverableResult("Failed to resolve new path: "+err.Error(), nil), nil
	}
	sessionID := tools.ProgressScopeFromContext(ctx).SessionID

	oldInfo, err := tools.PathExists(oldTarget.AbsPath)
	if err != nil {
		return recoverableResult("Old path does not exist", map[string]any{"path": oldTarget.RelPath}), nil
	}
	if oldInfo.IsDir() {
		return directoryMutationUnsupported(oldTarget.RelPath, "renamed"), nil
	}

	expectedHash, err := requireTrustedPresent(ctx, sessionID, oldTarget, "renaming")
	if err != nil {
		return SafetyErrorResult(err), nil
	}
	if err := ValidateAndExecuteRename(ctx, sessionID, oldTarget, newTarget, expectedHash, "rename_file"); err != nil {
		return SafetyErrorResult(err), nil
	}
	return tools.BuildSerializedToolResult(true, "Renamed successfully", RenameFileOutput{OldPath: oldTarget.AbsPath, NewPath: newTarget.AbsPath}), nil
}
