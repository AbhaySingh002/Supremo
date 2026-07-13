package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// SearchText searches for text patterns within files in a directory.
// This tool exists because coding agents need to find specific code patterns,
// function calls, or text across the codebase to understand dependencies and
// make informed changes.
// Claude Code and Cursor use this when users ask to find where something is
// used or when the agent needs to locate all occurrences of a pattern.
// Security: We validate the search path is within workspace and limit search
// depth to prevent excessive resource usage.

type SearchTextInput struct {
	Path          string `json:"path"`
	Pattern       string `json:"pattern"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxDepth      int    `json:"max_depth"`
}

type SearchTextOutput struct {
	Matches []TextMatch `json:"matches"`
}

type TextMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

type SearchText struct{}

func (t *SearchText) Name() string {
	return "search_text"
}

func (t *SearchText) Description() string {
	return "Searches for text patterns within files in a directory. Returns file path, line number, and matching content."
}

func (t *SearchText) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the directory to search in",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Text pattern to search for",
			},
			"case_sensitive": map[string]any{
				"type":        "boolean",
				"description": "Whether the search should be case sensitive",
			},
			"max_depth": map[string]any{
				"type":        "integer",
				"description": "Maximum directory depth to search (0 for unlimited)",
			},
		},
		"required": []string{"path", "pattern"},
	}
}

func (t *SearchText) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed SearchTextInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate path and pattern
	if parsed.Path == "" {
		return tools.BuildToolResult(false, "Path cannot be empty", nil), nil
	}
	if parsed.Pattern == "" {
		return tools.BuildToolResult(false, "Pattern cannot be empty", nil), nil
	}

	// Validate and resolve path
	absPath, err := tools.ValidateAndResolvePath(parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to resolve absolute path: "+err.Error(), nil), nil
	}

	// Check if path exists
	if _, err := tools.PathExists(absPath); err != nil {
		return tools.BuildToolResult(false, "Path does not exist", nil), nil
	}

	// Set default max depth if not specified
	maxDepth := parsed.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 10 // Reasonable default
	}

	// Prepare search pattern
	searchPattern := parsed.Pattern
	if !parsed.CaseSensitive {
		searchPattern = strings.ToLower(parsed.Pattern)
	}

	// Search for matches
	matches := []TextMatch{}
	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		// Skip directories
		if info.IsDir() {
			// Check depth
			relPath, err := filepath.Rel(absPath, path)
			if err != nil {
				return nil
			}
			depth := strings.Count(relPath, string(filepath.Separator))
			if depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden files and binary files
		if tools.ShouldSkipFile(path) {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip files we can't read
		}

		// Search line by line
		lines := strings.Split(string(content), "\n")
		for lineNum, line := range lines {
			searchLine := line
			if !parsed.CaseSensitive {
				searchLine = strings.ToLower(line)
			}

			if strings.Contains(searchLine, searchPattern) {
				matches = append(matches, TextMatch{
					File:    path,
					Line:    lineNum + 1,
					Content: strings.TrimSpace(line),
				})
			}
		}

		return nil
	})

	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Search failed: " + err.Error(),
		}, nil
	}

	// Build output
	output := SearchTextOutput{
		Matches: matches,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Search completed", dataMap), nil
}
