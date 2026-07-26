package agent

import (
	"context"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/prompts"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

const (
	promptTokenBudget  = 20_000
	systemStateBudget  = promptTokenBudget * 30 / 100
	messageWindowBudget = promptTokenBudget * 50 / 100
	toolBufferBudget   = promptTokenBudget * 20 / 100
)

// RealContextBuilder builds prompts from the startup-loaded system instructions.
type RealContextBuilder struct {
	system string
	memory Memory
}

// NewRealContextBuilder creates a new RealContextBuilder.
func NewRealContextBuilder(templateDir string, registry *tools.Registry, memory Memory) (*RealContextBuilder, error) {
	system, err := prompts.LoadSystem(templateDir, registry)
	if err != nil {
		return nil, err
	}
	return &RealContextBuilder{system: system, memory: memory}, nil
}

// Build implements agent.ContextBuilder.
func (cb *RealContextBuilder) Build(ctx context.Context, session *Session, _ string, _ *State) (*models.Prompt, error) {
	persistent, err := cb.memory.PersistentContext(systemStateBudget / 3)
	if err != nil {
		return nil, err
	}
	summary, err := cb.memory.GetSummary(ctx, session.ID, systemStateBudget/6)
	if err != nil {
		return nil, err
	}
	history, err := cb.memory.GetWindow(ctx, session.ID, messageWindowBudget, toolBufferBudget)
	if err != nil {
		return nil, err
	}
	system := truncateTokens(cb.system, systemStateBudget-systemStateBudget/3-systemStateBudget/6)
	if persistent != "" {
		system += "\n\n" + persistent
	}
	if summary != "" {
		system += "\n\n# Conversation Summary\n" + summary
	}
	return &models.Prompt{System: strings.TrimSpace(system), Messages: history}, nil
}
