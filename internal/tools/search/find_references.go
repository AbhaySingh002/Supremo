package search

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/repository"
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

func (t *FindReferences) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (t *FindReferences) Description() string {
	return "Finds symbol usages (file + line). Follow with read_file around that line. Use find_symbol for the definition."
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
	if result, index, indexed, err := indexedQuery(ctx, repository.Query{Text: parsed.Symbol, Kind: "symbol", Exact: true, Limit: tools.MaxSearchResults}); indexed {
		if err != nil {
			return &tools.ToolResult{Success: false, Message: "Indexed search failed: " + err.Error()}, nil
		}
		references := make([]ReferenceMatch, 0, len(result.Candidates))
		for _, candidate := range result.Candidates {
			if candidate.Type != "symbol" || candidate.GraphDistance != 1 || !candidateInDirectory(index, candidate, absPath) || parsed.Language != "" && parsed.Language != "go" {
				continue
			}
			references = append(references, ReferenceMatch{File: indexedPath(index, candidate), Line: candidate.StartLine, Column: candidate.StartColumn, Context: candidate.Signature})
		}
		return tools.BuildSerializedToolResult(true, "Reference search completed", FindReferencesOutput{References: references, Truncated: len(references) >= tools.MaxSearchResults}), nil
	}

	// Build regex pattern for symbol references
	// This is a simplified pattern - real reference finding needs language-aware parsing
	escapedSymbol := regexp.QuoteMeta(parsed.Symbol)

	// Pattern matches the symbol as a word boundary to avoid partial matches
	pattern := regexp.MustCompile(`\b` + escapedSymbol + `\b`)

	// Search for references
	references := []ReferenceMatch{}
	truncated := false
	err = walkSourceFiles(ctx, absPath, parsed.Language, func(path string, lines []string) error {
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

	return tools.BuildSerializedToolResult(true, "Reference search completed", output), nil
}
