package app

import (
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/capabilities/plan"
	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/filesystem"
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
		{&filesystem.ReadFile{}, inspectFS("read_file", true, false)},
		{&filesystem.WriteFile{}, writeFS("write_file", true)},
		{&filesystem.ReplaceInFile{}, writeFS("replace_in_file", true)},
		{&filesystem.RenameFile{}, writeFS("rename_file", true)},
		{&filesystem.DeleteFile{}, tools.ToolMetadata{CanonicalName: "delete_file", Family: "filesystem", CapabilityTags: []string{"filesystem.write", "delete", "remove", "clean"}, Access: tools.ToolAccessDestructive, SideEffect: tools.ToolSideEffectWorkspace, RequiresApproval: true}},
		{&search.Glob{}, inspectRepo("glob")},
		{&search.Grep{}, inspectRepo("grep")},
		{&terminal.ExecuteCommand{}, tools.ToolMetadata{CanonicalName: "execute_command", Family: "shell", CapabilityTags: []string{"shell.execute", "terminal", "command", "exec", "run", "open", "launch", "start", "serve", "process", "browse", "preview"}, Access: tools.ToolAccessDestructive, SideEffect: tools.ToolSideEffectProcess, RequiresApproval: true}},
		{&web.WebFetch{}, tools.ToolMetadata{CanonicalName: "web_fetch", Family: "web", CapabilityTags: []string{"web.search", "fetch", "http", "url", "download"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNetwork, ParallelSafe: true}},
		{&tools.TodoWrite{}, tools.ToolMetadata{CanonicalName: "todo_write", Family: "task", CapabilityTags: []string{"task", "todo", "checklist"}, Access: tools.ToolAccessWrite, SideEffect: tools.ToolSideEffectWorkspace}},
	}
	for _, entry := range entries {
		if err := registry.Register(entry.tool, entry.meta); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", entry.tool.Name(), err)
		}
	}
	return nil
}

func registerSubagentTools(registry *tools.Registry, manager *agent.SubagentManager) error {
	entries := []struct {
		tool tools.Tool
		meta tools.ToolMetadata
	}{
		{&subagenttools.Start{Manager: manager}, tools.ToolMetadata{CanonicalName: "subagent", Family: "delegation", CapabilityTags: []string{"agent.delegate", "subagent", "parallel"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, ParallelSafe: true}},
		{&subagenttools.List{Manager: manager}, tools.ToolMetadata{CanonicalName: "list_agents", Family: "delegation", CapabilityTags: []string{"agent.list", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone, ParallelSafe: true}},
		{&subagenttools.Send{Manager: manager}, tools.ToolMetadata{CanonicalName: "send_message", Family: "delegation", CapabilityTags: []string{"agent.message", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNetwork}},
		{&subagenttools.Wait{Manager: manager}, tools.ToolMetadata{CanonicalName: "wait_agent", Family: "delegation", CapabilityTags: []string{"agent.wait", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectNone}},
		{&subagenttools.Interrupt{Manager: manager}, tools.ToolMetadata{CanonicalName: "interrupt_agent", Family: "delegation", CapabilityTags: []string{"agent.interrupt", "subagent"}, Access: tools.ToolAccessRead, SideEffect: tools.ToolSideEffectProcess}},
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
		SupportedModes: []tools.ToolMode{tools.ToolModePlanning},
	}); err != nil {
		return fmt.Errorf("failed to register exit_plan_mode: %w", err)
	}
	return nil
}

func inspectFS(name string, inspection, persist bool) tools.ToolMetadata {
	return tools.ToolMetadata{CanonicalName: name, Family: "filesystem", CapabilityTags: []string{"filesystem.read", "read", "view", "inspect", "show", "cat"}, Access: tools.ToolAccessRead, Inspection: inspection, PersistCallObservation: persist, ParallelSafe: true}
}

func writeFS(name string, batmanManifest bool) tools.ToolMetadata {
	return tools.ToolMetadata{CanonicalName: name, Family: "filesystem", CapabilityTags: []string{"filesystem.write", "write", "create", "edit", "update", "modify", "save"}, Access: tools.ToolAccessWrite, SideEffect: tools.ToolSideEffectWorkspace, RequiresApproval: true, BatmanManifest: batmanManifest}
}

func inspectRepo(name string) tools.ToolMetadata {
	return tools.ToolMetadata{CanonicalName: name, Family: "repository", CapabilityTags: []string{"repository.search", "search", "find", "grep", "glob"}, Access: tools.ToolAccessRead, Inspection: true, PersistCallObservation: true, ParallelSafe: true}
}
