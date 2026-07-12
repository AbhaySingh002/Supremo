package tools

import "fmt"

// Registry manages a collection of tools indexed by their names.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates and initializes a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry. Returns an error if a tool with the same name already exists.
func (r *Registry) Register(tool Tool) error {

	if _, exists := r.tools[tool.Name()]; exists {
		return fmt.Errorf("%s already exists", tool.Name())
	}

	r.tools[tool.Name()] = tool
	return nil
}

// Get retrieves a tool by its name. Returns an error if the tool is not found.
func (r *Registry) Get(name string) (Tool, error) {

	tool, ok := r.tools[name]

	if !ok {
		return nil, fmt.Errorf("tool not found")
	}

	return tool, nil
}
