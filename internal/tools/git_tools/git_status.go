package git_tools

import (
	"context"
	"os/exec"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// GitStatus shows the working tree status of a git repository.
// This tool exists because coding agents need to understand what files
// have changed, been added, or are untracked to make informed decisions.
// Claude Code and Cursor use this when users ask about git status or when
// the agent needs to understand the current state of the repository.
// Security: We validate the directory is a git repository and run git commands
// in a controlled environment.

type GitStatusInput struct {
	Directory string `json:"directory"`
}

type GitStatusOutput struct {
	Branch    string       `json:"branch"`
	Staged    []FileStatus `json:"staged"`
	Modified  []FileStatus `json:"modified"`
	Untracked []FileStatus `json:"untracked"`
}

type FileStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "added", "modified", "deleted", "renamed", "untracked"
}

type GitStatus struct{}

func (t *GitStatus) Name() string {
	return "git_status"
}

func (t *GitStatus) Description() string {
	return "Shows the git working tree status including branch, staged, modified, and untracked files."
}

func (t *GitStatus) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Path to the git repository",
			},
		},
		"required": []string{"directory"},
	}
}

func (t *GitStatus) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed GitStatusInput
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

	// Get current branch
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = parsed.Directory
	branchBytes, err := cmd.Output()
	branch := strings.TrimSpace(string(branchBytes))
	if err != nil {
		branch = "HEAD" // Detached HEAD state
	}

	// Get git status in porcelain format
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = parsed.Directory
	output, err := cmd.Output()
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Failed to get git status: " + err.Error(),
		}, nil
	}

	// Parse the output
	staged := []FileStatus{}
	modified := []FileStatus{}
	untracked := []FileStatus{}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		// Git status porcelain format: XY path
		// X = staged status, Y = working tree status
		if len(line) < 3 {
			continue
		}

		stagedStatus := string(line[0])
		workStatus := string(line[1])
		path := strings.TrimSpace(line[3:])

		// Determine file status
		if stagedStatus == "?" {
			// Untracked
			untracked = append(untracked, FileStatus{
				Path:   path,
				Status: "untracked",
			})
		} else if stagedStatus != " " && stagedStatus != "?" {
			// Staged changes
			status := "added"
			if stagedStatus == "M" {
				status = "modified"
			} else if stagedStatus == "D" {
				status = "deleted"
			} else if stagedStatus == "R" {
				status = "renamed"
			}
			staged = append(staged, FileStatus{
				Path:   path,
				Status: status,
			})
		}

		// Working tree changes
		if workStatus != " " && workStatus != "?" {
			status := "modified"
			if workStatus == "D" {
				status = "deleted"
			} else if workStatus == "R" {
				status = "renamed"
			}
			modified = append(modified, FileStatus{
				Path:   path,
				Status: status,
			})
		}
	}

	// Build output
	outputData := GitStatusOutput{
		Branch:    branch,
		Staged:    staged,
		Modified:  modified,
		Untracked: untracked,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(outputData)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Git status retrieved", dataMap), nil
}
