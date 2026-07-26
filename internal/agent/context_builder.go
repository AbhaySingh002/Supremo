package agent

import (
	"context"
	"fmt"
	"os"
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
	systemInstructionsBudget  = systemStateBudget - workspaceMemoryBudget - conversationSummaryBudget - activePlanBudget
)

// RealContextBuilder builds prompts from the startup-loaded system instructions.
type RealContextBuilder struct {
	system    string
	workspace string
	memory    Memory
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
	return &RealContextBuilder{system: system, workspace: workspace, memory: memory}, nil
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
	plan, err := session.ActivePlan(cb.workspace)
	if err != nil {
		return nil, fmt.Errorf("load active plan: %w", err)
	}
	if plan != nil {
		system += "\n\n" + truncateTokens(plan.Context(), activePlanBudget)
	}
	return &models.Prompt{System: strings.TrimSpace(system), Messages: history}, nil
}
