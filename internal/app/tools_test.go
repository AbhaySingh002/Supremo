package app

import (
	"sort"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestBuiltinToolsetIsMinimalAndPlanSafe(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registerBuiltinTools(registry); err != nil {
		t.Fatal(err)
	}
	if err := registerSubagentTools(registry, nil); err != nil {
		t.Fatal(err)
	}
	if err := registerPlanAndInteractionTools(registry, nil); err != nil {
		t.Fatal(err)
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	registered := make([]string, 0, len(catalog.Tools))
	for _, descriptor := range catalog.Tools {
		registered = append(registered, descriptor.Name)
	}
	sort.Strings(registered)
	want := []string{"ask_user_question", "delete_file", "execute_command", "exit_plan_mode", "interrupt_agent", "list_agents", "read_file", "rename_file", "replace_in_file", "send_message", "subagent", "todo_write", "wait_agent", "web_fetch", "write_file"}
	if len(registered) != len(want) {
		t.Fatalf("registered tools = %v, want %v", registered, want)
	}
	for i := range want {
		if registered[i] != want[i] {
			t.Fatalf("registered tools = %v, want %v", registered, want)
		}
	}
	for _, removed := range []string{
		"create_directory", "discover_tools", "file_info", "find_references", "find_symbol", "git_diff", "git_log", "git_status",
		"list_directory", "repository_query", "search_file_name", "search_text", "glob", "grep",
	} {
		if _, ok := catalog.Descriptor(removed); ok {
			t.Fatalf("removed tool %q is still registered", removed)
		}
	}

	route := catalog.Route(tools.ToolRouteProfile{Mode: tools.ToolModePlanning, ReadOnly: true, ResearchOnly: true})
	visible := map[string]bool{}
	for _, candidate := range route.Candidates {
		visible[candidate.Tool.Name] = true
		if !candidate.Tool.PlanningSafe() {
			t.Fatalf("unsafe tool exposed in Plan Mode: %#v", candidate.Tool)
		}
	}
	for _, required := range []string{"ask_user_question", "exit_plan_mode", "list_agents", "read_file", "subagent", "wait_agent"} {
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
