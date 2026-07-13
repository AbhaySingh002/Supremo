package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// SearchFileName searches for files by name pattern in a directory.
// This tool exists because coding agents need to find files by name when
// locating specific modules, configuration files, or resources.
// Claude Code and Cursor use this when users ask to find files by name or
// when the agent needs to locate specific files in the project structure.
// Security: We validate the search path is within workspace and limit search
// depth to prevent excessive resource usage.

type SearchFileNameInput struct {
	Path          string `json:"path"`
	Pattern       string `json:"pattern"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxDepth      int    `json:"max_depth"`
}

type SearchFileNameOutput struct {
	Matches []FileNameMatch `json:"matches"`
}

type FileNameMatch struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Type string `json:"type"` // "file" or "directory"
}

type SearchFileName struct{}

func (t *SearchFileName) Name() string {
	return "search_file_name"
}

func (t *SearchFileName) Description() string {
	return "Searches for files by name pattern in a directory. Returns file path, name, and type for each match."
}

func (t *SearchFileName) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the directory to search in",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "File name pattern to search for (supports * wildcards)",
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

func (t *SearchFileName) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed SearchFileNameInput
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

	// Prepare search pattern - convert glob to simple matching
	searchPattern := parsed.Pattern
	if !parsed.CaseSensitive {
		searchPattern = strings.ToLower(parsed.Pattern)
	}

	// Search for matches
	matches := []FileNameMatch{}
	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access
		}

		// Check depth for directories
		if info.IsDir() {
			relPath, err := filepath.Rel(absPath, path)
			if err != nil {
				return nil
			}
			depth := strings.Count(relPath, string(filepath.Separator))
			if depth > maxDepth {
				return filepath.SkipDir
			}
		}

		// Get the base name
		baseName := filepath.Base(path)

		// Skip hidden files
		if tools.IsHidden(baseName) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Match the pattern
		nameToMatch := baseName
		if !parsed.CaseSensitive {
			nameToMatch = strings.ToLower(baseName)
		}

		// Simple glob matching (supports * wildcard)
		if matchesPattern(nameToMatch, searchPattern) {
			matchType := "file"
			if info.IsDir() {
				matchType = "directory"
			}
			matches = append(matches, FileNameMatch{
				Path: path,
				Name: baseName,
				Type: matchType,
			})
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
	output := SearchFileNameOutput{
		Matches: matches,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Search completed", dataMap), nil
}

// matchesPattern checks if a name matches a glob pattern (supports * wildcard)
func matchesPattern(name, pattern string) bool {
	// If pattern has no wildcard, do exact match
	if !strings.Contains(pattern, "*") {
		return name == pattern
	}

	// Simple glob matching
	patternParts := strings.Split(pattern, "*")
	if len(patternParts) == 0 {
		return true
	}

	// Check if name starts with first part
	if !strings.HasPrefix(name, patternParts[0]) {
		return false
	}

	// Check if name ends with last part
	if !strings.HasSuffix(name, patternParts[len(patternParts)-1]) {
		return false
	}

	// Check middle parts in order
	currentPos := len(patternParts[0])
	for i := 1; i < len(patternParts)-1; i++ {
		part := patternParts[i]
		if part == "" {
			continue
		}
		idx := strings.Index(name[currentPos:], part)
		if idx == -1 {
			return false
		}
		currentPos += idx + len(part)
	}

	return true
}
