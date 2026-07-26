package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// FindReferences searches for references to a symbol in the codebase.
// This tool exists because coding agents need to find where functions,
// classes, variables, and other symbols are used to understand dependencies
// and the impact of changes.
// Claude Code and Cursor use this when users ask to find where something is
// used or when the agent needs to understand symbol usage before refactoring.
// Security: We validate the search path is within workspace and use regex
// patterns to match symbol references.
// Note: This is a simplified implementation. Full reference finding requires
// LSP integration or language-specific parsers.

type FindReferencesInput struct {
	Directory string `json:"directory"`
	Symbol    string `json:"symbol"`
	Language  string `json:"language"` // go, python, javascript, etc.
}

type FindReferencesOutput struct {
	References []ReferenceMatch `json:"references"`
	Truncated  bool             `json:"truncated,omitempty"`
}

type ReferenceMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Context string `json:"context"`
}

type FindReferences struct{}

func (t *FindReferences) Name() string {
	return "find_references"
}

func (t *FindReferences) Description() string {
	return "Searches for references to a symbol in the codebase. Returns file path, line number, column, and context for each reference."
}

func (t *FindReferences) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Directory to search in",
			},
			"symbol": map[string]any{
				"type":        "string",
				"description": "Symbol name to search for references",
			},
			"language": map[string]any{
				"type":        "string",
				"description": "Programming language (go, python, javascript, typescript)",
			},
		},
		"required": []string{"directory", "symbol"},
	}
}

func (t *FindReferences) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed FindReferencesInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate inputs
	if parsed.Directory == "" {
		return tools.BuildToolResult(false, "Directory cannot be empty", nil), nil
	}
	if parsed.Symbol == "" {
		return tools.BuildToolResult(false, "Symbol cannot be empty", nil), nil
	}

	// Validate and resolve path
	absPath, err := tools.ValidateAndResolvePath(ctx, parsed.Directory)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to resolve absolute path: "+err.Error(), nil), nil
	}

	// Check if path exists
	if _, err := tools.PathExists(absPath); err != nil {
		return tools.BuildToolResult(false, "Directory does not exist", nil), nil
	}

	// Build regex pattern for symbol references
	// This is a simplified pattern - real reference finding needs language-aware parsing
	escapedSymbol := regexp.QuoteMeta(parsed.Symbol)

	// Pattern matches the symbol as a word boundary to avoid partial matches
	pattern := regexp.MustCompile(`\b` + escapedSymbol + `\b`)

	// Search for references
	references := []ReferenceMatch{}
	truncated := false
	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil
		}

		if info.IsDir() {
			if tools.IsHidden(info.Name()) && path != absPath {
				return filepath.SkipDir
			}
			depth, err := tools.SearchDepth(absPath, path)
			if err != nil {
				return err
			}
			if depth > tools.MaxSearchDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if tools.ShouldSkipFile(path) {
			return nil
		}
		depth, err := tools.SearchDepth(absPath, path)
		if err != nil || depth > tools.MaxSearchDepth {
			return err
		}

		// Filter by language extension
		if !matchesLanguage(path, parsed.Language) {
			return nil
		}

		// Read file content
		content, err := tools.ReadSearchFile(path)
		if err != nil {
			return nil
		}

		// Search for symbol references
		lines := strings.Split(string(content), "\n")
		for lineNum, line := range lines {
			matches := pattern.FindAllStringIndex(line, -1)
			for _, match := range matches {
				if len(references) == tools.MaxSearchResults {
					truncated = true
					return tools.ErrSearchLimit
				}
				// Get context around the match
				start := match[0] - 20
				if start < 0 {
					start = 0
				}
				end := match[1] + 20
				if end > len(line) {
					end = len(line)
				}
				context := strings.TrimSpace(line[start:end])

				references = append(references, ReferenceMatch{
					File:    path,
					Line:    lineNum + 1,
					Column:  match[0] + 1, // 1-indexed column
					Context: context,
				})
			}
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
	output := FindReferencesOutput{
		References: references,
		Truncated:  truncated,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Reference search completed", dataMap), nil
}
