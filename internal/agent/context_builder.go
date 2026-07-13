package agent

import (
	"context"
	"time"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/prompts"
)

// RealContextBuilder implements ContextBuilder using the prompts.Builder system.
type RealContextBuilder struct {
	builder        *prompts.Builder
	runtimeManager RuntimeManager
	memory         Memory
}

// NewRealContextBuilder creates a new RealContextBuilder.
func NewRealContextBuilder(builder *prompts.Builder, runtimeManager RuntimeManager, memory Memory) *RealContextBuilder {
	return &RealContextBuilder{
		builder:        builder,
		runtimeManager: runtimeManager,
		memory:         memory,
	}
}

// Build implements agent.ContextBuilder using the prompt building system.
func (cb *RealContextBuilder) Build(ctx context.Context, session *Session, userInput string, state *State) (*models.Prompt, error) {
	// Build system prompt with all templates
	systemTemplates := []string{
		prompts.TemplateSystem,
		prompts.TemplateCoding,
		prompts.TemplateTools,
		prompts.TemplatePlanner,
		prompts.TemplateResponse,
	}

	// Build message templates - empty for now, we'll add user input manually
	messageTemplates := []prompts.MessageTemplate{}

	// Prepare template variables
	vars := map[string]any{
		"SYSTEM":    "Supremo",
		"TOOLS":     "",
		"WORKSPACE": cb.runtimeManager.GetWorkingDirectory(),
		"MODEL":     "gemini-3.5-flash",
		"TASK":      userInput,
		"DATE":      time.Now().Format("2006-01-02"),
	}

	// Build the prompt document with system templates only
	doc, err := cb.builder.Build(systemTemplates, messageTemplates, vars)
	if err != nil {
		return nil, err
	}

	// Add conversation history from memory
	history, err := cb.memory.Get(ctx, session.ID)
	if err != nil {
		return nil, err
	}

	// Add history to messages (already includes current user input from loop.go)
	doc.Messages = append(doc.Messages, history...)

	return doc.ToPrompt(), nil
}
