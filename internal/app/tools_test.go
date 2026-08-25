package app

import (
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestBuiltinToolsetIsMinimalAndPlanSafe(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registerBuiltinTools(registry); err != nil {
		t.Fatal(err)
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"create_file", "run_build", "run_tests", "run_formatter"} {
		if _, ok := catalog.Descriptor(removed); ok {
			t.Fatalf("redundant tool %q is still registered", removed)
		}
	}
	if _, ok := catalog.Descriptor("execute_command"); !ok {
		t.Fatal("canonical command tool is missing")
	}

	route := catalog.Route(tools.ToolRouteProfile{Mode: tools.ToolModePlanning, ReadOnly: true, ResearchOnly: true, RequestedCapabilities: []string{"git_status"}})
	visible := map[string]bool{}
	for _, candidate := range route.Candidates {
		visible[candidate.Tool.Name] = true
		if !candidate.Tool.PlanningSafe() {
			t.Fatalf("unsafe tool exposed in Plan Mode: %#v", candidate.Tool)
		}
	}
	for _, required := range []string{"read_file", "list_directory", "search_file_name", "repository_query", "discover_tools", "git_status"} {
		if !visible[required] {
			t.Fatalf("Plan Mode missing %q: %#v", required, route)
		}
	}
	for _, forbidden := range []string{"write_file", "delete_file", "execute_command", "web_fetch"} {
		if visible[forbidden] {
			t.Fatalf("Plan Mode exposed %q", forbidden)
		}
	}
}

func TestSubagentToolMetadataKeepsDelegationSafeAndControlsExclusive(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registerSubagentTools(registry, nil); err != nil {
		t.Fatal(err)
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	delegate, _ := catalog.Descriptor("subagent")
	list, _ := catalog.Descriptor("list_agents")
	send, _ := catalog.Descriptor("send_message")
	interrupt, _ := catalog.Descriptor("interrupt_agent")
	if !delegate.ParallelSafe || !list.ParallelSafe || send.ParallelSafe || interrupt.ParallelSafe {
		t.Fatalf("subagent scheduling metadata = delegate:%#v list:%#v send:%#v interrupt:%#v", delegate, list, send, interrupt)
	}
	route := catalog.Route(tools.ToolRouteProfile{Mode: tools.ToolModePlanning, ReadOnly: true, ResearchOnly: true})
	visible := map[string]bool{}
	for _, candidate := range route.Candidates {
		visible[candidate.Tool.Name] = true
	}
	for _, safe := range []string{"subagent", "list_agents", "wait_agent"} {
		if !visible[safe] {
			t.Fatalf("Plan Mode missing safe subagent control %q: %#v", safe, route)
		}
	}
	for _, exclusive := range []string{"send_message", "interrupt_agent"} {
		if visible[exclusive] {
			t.Fatalf("Plan Mode exposed mutating subagent control %q", exclusive)
		}
	}
}
