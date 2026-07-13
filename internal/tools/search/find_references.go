package search

import (
	"context"
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
	absPath, err := tools.ValidateAndResolvePath(parsed.Directory)
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
	err = filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Skip hidden files
		baseName := filepath.Base(path)
		if tools.IsHidden(baseName) {
			return nil
		}

		// Filter by language extension
		if !matchesLanguage(path, parsed.Language) {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		// Search for symbol references
		lines := strings.Split(string(content), "\n")
		for lineNum, line := range lines {
			matches := pattern.FindAllStringIndex(line, -1)
			for _, match := range matches {
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

	if err != nil {
		return &tools.ToolResult{
			Success: false,
			Message: "Search failed: " + err.Error(),
		}, nil
	}

	// Build output
	output := FindReferencesOutput{
		References: references,
	}

	// Convert output to map for ToolResult
	dataMap, err := tools.SerializeOutput(output)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return tools.BuildToolResult(true, "Reference search completed", dataMap), nil
}
