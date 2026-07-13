package git_tools

import (
	"context"
	"os/exec"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// GitDiff shows changes between commits, branches, or the working tree.
// This tool exists because coding agents need to see what has changed in files
// to understand the impact of modifications and review code changes.
// Claude Code and Cursor use this when users ask to see diffs or when the
// agent needs to review changes before suggesting edits.
// Security: We validate the directory is a git repository and run git commands
// in a controlled environment.

type GitDiffInput struct {
	Directory string `json:"directory"`
	Target    string `json:"target"` // target commit/branch (empty for working tree vs staged)
	File      string `json:"file"`   // specific file to diff (empty for all)
}

type GitDiffOutput struct {
	Diff string `json:"diff"`
}

type GitDiff struct{}

func (t *GitDiff) Name() string {
	return "git_diff"
}

func (t *GitDiff) Description() string {
	return "Shows git diff between commits, branches, or working tree. Returns the diff output."
}

func (t *GitDiff) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Path to the git repository",
			},
			"target": map[string]any{
				"type":        "string",
				"description": "Target commit or branch to compare against (empty for working tree vs staged)",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "Specific file to diff (empty for all files)",
			},
		},
		"required": []string{"directory"},
	}
}

func (t *GitDiff) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed GitDiffInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate directory
	if err := tools.ValidateDirectory(parsed.Directory); err != nil {
		return tools.BuildToolResult(false, "Directory cannot be empty", nil), nil
	}

	// Check if directory is a git repository
	if err := IsGitRepository(parsed.Directory); err != nil {
		return tools.BuildToolResult(false, "Not a git repository", nil), nil
	}

	// Build git diff command
	args := []string{"diff"}

	if parsed.Target != "" {
		args = append(args, parsed.Target)
	}

	if parsed.File != "" {
		args = append(args, "--", parsed.File)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = parsed.Directory

	output, err := cmd.Output()
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to get git diff: " + err.Error(),
		}, nil
	}

	// Build output
	outputData := GitDiffOutput{
		Diff: string(output),
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(outputData)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Git diff retrieved", dataMap), nil
}
