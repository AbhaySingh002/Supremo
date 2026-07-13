package prompts

import "sync"

// Registry keeps track of registered prompt template names.
type Registry struct {
	mu        sync.RWMutex
	templates map[string]bool
}

// NewRegistry creates a new Registry pre-populated with built-in templates.
func NewRegistry() *Registry {
	r := &Registry{
		templates: make(map[string]bool),
	}
	// Register default built-in templates
	r.Register(TemplateSystem)
	r.Register(TemplateCoding)
	r.Register(TemplateTools)
	r.Register(TemplatePlanner)
	r.Register(TemplateResponse)
	return r
}

// Register adds a new template name to the registry.
func (r *Registry) Register(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.templates[name] = true
}

// IsRegistered checks if a template name is registered.
func (r *Registry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.templates[name]
}

// List returns a slice of all registered template names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]string, 0, len(r.templates))
	for name := range r.templates {
		list = append(list, name)
	}
	return list
}
