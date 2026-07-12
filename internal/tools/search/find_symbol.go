package search

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

// FindSymbol searches for symbol definitions in the codebase.
// This tool exists because coding agents need to find where functions,
// classes, variables, and other symbols are defined to understand code
// structure and navigate the codebase.
// Claude Code and Cursor use this when users ask to find where something
// is defined or when the agent needs to locate symbol definitions.
// Security: We validate the search path is within workspace and use regex
// patterns to match common symbol definitions.
// Note: This is a simplified implementation. Full symbol finding requires
// LSP integration or language-specific parsers.

type FindSymbolInput struct {
	Directory string `json:"directory"`
	Symbol    string `json:"symbol"`
	Language  string `json:"language"` // go, python, javascript, etc.
}

type FindSymbolOutput struct {
	Matches []SymbolMatch `json:"matches"`
}

type SymbolMatch struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Symbol     string `json:"symbol"`
	SymbolType string `json:"symbol_type"` // function, class, variable, etc.
}

type FindSymbol struct{}

func (t *FindSymbol) Name() string {
	return "find_symbol"
}

func (t *FindSymbol) Description() string {
	return "Searches for symbol definitions in the codebase. Returns file path, line number, symbol name, and type."
}

func (t *FindSymbol) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"directory": map[string]any{
				"type":        "string",
				"description": "Directory to search in",
			},
			"symbol": map[string]any{
				"type":        "string",
				"description": "Symbol name to search for",
			},
			"language": map[string]any{
				"type":        "string",
				"description": "Programming language (go, python, javascript, typescript)",
			},
		},
		"required": []string{"directory", "symbol"},
	}
}

func (t *FindSymbol) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	// Parse input
	var parsed FindSymbolInput
	if err := ParseInput(input, &parsed); err != nil {
		return nil, err
	}

	// Validate inputs
	if parsed.Directory == "" {
		return BuildToolResult(false, "Directory cannot be empty", nil), nil
	}
	if parsed.Symbol == "" {
		return BuildToolResult(false, "Symbol cannot be empty", nil), nil
	}

	// Validate and resolve path
	absPath, err := ValidateAndResolvePath(parsed.Directory)
	if err != nil {
		return BuildToolResult(false, "Failed to resolve absolute path: "+err.Error(), nil), nil
	}

	// Check if path exists
	if _, err := PathExists(absPath); err != nil {
		return BuildToolResult(false, "Directory does not exist", nil), nil
	}

	// Get language-specific patterns
	patterns := getSymbolPatterns(parsed.Language, parsed.Symbol)

	// Search for matches
	matches := []SymbolMatch{}
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
		if IsHidden(baseName) {
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

		// Search for symbol definitions
		lines := strings.Split(string(content), "\n")
		for lineNum, line := range lines {
			for _, pattern := range patterns {
				if pattern.regex.MatchString(line) {
					matches = append(matches, SymbolMatch{
						File:       path,
						Line:       lineNum + 1,
						Symbol:     parsed.Symbol,
						SymbolType: pattern.symbolType,
					})
					break // Don't add duplicate matches for the same line
				}
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
	output := FindSymbolOutput{
		Matches: matches,
	}

	// Convert output to map for ToolResult
	dataMap, err := SerializeOutput(output)
	if err != nil {
		return BuildToolResult(false, "Failed to serialize output: "+err.Error(), nil), nil
	}

	return BuildToolResult(true, "Symbol search completed", dataMap), nil
}

type symbolPattern struct {
	regex      *regexp.Regexp
	symbolType string
}

func getSymbolPatterns(language, symbol string) []symbolPattern {
	// Escape special regex characters in symbol
	escapedSymbol := regexp.QuoteMeta(symbol)

	switch strings.ToLower(language) {
	case "go":
		return []symbolPattern{
			{regexp.MustCompile(`func\s+` + escapedSymbol + `\s*\(`), "function"},
			{regexp.MustCompile(`type\s+` + escapedSymbol + `\s+`), "type"},
			{regexp.MustCompile(`var\s+` + escapedSymbol + `\s+`), "variable"},
			{regexp.MustCompile(`const\s+` + escapedSymbol + `\s+`), "constant"},
		}
	case "python":
		return []symbolPattern{
			{regexp.MustCompile(`def\s+` + escapedSymbol + `\s*\(`), "function"},
			{regexp.MustCompile(`class\s+` + escapedSymbol + `\s*:`), "class"},
		}
	case "javascript", "typescript":
		return []symbolPattern{
			{regexp.MustCompile(`function\s+` + escapedSymbol + `\s*\(`), "function"},
			{regexp.MustCompile(`const\s+` + escapedSymbol + `\s*=`), "variable"},
			{regexp.MustCompile(`let\s+` + escapedSymbol + `\s*=`), "variable"},
			{regexp.MustCompile(`class\s+` + escapedSymbol + `\s*`), "class"},
		}
	default:
		// Generic patterns
		return []symbolPattern{
			{regexp.MustCompile(`function\s+` + escapedSymbol + `\s*\(`), "function"},
			{regexp.MustCompile(`def\s+` + escapedSymbol + `\s*\(`), "function"},
			{regexp.MustCompile(`class\s+` + escapedSymbol + `\s*`), "class"},
		}
	}
}

func matchesLanguage(path, language string) bool {
	if language == "" {
		return true // Search all files
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch strings.ToLower(language) {
	case "go":
		return ext == ".go"
	case "python":
		return ext == ".py"
	case "javascript":
		return ext == ".js"
	case "typescript":
		return ext == ".ts"
	default:
		return true
	}
}
