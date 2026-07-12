package prompts

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// MessageTemplate maps an LLM message role to the template names used to construct its content.
type MessageTemplate struct {
	Role          models.Role
	TemplateNames []string
}

// PromptDocument represents the compiled intermediate prompt.
type PromptDocument struct {
	System   string
	Messages []models.Message
}

// ToPrompt converts the intermediate document into the final provider-facing models.Prompt.
func (d *PromptDocument) ToPrompt() *models.Prompt {
	return &models.Prompt{
		System:   d.System,
		Messages: d.Messages,
	}
}

// Builder orchestrates the prompt assembly process.
type Builder struct {
	loader       *Loader
	registry     *Registry
	toolRegistry *tools.Registry
}

// NewBuilder constructs a new Builder with a Loader, Registry, and ToolRegistry.
func NewBuilder(loader *Loader, registry *Registry, toolRegistry *tools.Registry) *Builder {
	return &Builder{
		loader:       loader,
		registry:     registry,
		toolRegistry: toolRegistry,
	}
}

// Build compiles templates into a final PromptDocument structure by executing templates,
// injecting tool documentation after the tools template, and concatenating.
func (b *Builder) Build(systemTemplates []string, messageTemplates []MessageTemplate, vars any) (*PromptDocument, error) {
	// 1. Build system instructions
	var systemBuilder strings.Builder
	for i, name := range systemTemplates {
		if !b.registry.IsRegistered(name) {
			return nil, fmt.Errorf("system template %q is not registered in the registry", name)
		}
		content, err := b.loader.Load(name)
		if err != nil {
			return nil, fmt.Errorf("failed to load system template %q: %w", name, err)
		}
		rendered, err := RenderTemplate(name, content, vars)
		if err != nil {
			return nil, fmt.Errorf("failed to render system template %q: %w", name, err)
		}
		if name == TemplateTools {
			toolDocs, err := GenerateToolDocs(b.toolRegistry)
			if err != nil {
				return nil, fmt.Errorf("failed to generate tool docs: %w", err)
			}
			if toolDocs != "" {
				rendered = rendered + "\n" + toolDocs
			}
		}
		if i > 0 {
			systemBuilder.WriteString("\n")
		}
		systemBuilder.WriteString(rendered)
	}
	systemPrompt := systemBuilder.String()

	// 2. Build chat messages
	messages := make([]models.Message, 0, len(messageTemplates))
	for _, mt := range messageTemplates {
		var contentBuilder strings.Builder
		for j, name := range mt.TemplateNames {
			if !b.registry.IsRegistered(name) {
				return nil, fmt.Errorf("message template %q is not registered in the registry", name)
			}
			content, err := b.loader.Load(name)
			if err != nil {
				return nil, fmt.Errorf("failed to load message template %q: %w", name, err)
			}
			rendered, err := RenderTemplate(name, content, vars)
			if err != nil {
				return nil, fmt.Errorf("failed to render message template %q: %w", name, err)
			}
			if name == TemplateTools {
				toolDocs, err := GenerateToolDocs(b.toolRegistry)
				if err != nil {
					return nil, fmt.Errorf("failed to generate tool docs: %w", err)
				}
				if toolDocs != "" {
					rendered = rendered + "\n" + toolDocs
				}
			}
			if j > 0 {
				contentBuilder.WriteString("\n")
			}
			contentBuilder.WriteString(rendered)
		}
		messages = append(messages, models.Message{
			Role:    mt.Role,
			Content: contentBuilder.String(),
		})
	}

	return &PromptDocument{
		System:   systemPrompt,
		Messages: messages,
	}, nil
}

// RenderTemplate parses and executes a single template content using Go's text/template.
func RenderTemplate(name, content string, vars any) (string, error) {
	tmpl, err := template.New(name).Delims(PlaceholderStart, PlaceholderEnd).Parse(content)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}
