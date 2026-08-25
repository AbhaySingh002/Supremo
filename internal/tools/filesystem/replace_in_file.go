package filesystem

import (
	"context"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

type ReplaceInFileInput struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type ReplaceInFile struct{}

func (t *ReplaceInFile) Name() string { return "replace_in_file" }

func (t *ReplaceInFile) Capabilities() tools.CapabilitySet { return tools.CapabilityWriteWorkspace }

func (t *ReplaceInFile) Description() string {
	return "Replaces unique old_string with new_string in a file. The file must be read first. Fails distinctly on 0 matches, multiple matches (unless replace_all), or stale content. Prefer this over write_file for localized edits."
}

func (t *ReplaceInFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":        map[string]any{"type": "string", "description": "File to edit"},
			"old_string":  map[string]any{"type": "string", "description": "Exact text to replace"},
			"new_string":  map[string]any{"type": "string", "description": "Replacement text"},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence (default false; multiple matches otherwise fail)"},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

func (t *ReplaceInFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var parsed ReplaceInFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	if parsed.OldString == "" {
		return recoverableResult("old_string cannot be empty", nil), nil
	}
	target, err := ResolveTarget(ctx, parsed.Path)
	if err != nil {
		return recoverableResult("Path cannot be empty or is invalid", nil), nil
	}
	sessionID := tools.ProgressScopeFromContext(ctx).SessionID

	expectedHash, err := requireTrustedPresent(ctx, sessionID, target, "editing")
	if err != nil {
		return SafetyErrorResult(err), nil
	}

	var semanticErr *tools.ToolResult
	out, err := ValidateAndExecuteMutation(ctx, sessionID, target, "replace_in_file", MutationIntent{ExpectedHash: expectedHash}, func(before []byte) ([]byte, error) {
		content := string(before)
		count := strings.Count(content, parsed.OldString)
		if count == 0 {
			excerpt := formatNumberedLines(strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n"), 1)
			if len(excerpt) > 2000 {
				excerpt = excerpt[:2000] + "\n…"
			}
			semanticErr = &tools.ToolResult{
				Success:   false,
				Status:    tools.ToolStatusFailed,
				Retryable: true,
				Message:   "old_string not found (0 matches)",
				Error:     &tools.ToolError{Class: "recoverable", Message: "old_string not found (0 matches)"},
				Data:      map[string]any{"path": target.RelPath, "matches": 0, "excerpt": excerpt},
			}
			return nil, fmt.Errorf("old_string not found (0 matches)")
		}
		if count > 1 && !parsed.ReplaceAll {
			semanticErr = &tools.ToolResult{
				Success:   false,
				Status:    tools.ToolStatusFailed,
				Retryable: true,
				Message:   fmt.Sprintf("old_string matched %d times; pass replace_all or a unique snippet", count),
				Error:     &tools.ToolError{Class: "recoverable", Message: "multiple matches"},
				Data:      map[string]any{"path": target.RelPath, "matches": count},
			}
			return nil, fmt.Errorf("multiple matches")
		}
		if parsed.ReplaceAll {
			return []byte(strings.ReplaceAll(content, parsed.OldString, parsed.NewString)), nil
		}
		return []byte(strings.Replace(content, parsed.OldString, parsed.NewString, 1)), nil
	})

	if semanticErr != nil {
		return semanticErr, nil
	}
	if err != nil {
		return SafetyErrorResult(err), nil
	}
	return tools.BuildSerializedToolResult(true, "File updated", out), nil
}
