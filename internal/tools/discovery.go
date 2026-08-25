package tools

import (
	"context"
	"strings"
)

// DiscoverTools is the small, read-only bootstrap for finding capabilities
// without sending every installed schema to the provider.
type DiscoverTools struct{ registry *Registry }

func NewDiscoverTools(registry *Registry) *DiscoverTools { return &DiscoverTools{registry: registry} }

func (*DiscoverTools) Name() string { return "discover_tools" }
func (*DiscoverTools) Description() string {
	return "Lists matching tool capabilities and families without loading their argument schemas."
}
func (*DiscoverTools) Schema() any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"query": map[string]any{"type": "string", "description": "Capability, family, or tool-name fragment"},
		"limit": map[string]any{"type": "integer", "description": "Maximum matches (default 8, maximum 16)"},
	}}
}
func (*DiscoverTools) Capabilities() CapabilitySet { return CapabilityReadWorkspace }
func (t *DiscoverTools) Execute(_ context.Context, input any) (*ToolResult, error) {
	var request struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := ParseInput(input, &request); err != nil {
		return nil, err
	}
	if t.registry == nil {
		return BuildToolResult(false, "Tool registry is unavailable", nil), nil
	}
	if strings.TrimSpace(request.Query) == "" {
		request.Query = ""
	}
	catalog, err := t.registry.Catalog()
	if err != nil {
		return nil, err
	}
	if request.Limit <= 0 {
		request.Limit = 8
	}
	return BuildSerializedToolResult(true, "Matching tool capabilities", map[string]any{"tools": catalog.Discover(request.Query, request.Limit)}), nil
}
