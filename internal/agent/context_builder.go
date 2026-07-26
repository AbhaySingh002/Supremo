package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/prompts"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

const (
	promptTokenBudget         = 20_000
	systemStateBudget         = promptTokenBudget * 30 / 100
	messageWindowBudget       = promptTokenBudget * 50 / 100
	toolBufferBudget          = promptTokenBudget * 20 / 100
	workspaceMemoryBudget     = systemStateBudget / 4
	conversationSummaryBudget = systemStateBudget / 6
	activePlanBudget          = systemStateBudget / 12
	projectInstructionsBudget = systemStateBudget / 6
	systemInstructionsBudget  = systemStateBudget - workspaceMemoryBudget - conversationSummaryBudget - activePlanBudget - projectInstructionsBudget
)

// RealContextBuilder builds prompts from the startup-loaded system instructions.
type RealContextBuilder struct {
	system    string
	workspace string
	memory    Memory
	project   string
}

// NewRealContextBuilder creates a new RealContextBuilder.
func NewRealContextBuilder(templateDir string, registry *tools.Registry, memory Memory) (*RealContextBuilder, error) {
	system, err := prompts.LoadSystem(templateDir, registry)
	if err != nil {
		return nil, err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &RealContextBuilder{system: system, workspace: workspace, memory: memory, project: loadProjectInstructions(workspace)}, nil
}

// Build implements agent.ContextBuilder.
func (cb *RealContextBuilder) Build(ctx context.Context, session *Session, _ string, _ *State) (*models.Prompt, error) {
	persistent, err := cb.memory.PersistentContext(workspaceMemoryBudget)
	if err != nil {
		return nil, err
	}
	history, err := cb.memory.GetWindow(ctx, session.ID, messageWindowBudget, toolBufferBudget)
	if err != nil {
		return nil, err
	}
	summary, err := cb.memory.GetSummary(ctx, session.ID, conversationSummaryBudget)
	if err != nil {
		return nil, err
	}
	system := truncateTokens(cb.system, systemInstructionsBudget)
	if persistent != "" {
		system += "\n\n" + persistent
	}
	if summary != "" {
		system += "\n\n# Conversation Summary\n" + summary
	}
	if cb.project != "" {
		system += "\n\n# Project Instructions\n" + truncateTokens(cb.project, projectInstructionsBudget)
	}
	plan, err := session.ActivePlan(cb.workspace)
	if err != nil {
		return nil, fmt.Errorf("load active plan: %w", err)
	}
	if plan != nil {
		system += "\n\n" + truncateTokens(plan.Context(), activePlanBudget)
	}
	return &models.Prompt{System: strings.TrimSpace(system), Messages: history}, nil
}

func loadProjectInstructions(workspace string) string {
	for _, name := range []string{"SUPREMO.md", "AGENTS.md"} {
		content, err := os.ReadFile(filepath.Join(workspace, name))
		if err == nil {
			return string(content)
		}
	}
	return ""
}
