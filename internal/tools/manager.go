package tools

import "context"

// Manager handles tool execution with validation and registry lookup.
type Manager struct {
	registry *Registry
}

// NewManager creates a new Manager with the given tool registry.
func NewManager(r *Registry) *Manager {
	return &Manager{
		registry: r,
	}
}

// Execute retrieves a tool by name, validates the input against its schema,
// and executes the tool with the provided context and input.
func (m *Manager) Execute(
	ctx context.Context,
	name string,
	input any,
) (*ToolResult, error) {

	tool, err := m.registry.Get(name)

	if err != nil {
		return nil, err
	}

	return tool.Execute(ctx, input)
}
