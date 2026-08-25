package search

import (
	"context"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/repository"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// RepositoryQuery exposes a read-only hybrid lookup; read_file remains the
// exact-source follow-up once a candidate is selected.
type RepositoryQuery struct{}

type RepositoryQueryInput struct {
	Query string `json:"query"`
	Path  string `json:"path,omitempty"`
	Exact bool   `json:"exact,omitempty"`
}

func (RepositoryQuery) Name() string { return "repository_query" }

func (RepositoryQuery) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (RepositoryQuery) Description() string {
	return "Architecture/index lookup by symbol, path, or concept. After a candidate, read_file around the returned line. Not a substitute for search_text."
}

func (RepositoryQuery) Schema() any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"query": map[string]any{"type": "string", "description": "Identifier, path fragment, or conceptual query"},
		"path":  map[string]any{"type": "string", "description": "Optional directory scope"},
		"exact": map[string]any{"type": "boolean", "description": "Only use exact/path/symbol lookup"},
	}, "required": []string{"query"}}
}

func (RepositoryQuery) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var parsed RepositoryQueryInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	if strings.TrimSpace(parsed.Query) == "" {
		return tools.BuildToolResult(false, "Query cannot be empty", nil), nil
	}
	if parsed.Path != "" {
		if _, err := tools.ValidateAndResolvePath(ctx, parsed.Path); err != nil {
			return tools.BuildToolResult(false, "Failed to resolve path: "+err.Error(), nil), nil
		}
	}
	index := repository.FromContext(ctx)
	if index == nil {
		return tools.BuildToolResult(false, "Workspace index is unavailable", nil), nil
	}
	result, err := index.Query(ctx, repository.Query{Text: parsed.Query, Path: parsed.Path, Exact: parsed.Exact, FullText: !parsed.Exact})
	if err != nil {
		return &tools.ToolResult{Success: false, Message: "Repository query failed: " + err.Error()}, nil
	}
	return tools.BuildSerializedToolResult(true, "Repository query completed", result), nil
}
