package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/repository"
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
	Matches   []FileNameMatch `json:"matches"`
	Truncated bool            `json:"truncated,omitempty"`
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

func (t *SearchFileName) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (t *SearchFileName) Description() string {
	return "Finds files by name glob. Use this to localize a path, then search_text or read_file inside it."
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
	absPath, err := tools.ValidateAndResolvePath(ctx, parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to resolve absolute path: "+err.Error(), nil), nil
	}

	// Check if path exists
	if _, err := tools.PathExists(absPath); err != nil {
		return tools.BuildToolResult(false, "Path does not exist", nil), nil
	}
	if query := strings.Trim(parsed.Pattern, "*"); query != "" {
		if result, index, indexed, err := indexedQuery(ctx, repository.Query{Text: query, Kind: "file", Exact: true, Limit: tools.MaxSearchResults}); indexed {
			if err != nil {
				return &tools.ToolResult{Success: false, Message: "Indexed search failed: " + err.Error()}, nil
			}
			matches := []FileNameMatch{}
			for _, candidate := range result.Candidates {
				if candidate.Type != "file" || !candidateInDirectory(index, candidate, absPath) {
					continue
				}
				name := filepath.Base(candidate.Path)
				value, pattern := name, parsed.Pattern
				if !parsed.CaseSensitive {
					value, pattern = strings.ToLower(value), strings.ToLower(pattern)
				}
				if matched, _ := filepath.Match(pattern, value); matched {
					matches = append(matches, FileNameMatch{Path: indexedPath(index, candidate), Name: name, Type: "file"})
				}
			}
			return tools.BuildSerializedToolResult(true, "Search completed", SearchFileNameOutput{Matches: matches, Truncated: len(matches) >= tools.MaxSearchResults}), nil
		}
	}

	maxDepth := tools.SearchDepthLimit(parsed.MaxDepth)

	// Prepare search pattern - convert glob to simple matching
	searchPattern := parsed.Pattern
	if !parsed.CaseSensitive {
		searchPattern = strings.ToLower(parsed.Pattern)
	}
	if _, err := filepath.Match(searchPattern, ""); err != nil {
		return tools.BuildToolResult(false, "Invalid file name pattern: "+err.Error(), nil), nil
	}

	// Search for matches
	matches := []FileNameMatch{}
	truncated := false
	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil // Skip files we can't access
		}

		if info.IsDir() {
			if tools.IsHidden(info.Name()) && path != absPath {
				return filepath.SkipDir
			}
			depth, err := tools.SearchDepth(absPath, path)
			if err != nil {
				return nil
			}
			if depth > maxDepth {
				return filepath.SkipDir
			}
		}

		// Get the base name
		baseName := filepath.Base(path)

		if tools.ShouldSkipFile(path) {
			return nil
		}
		depth, err := tools.SearchDepth(absPath, path)
		if err != nil || depth > maxDepth {
			return err
		}

		// Match the pattern
		nameToMatch := baseName
		if !parsed.CaseSensitive {
			nameToMatch = strings.ToLower(baseName)
		}

		matched, _ := filepath.Match(searchPattern, nameToMatch)
		if matched {
			if len(matches) == tools.MaxSearchResults {
				truncated = true
				return tools.ErrSearchLimit
			}
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

	if errors.Is(err, tools.ErrSearchLimit) {
		err = nil
	}
	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Search failed: " + err.Error(),
		}, nil
	}

	// Build output
	output := SearchFileNameOutput{
		Matches:   matches,
		Truncated: truncated,
	}

	return tools.BuildSerializedToolResult(true, "Search completed", output), nil
}
