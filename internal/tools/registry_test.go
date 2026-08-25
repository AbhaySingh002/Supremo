package tools

import (
	"context"
	"fmt"
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

func TestCatalogRoutesBootstrapFamiliesAndReadOnlyPolicy(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"read_file", "repository_query", "git_log", "git_status"} {
		caps := CapabilityReadWorkspace
		if name == "git_log" || name == "git_status" {
			caps |= CapabilityExecuteProcess
		}
		meta := ToolMetadata{CanonicalName: name, Family: "workspace", CapabilityTags: []string{"workspace"}}
		if name == "git_log" || name == "git_status" {
			meta.Family, meta.CapabilityTags = "git", []string{"git"}
		}
		if name == "read_file" || name == "repository_query" {
			meta.Bootstrap = true
		}
		if err := registry.Register(catalogTool{name: name, caps: caps}, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(NewDiscoverTools(registry), ToolMetadata{CanonicalName: "discover_tools", Family: "tool_discovery", CapabilityTags: []string{"tool.discovery"}, Bootstrap: true}); err != nil {
		t.Fatal(err)
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	route := catalog.Route(ToolRouteProfile{Mode: ToolModeNormal, RequestedCapabilities: []string{"git"}})
	got := map[string]bool{}
	for _, candidate := range route.Candidates {
		got[candidate.Tool.Name] = true
	}
	if !got["git_log"] || !got["git_status"] {
		t.Fatalf("git request missing git tools: %#v", route)
	}
	if got["discover_tools"] || got["read_file"] || got["repository_query"] {
		t.Fatalf("normal chat still auto-attached bootstrap: %#v", route)
	}
	idle := catalog.Route(ToolRouteProfile{Mode: ToolModeNormal, Objective: "What is a mutex?"})
	if len(idle.Candidates) != 0 {
		t.Fatalf("pure conversation attached tools: %#v", idle)
	}
	readOnly := catalog.Route(ToolRouteProfile{Mode: ToolModePlanning, ReadOnly: true, RequestedCapabilities: []string{"git"}})
	for _, candidate := range readOnly.Candidates {
		if candidate.Tool.Family == "git" {
			t.Fatalf("read-only route exposed Git process tool: %#v", candidate)
		}
	}
	if len(readOnly.Rejected) == 0 {
		t.Fatalf("read-only request was not diagnosed: %#v", readOnly)
	}
}

func TestPlanResearchRoutesOnlyLocalCoreAndActivatedTools(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"read_file", "list_directory", "search_file_name", "repository_query", "find_symbol", "web_fetch", "execute_command"} {
		caps := CapabilityReadWorkspace
		switch name {
		case "web_fetch":
			caps |= CapabilityUseNetwork
		case "execute_command":
			caps |= CapabilityExecuteProcess
		}
		core := name == "read_file" || name == "list_directory" || name == "search_file_name" || name == "repository_query"
		if err := registry.Register(catalogTool{name: name, caps: caps}, ToolMetadata{CanonicalName: name, Family: "workspace", CapabilityTags: []string{"workspace"}, PlanningCore: core, Bootstrap: name == "read_file" || name == "repository_query"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(NewDiscoverTools(registry), ToolMetadata{CanonicalName: "discover_tools", Family: "tool_discovery", CapabilityTags: []string{"tool.discovery"}, Bootstrap: true, PlanningCore: true}); err != nil {
		t.Fatal(err)
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	route := catalog.Route(ToolRouteProfile{Mode: ToolModePlanning, ReadOnly: true, ResearchOnly: true, Objective: "find every symbol"})
	got := make(map[string]string)
	for _, candidate := range route.Candidates {
		got[candidate.Tool.Name] = candidate.Reason
	}
	for _, name := range []string{"discover_tools", "read_file", "list_directory", "search_file_name", "repository_query", "find_symbol"} {
		if got[name] != "planning" {
			t.Fatalf("planning tools missing %s: %#v", name, route)
		}
	}
	for _, name := range []string{"web_fetch", "execute_command"} {
		if _, found := got[name]; found {
			t.Fatalf("plan research exposed %s: %#v", name, route)
		}
	}

	route = catalog.Route(ToolRouteProfile{Mode: ToolModePlanning, ReadOnly: true, ResearchOnly: true, RequestedCapabilities: []string{"find_symbol"}})
	if got := route.Candidates; len(got) != 6 {
		t.Fatalf("planning route = %#v", route)
	}
}

func TestCatalogDiscoveryIsBoundedAndDeterministic(t *testing.T) {
	registry := NewRegistry()
	for index := 0; index < 20; index++ {
		if err := registry.Register(catalogTool{name: fmt.Sprintf("tool_%02d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	first, second := catalog.Discover("tool", 3), catalog.Discover("tool", 3)
	if len(first) != 3 || fmt.Sprint(first) != fmt.Sprint(second) || first[0].Name != "tool_00" {
		t.Fatalf("discovery = %#v / %#v", first, second)
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

func TestCatalogRoutesExecuteCommandForOpenAndPreviewActions(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"read_file", "write_file", "execute_command"} {
		caps := CapabilityReadWorkspace
		meta := ToolMetadata{CanonicalName: name, Family: "workspace", CapabilityTags: []string{"workspace"}}
		if name == "execute_command" {
			caps |= CapabilityExecuteProcess
			meta.Family, meta.CapabilityTags = "shell", []string{"shell.execute", "terminal", "command", "exec", "run", "open", "launch", "start", "serve", "process", "browse", "preview"}
		}
		if err := registry.Register(catalogTool{name: name, caps: caps}, meta); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, prompt := range []string{"open the meal tracker", "launch in chrome", "start the dev server", "preview index.html"} {
		route := catalog.Route(ToolRouteProfile{Mode: ToolModeNormal, Objective: prompt})
		found := false
		for _, candidate := range route.Candidates {
			if candidate.Tool.Name == "execute_command" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("prompt %q failed to route execute_command: %#v", prompt, route)
		}
	}
}

func TestCatalogBoundsAutomaticActivation(t *testing.T) {
	registry := NewRegistry()
	for index := 0; index < 500; index++ {
		if err := registry.Register(catalogTool{name: fmt.Sprintf("tool_%03d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	route := catalog.Route(ToolRouteProfile{Mode: ToolModeNormal, Objective: "tool"})
	if len(route.Candidates) != maxAutomaticToolSchemas || len(route.Rejected) != 500-maxAutomaticToolSchemas {
		t.Fatalf("unbounded automatic route: candidates=%d rejected=%d", len(route.Candidates), len(route.Rejected))
	}
}

func TestCatalogExecutionExposesEveryEligibleTool(t *testing.T) {
	registry := NewRegistry()
	for _, tool := range []catalogTool{
		{name: "read_file", caps: CapabilityReadWorkspace},
		{name: "write_file", caps: CapabilityWriteWorkspace},
		{name: "execute_command", caps: CapabilityExecuteProcess},
	} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	route := catalog.Route(ToolRouteProfile{Mode: ToolModeExecution})
	got := make([]string, 0, len(route.Candidates))
	for _, candidate := range route.Candidates {
		if candidate.Reason != "execution" {
			t.Fatalf("candidate = %#v, want execution routing", candidate)
		}
		got = append(got, candidate.Tool.Name)
	}
	if strings.Join(got, ",") != "execute_command,read_file,write_file" {
		t.Fatalf("execution route = %v", got)
	}
}

func BenchmarkCatalogRoute500(b *testing.B) {
	registry := NewRegistry()
	for index := 0; index < 500; index++ {
		_ = registry.Register(catalogTool{name: fmt.Sprintf("tool_%03d", index)})
	}
	catalog, err := registry.Catalog()
	if err != nil {
		b.Fatal(err)
	}
	profile := ToolRouteProfile{Mode: ToolModeNormal, Objective: "tool"}
	for b.Loop() {
		_ = catalog.Route(profile)
	}
}
