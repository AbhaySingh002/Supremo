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

const defaultSearchResults = 50

type SearchTextInput struct {
	Path          string `json:"path"`
	Pattern       string `json:"pattern"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxDepth      int    `json:"max_depth"`
	Glob          string `json:"glob,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
	ContextLines  int    `json:"context_lines,omitempty"`
}

type SearchTextOutput struct {
	Matches   []TextMatch `json:"matches"`
	Truncated bool        `json:"truncated,omitempty"`
}

type TextMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
	Context string `json:"context,omitempty"`
}

type SearchText struct{}

func (t *SearchText) Name() string { return "search_text" }

func (t *SearchText) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (t *SearchText) Description() string {
	return "Localizes literal or substring matches. Returns file, line, match, and optional context. Path may be a file or directory. Narrow with glob/path/max_results when truncated; then read_file around the line. Use find_symbol for definitions and find_references for usages."
}

func (t *SearchText) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":           map[string]any{"type": "string", "description": "File or directory to search"},
			"pattern":        map[string]any{"type": "string", "description": "Literal substring to find"},
			"case_sensitive": map[string]any{"type": "boolean", "description": "Case-sensitive match (default false)"},
			"glob":           map[string]any{"type": "string", "description": "Optional filename glob (e.g. *.go)"},
			"max_results":    map[string]any{"type": "integer", "description": "Cap results (default 50)"},
			"context_lines":  map[string]any{"type": "integer", "description": "Neighboring lines to include as context"},
			"max_depth":      map[string]any{"type": "integer", "description": "Maximum directory depth (0 for default limit)"},
		},
		"required": []string{"path", "pattern"},
	}
}

func (t *SearchText) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var parsed SearchTextInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	if parsed.Path == "" {
		return tools.BuildToolResult(false, "Path cannot be empty", nil), nil
	}
	if parsed.Pattern == "" {
		return tools.BuildToolResult(false, "Pattern cannot be empty", nil), nil
	}
	absPath, err := tools.ValidateAndResolvePath(ctx, parsed.Path)
	if err != nil {
		return tools.BuildToolResult(false, "Failed to resolve absolute path: "+err.Error(), nil), nil
	}
	info, err := tools.PathExists(absPath)
	if err != nil {
		return tools.BuildToolResult(false, "Path does not exist", nil), nil
	}
	limit := parsed.MaxResults
	if limit <= 0 {
		limit = defaultSearchResults
	}
	if limit > tools.MaxSearchResults {
		limit = tools.MaxSearchResults
	}
	ctxLines := parsed.ContextLines
	if ctxLines < 0 {
		ctxLines = 0
	}

	if result, index, indexed, err := indexedQuery(ctx, repository.Query{Text: parsed.Pattern, FullText: true, Limit: limit}); indexed {
		if err != nil {
			return &tools.ToolResult{Success: false, Message: "Indexed search failed: " + err.Error()}, nil
		}
		needle := parsed.Pattern
		if !parsed.CaseSensitive {
			needle = strings.ToLower(needle)
		}
		matches := []TextMatch{}
		truncated := false
		for _, candidate := range result.Candidates {
			if candidate.Type != "chunk" || !candidateInDirectory(index, candidate, absPath) {
				continue
			}
			file := indexedPath(index, candidate)
			if parsed.Glob != "" {
				ok, err := filepath.Match(parsed.Glob, filepath.Base(file))
				if err != nil {
					return tools.BuildToolResult(false, "Invalid glob: "+err.Error(), nil), nil
				}
				if !ok {
					continue
				}
			}
			lines := strings.Split(candidate.Content, "\n")
			for i, content := range lines {
				comparison := content
				if !parsed.CaseSensitive {
					comparison = strings.ToLower(comparison)
				}
				if !strings.Contains(comparison, needle) {
					continue
				}
				if len(matches) >= limit {
					truncated = true
					break
				}
				matches = append(matches, TextMatch{
					File: file, Line: candidate.StartLine + i, Content: strings.TrimSpace(content),
					Context: matchContext(lines, i, ctxLines),
				})
			}
			if truncated {
				break
			}
		}
		return searchTextResult(matches, truncated), nil
	}

	matches := []TextMatch{}
	truncated := false
	searchPattern := parsed.Pattern
	if !parsed.CaseSensitive {
		searchPattern = strings.ToLower(parsed.Pattern)
	}
	collect := func(path string) error {
		if tools.ShouldSkipFile(path) {
			return nil
		}
		if parsed.Glob != "" {
			ok, err := filepath.Match(parsed.Glob, filepath.Base(path))
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		content, err := tools.ReadSearchFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(content), "\n")
		for lineNum, line := range lines {
			searchLine := line
			if !parsed.CaseSensitive {
				searchLine = strings.ToLower(line)
			}
			if !strings.Contains(searchLine, searchPattern) {
				continue
			}
			if len(matches) >= limit {
				truncated = true
				return tools.ErrSearchLimit
			}
			matches = append(matches, TextMatch{
				File: path, Line: lineNum + 1, Content: strings.TrimSpace(line),
				Context: matchContext(lines, lineNum, ctxLines),
			})
		}
		return nil
	}

	if !info.IsDir() {
		if err := collect(absPath); err != nil && !errors.Is(err, tools.ErrSearchLimit) {
			if strings.Contains(err.Error(), "syntax") || strings.Contains(err.Error(), "Glob") {
				return tools.BuildToolResult(false, "Invalid glob: "+err.Error(), nil), nil
			}
			return &tools.ToolResult{Success: false, Message: "Search failed: " + err.Error()}, nil
		}
		return searchTextResult(matches, truncated), nil
	}

	maxDepth := tools.SearchDepthLimit(parsed.MaxDepth)
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
				return nil
			}
			if depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if tools.IsHidden(info.Name()) {
			return nil
		}
		depth, err := tools.SearchDepth(absPath, path)
		if err != nil || depth > maxDepth {
			return err
		}
		return collect(path)
	})
	if errors.Is(err, tools.ErrSearchLimit) {
		err = nil
	}
	if err != nil {
		return &tools.ToolResult{Success: false, Message: "Search failed: " + err.Error()}, nil
	}
	return searchTextResult(matches, truncated), nil
}

func matchContext(lines []string, i, n int) string {
	if n <= 0 {
		return ""
	}
	start := i - n
	if start < 0 {
		start = 0
	}
	end := i + n + 1
	if end > len(lines) {
		end = len(lines)
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func searchTextResult(matches []TextMatch, truncated bool) *tools.ToolResult {
	msg := "Search completed"
	if truncated {
		msg = "Search truncated; narrow path, glob, or pattern"
	}
	return tools.BuildSerializedToolResult(true, msg, SearchTextOutput{Matches: matches, Truncated: truncated})
}
