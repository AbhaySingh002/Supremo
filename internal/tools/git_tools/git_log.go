package git_tools

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// GitLog shows the commit history of a git repository.
// This tool exists because coding agents need to understand the history of
// changes to track when features were added, bugs were fixed, or to find
// the commit that introduced a change.
// Claude Code and Cursor use this when users ask about commit history or when
// the agent needs to understand the timeline of changes.
// Security: We validate the directory is a git repository and run git commands
// in a controlled environment.

type GitLogInput struct {
	Directory string `json:"directory"`
	Limit     int    `json:"limit"` // number of commits to show (default: 10)
}

type GitLogOutput struct {
	Commits []CommitInfo `json:"commits"`
}

type CommitInfo struct {
	Hash    string `json:"hash"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

type GitLog struct{}

func (t *GitLog) Name() string {
	return "git_log"
}

func (t *GitLog) Description() string {
	return "Shows the git commit history. Returns commit hash, author, date, and message for each commit."
}

func (t *GitLog) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Path to the git repository",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of commits to show (default: 10)",
			},
		},
		"required": []string{"directory"},
	}
}

func (t *GitLog) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed GitLogInput
	if err := ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate directory
	if err := ValidateDirectory(parsed.Directory); err != nil {
		return BuildToolResult(false, "Directory cannot be empty", nil), nil
	}

	// Check if directory is a git repository
	if err := IsGitRepository(parsed.Directory); err != nil {
		return BuildToolResult(false, "Not a git repository", nil), nil
	}

	// Set default limit
	limit := parsed.Limit
	if limit <= 0 {
		limit = 10
	}

	// Get git log with formatted output
	args := []string{"log", "-n", strconv.Itoa(limit), "--pretty=format:%H|%an|%ai|%s"}
	cmd := exec.Command("git", args...)
	cmd.Dir = parsed.Directory

	output, err := cmd.Output()
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to get git log: " + err.Error(),
		}, nil
	}

	// Parse the output
	commits := []CommitInfo{}
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Format: hash|author|date|message
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}

		commits = append(commits, CommitInfo{
			Hash:    parts[0],
			Author:  parts[1],
			Date:    parts[2],
			Message: parts[3],
		})
	}

	// Build output
	outputData := GitLogOutput{
		Commits: commits,
	}

	// Convert output to map for ToolResult
	dataMap, err := SerializeOutput(outputData)
	if err != nil {
		return BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return BuildToolResult(true, "Git log retrieved", dataMap), nil
}
