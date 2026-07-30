package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/prompts"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

const (
	maxPromptTokenBudget = 131_072
	minPromptTokenBudget = 1_024
)

// RealContextBuilder builds prompts from the startup-loaded system instructions.
type RealContextBuilder struct {
	system       string
	workspace    string
	memory       Memory
	project      string
	contextLimit func() int
}

// NewRealContextBuilder creates a new RealContextBuilder.
func NewRealContextBuilder(registry *tools.Registry, memory Memory, contextLimit func() int) (*RealContextBuilder, error) {
	system, err := prompts.LoadSystem(registry)
	if err != nil {
		return nil, err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &RealContextBuilder{system: system, workspace: workspace, memory: memory, project: loadProjectInstructions(workspace), contextLimit: contextLimit}, nil
}

// Build implements agent.ContextBuilder.
func (cb *RealContextBuilder) Build(ctx context.Context, session *Session) (*models.Prompt, error) {
	promptBudget := maxPromptTokenBudget
	contextLimit := 0
	if cb.contextLimit != nil {
		contextLimit = cb.contextLimit()
	}
	if contextLimit > 0 && contextLimit < maxPromptTokenBudget {
		// Keep room for the model's response instead of filling its whole advertised context.
		promptBudget = max(minPromptTokenBudget, contextLimit*3/4)
	}
	systemStateBudget := promptBudget * 30 / 100
	messageWindowBudget := promptBudget * 50 / 100
	toolBufferBudget := promptBudget * 20 / 100
	workspaceMemoryBudget := systemStateBudget / 4
	conversationSummaryBudget := systemStateBudget / 6
	activePlanBudget := systemStateBudget / 12
	projectInstructionsBudget := systemStateBudget / 6
	systemInstructionsBudget := systemStateBudget - workspaceMemoryBudget - conversationSummaryBudget - activePlanBudget - projectInstructionsBudget
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
