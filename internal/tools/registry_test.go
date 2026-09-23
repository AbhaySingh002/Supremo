package tools

import (
	"context"
	"strings"
	"testing"
)

type catalogTool struct {
	name string
	caps CapabilitySet
}

func (t catalogTool) Name() string        { return t.name }
func (t catalogTool) Description() string { return "test " + t.name }
func (t catalogTool) Schema() any         { return map[string]any{"type": "object"} }
func (t catalogTool) Capabilities() CapabilitySet {
	if t.caps != 0 {
		return t.caps
	}
	return CapabilityReadWorkspace
}
func (t catalogTool) Execute(context.Context, any) (*ToolResult, error) {
	return BuildToolResult(true, "ok", nil), nil
}

func TestCatalogRoutesEveryEligibleToolInStableOrder(t *testing.T) {
	registry := NewRegistry()
	for _, tool := range []catalogTool{
		{name: "read_file", caps: CapabilityReadWorkspace},
		{name: "write_file", caps: CapabilityWriteWorkspace},
		{name: "execute_command", caps: CapabilityExecuteProcess},
		{name: "web_fetch", caps: CapabilityUseNetwork},
	} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}

	route := catalog.Route(ToolRouteProfile{Mode: ToolModeNormal})
	got := make([]string, 0, len(route.Candidates))
	for _, candidate := range route.Candidates {
		if candidate.Reason != string(ToolModeNormal) {
			t.Fatalf("candidate reason = %#v", candidate)
		}
		got = append(got, candidate.Tool.Name)
	}
	if strings.Join(got, ",") != "execute_command,read_file,web_fetch,write_file" {
		t.Fatalf("normal route = %v", got)
	}

	execution := catalog.Route(ToolRouteProfile{Mode: ToolModeExecution})
	if len(execution.Candidates) != len(route.Candidates) {
		t.Fatalf("execution route = %#v", execution)
	}
	for _, candidate := range execution.Candidates {
		if candidate.Reason != string(ToolModeExecution) {
			t.Fatalf("execution candidate = %#v", candidate)
		}
	}
}

func TestCatalogPreservesReadOnlyAndSideProfileBoundaries(t *testing.T) {
	registry := NewRegistry()
	entries := []struct {
		tool catalogTool
		meta ToolMetadata
	}{
		{catalogTool{name: "read_file", caps: CapabilityReadWorkspace}, ToolMetadata{CanonicalName: "read_file", Family: "filesystem", CapabilityTags: []string{"read"}, Access: ToolAccessRead, SideEffect: ToolSideEffectNone}},
		{catalogTool{name: "glob", caps: CapabilityReadWorkspace}, ToolMetadata{CanonicalName: "glob", Family: "repository", CapabilityTags: []string{"glob"}, Access: ToolAccessRead, SideEffect: ToolSideEffectNone}},
		{catalogTool{name: "write_file", caps: CapabilityWriteWorkspace}, ToolMetadata{CanonicalName: "write_file", Family: "filesystem", CapabilityTags: []string{"write"}, Access: ToolAccessWrite, SideEffect: ToolSideEffectWorkspace}},
		{catalogTool{name: "execute_command", caps: CapabilityExecuteProcess}, ToolMetadata{CanonicalName: "execute_command", Family: "shell", CapabilityTags: []string{"execute"}, Access: ToolAccessDestructive, SideEffect: ToolSideEffectProcess}},
		{catalogTool{name: "web_fetch", caps: CapabilityUseNetwork}, ToolMetadata{CanonicalName: "web_fetch", Family: "web", CapabilityTags: []string{"fetch"}, Access: ToolAccessRead, SideEffect: ToolSideEffectNetwork}},
	}
	for _, entry := range entries {
		if err := registry.Register(entry.tool, entry.meta); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}

	planning := catalog.Route(ToolRouteProfile{Mode: ToolModePlanning, ReadOnly: true, ResearchOnly: true})
	got := make([]string, 0, len(planning.Candidates))
	for _, candidate := range planning.Candidates {
		if !candidate.Tool.PlanningSafe() {
			t.Fatalf("unsafe planning tool: %#v", candidate.Tool)
		}
		got = append(got, candidate.Tool.Name)
	}
	if strings.Join(got, ",") != "glob,read_file" {
		t.Fatalf("planning route = %v", got)
	}
	if side := catalog.Route(ToolRouteProfile{Mode: ToolModeSide}); len(side.Candidates) != 0 {
		t.Fatalf("side route = %#v", side)
	}
}

func TestRegistryMakesObjectSchemasStrict(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(catalogTool{name: "strict_tool"}); err != nil {
		t.Fatal(err)
	}
	descriptor, err := registry.Descriptor("strict_tool")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(descriptor.InputSchema), `"additionalProperties": false`) {
		t.Fatalf("schema is not strict: %s", descriptor.InputSchema)
	}
}

func TestParallelSafetyIsExplicitAndFailClosed(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(catalogTool{name: "safe"}, ToolMetadata{ParallelSafe: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(catalogTool{name: "unknown"}); err != nil {
		t.Fatal(err)
	}
	safe, _ := registry.Descriptor("safe")
	unknown, _ := registry.Descriptor("unknown")
	if !safe.ParallelSafe || unknown.ParallelSafe {
		t.Fatalf("parallel safety safe=%v unknown=%v", safe.ParallelSafe, unknown.ParallelSafe)
	}
}
