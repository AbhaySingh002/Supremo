package app

import (
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/capabilities/plan"
	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/filesystem"
	"github.com/AbhaySingh002/supremo/internal/tools/git"
	"github.com/AbhaySingh002/supremo/internal/tools/interaction"
	"github.com/AbhaySingh002/supremo/internal/tools/search"
	subagenttools "github.com/AbhaySingh002/supremo/internal/tools/subagent"
	"github.com/AbhaySingh002/supremo/internal/tools/terminal"
	"github.com/AbhaySingh002/supremo/internal/tools/web"
)

func registerBuiltinTools(registry *tools.Registry) error {
	entries := []struct {
		tool tools.Tool
		meta tools.ToolMetadata
	}{
		{&filesystem.ReadFile{}, inspectFS("read_file", true, false, true, true)},
		{&filesystem.FileInfo{}, inspectFS("file_info", true, true, false, false)},
		{&filesystem.ListDirectory{}, inspectFS("list_directory", true, true, true, false)},
		{&filesystem.CreateDirectory{}, writeFS("create_directory", false)},
		{&filesystem.WriteFile{}, writeFS("write_file", true)},
		{&filesystem.ReplaceInFile{}, writeFS("replace_in_file", true)},
		{&filesystem.RenameFile{}, writeFS("rename_file", true)},
		{&filesystem.DeleteFile{}, tools.ToolMetadata{CanonicalName: "delete_file", Family: "filesystem", CapabilityTags: []string{"filesystem.write", "delete", "remove", "clean"}, Access: tools.ToolAccessDestructive, SideEffect: tools.ToolSideEffectWorkspace, RequiresApproval: true}},
		{&search.FindReferences{}, inspectRepo("find_references", false)},
		{&search.FindSymbol{}, inspectRepo("find_symbol", false)},
		{&search.SearchFileName{}, inspectRepo("search_file_name", true)},
		{&search.SearchText{}, inspectRepo("search_text", false)},
		{&search.RepositoryQuery{}, inspectRepo("repository_query", true)},
		{&git.GitLog{}, tools.ToolMetadata{CanonicalName: "git_log", Family: "git", CapabilityTags: []string{"git", "diff", "status", "log", "commit"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone}},
		{&git.GitDiff{}, tools.ToolMetadata{CanonicalName: "git_diff", Family: "git", CapabilityTags: []string{"git", "diff", "status", "log", "commit"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, Inspection: true, PersistCallObservation: true}},
		{&git.GitStatus{}, tools.ToolMetadata{CanonicalName: "git_status", Family: "git", CapabilityTags: []string{"git", "diff", "status", "log", "commit"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, Inspection: true, PersistCallObservation: true}},
		{&terminal.ExecuteCommand{}, tools.ToolMetadata{CanonicalName: "execute_command", Family: "shell", CapabilityTags: []string{"shell.execute", "terminal", "command", "exec", "run", "open", "launch", "start", "serve", "process", "browse", "preview"}, Access: tools.ToolAccessDestructive, SideEffect: tools.ToolSideEffectProcess, RequiresApproval: true}},
		{&web.WebFetch{}, tools.ToolMetadata{CanonicalName: "web_fetch", Family: "web", CapabilityTags: []string{"web.search", "fetch", "http", "url", "download"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNetwork, ParallelSafe: true}},
		{&tools.TodoWrite{}, tools.ToolMetadata{CanonicalName: "todo_write", Family: "task", CapabilityTags: []string{"task", "todo", "checklist"}, Access: tools.ToolAccessWrite, SideEffect: tools.ToolSideEffectWorkspace, PlanningCore: true}},
	}
	for _, entry := range entries {
		if err := registry.Register(entry.tool, entry.meta); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", entry.tool.Name(), err)
		}
	}
	discover := tools.NewDiscoverTools(registry)
	if err := registry.Register(discover, tools.ToolMetadata{CanonicalName: "discover_tools", Family: "tool_discovery", CapabilityTags: []string{"tool.discovery"}, Access: tools.ToolAccessRead, Bootstrap: true, PlanningCore: true}); err != nil {
		return fmt.Errorf("failed to register tool discovery: %w", err)
	}
	return nil
}

func registerSubagentTools(registry *tools.Registry, manager *agent.SubagentManager) error {
	entries := []struct {
		tool tools.Tool
		meta tools.ToolMetadata
	}{
		{&subagenttools.Start{Manager: manager}, tools.ToolMetadata{CanonicalName: "subagent", Family: "delegation", CapabilityTags: []string{"agent.delegate", "subagent", "parallel"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, Bootstrap: true, ParallelSafe: true}},
		{&subagenttools.List{Manager: manager}, tools.ToolMetadata{CanonicalName: "list_agents", Family: "delegation", CapabilityTags: []string{"agent.list", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, Bootstrap: true, ParallelSafe: true}},
		{&subagenttools.Send{Manager: manager}, tools.ToolMetadata{CanonicalName: "send_message", Family: "delegation", CapabilityTags: []string{"agent.message", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNetwork, Bootstrap: true}},
		{&subagenttools.Wait{Manager: manager}, tools.ToolMetadata{CanonicalName: "wait_agent", Family: "delegation", CapabilityTags: []string{"agent.wait", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, Bootstrap: true}},
		{&subagenttools.Interrupt{Manager: manager}, tools.ToolMetadata{CanonicalName: "interrupt_agent", Family: "delegation", CapabilityTags: []string{"agent.interrupt", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectProcess, Bootstrap: true}},
	}
	for _, entry := range entries {
		if err := registry.Register(entry.tool, entry.meta); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", entry.tool.Name(), err)
		}
	}
	return nil
}

func registerPlanAndInteractionTools(registry *tools.Registry, qService *questions.Service) error {
	askQuestionTool := interaction.NewAskUserQuestion(qService)
	if err := registry.Register(askQuestionTool, tools.ToolMetadata{
		CanonicalName:  "ask_user_question",
		Family:         "interaction",
		CapabilityTags: []string{"interaction.question", "ask", "question", "clarify"},
		Access:         tools.ToolAccessRead,
		Inspection:     true,
		PlanningCore:   true,
		Bootstrap:      true,
	}); err != nil {
		return fmt.Errorf("failed to register ask_user_question: %w", err)
	}

	exitPlanTool := plan.NewExitPlanMode(qService)
	if err := registry.Register(exitPlanTool, tools.ToolMetadata{
		CanonicalName:  "exit_plan_mode",
		Family:         "planning",
		CapabilityTags: []string{"planning.exit", "plan", "submit_plan", "approve_plan"},
		Access:         tools.ToolAccessRead,
		Inspection:     true,
		PlanningCore:   true,
		Bootstrap:      true,
		SupportedModes: []tools.ToolMode{tools.ToolModePlanning},
	}); err != nil {
		return fmt.Errorf("failed to register exit_plan_mode: %w", err)
	}
	return nil
}

func inspectFS(name string, inspection, persist, planningCore, bootstrap bool) tools.ToolMetadata {
	return tools.ToolMetadata{CanonicalName: name, Family: "filesystem", CapabilityTags: []string{"filesystem.read", "read", "view", "list", "inspect", "show", "cat"}, Access: tools.ToolAccessRead, Inspection: inspection, PersistCallObservation: persist, PlanningCore: planningCore, Bootstrap: bootstrap, ParallelSafe: true}
}

func writeFS(name string, batmanManifest bool) tools.ToolMetadata {
	return tools.ToolMetadata{CanonicalName: name, Family: "filesystem", CapabilityTags: []string{"filesystem.write", "write", "create", "edit", "update", "modify", "save"}, Access: tools.ToolAccessWrite, SideEffect: tools.ToolSideEffectWorkspace, RequiresApproval: true, BatmanManifest: batmanManifest}
}

func inspectRepo(name string, planningCore bool) tools.ToolMetadata {
	return tools.ToolMetadata{CanonicalName: name, Family: "repository", CapabilityTags: []string{"repository.search", "search", "find", "grep", "query"}, Access: tools.ToolAccessRead, Inspection: true, PersistCallObservation: true, PlanningCore: planningCore, Bootstrap: name == "repository_query", ParallelSafe: true}
}
