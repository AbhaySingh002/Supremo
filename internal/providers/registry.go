package providers

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Factory instantiates a Provider client from credentials and runtime parameters.
type Factory func(ctx context.Context, apiKey, model, endpoint string) (Provider, error)

// ProviderRegistration is the complete CLI-facing provider contract. It keeps
// construction, defaults, and presentation together so adding a provider does
// not require parallel command or UI lists.
type ProviderRegistration struct {
	Type              string
	DisplayName       string
	Description       string
	DefaultModel      string
	DefaultEndpoint   string
	RequiresEndpoint  bool
	AllowsNamedRoutes bool
	Factory           Factory
}

// Registry maps provider type identifiers to their registrations.
type Registry struct {
	mu            sync.RWMutex
	registrations map[string]ProviderRegistration
}

// NewRegistry creates a new empty provider factory registry.
func NewRegistry() *Registry {
	return &Registry{
		registrations: make(map[string]ProviderRegistration),
	}
}

// Register adds one provider type (e.g. "openai", "gemini") to the registry.
// Route-qualified provider names belong in configuration, not in the registry.
func (r *Registry) Register(registration ProviderRegistration) error {
	if r == nil {
		return fmt.Errorf("provider registry is not initialized")
	}
	name := strings.ToLower(strings.TrimSpace(registration.Type))
	if name == "" {
		return fmt.Errorf("provider type is required")
	}
	if strings.Contains(name, ":") {
		return fmt.Errorf("provider type %q must not include a route", name)
	}
	if strings.TrimSpace(registration.DisplayName) == "" || strings.TrimSpace(registration.Description) == "" {
		return fmt.Errorf("provider %q requires display name and description", name)
	}
	if registration.Factory == nil {
		return fmt.Errorf("provider factory for %q is required", name)
	}
	registration.Type = name
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.registrations[name]; exists {
		return fmt.Errorf("provider type %q is already registered", name)
	}
	r.registrations[name] = registration
	return nil
}

// Has returns true if a provider type is registered.
func (r *Registry) Has(providerName string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ptype := strings.ToLower(providerType(providerName))
	_, ok := r.registrations[ptype]
	return ok
}

// Create instantiates a Provider using the registered factory for providerName.
func (r *Registry) Create(ctx context.Context, providerName, apiKey, model, endpoint string) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("provider registry is not initialized")
	}
	r.mu.RLock()
	ptype := strings.ToLower(providerType(providerName))
	registration, ok := r.registrations[ptype]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
	return registration.Factory(ctx, apiKey, model, endpoint)
}

// Registration returns the provider registration for a type or named route.
func (r *Registry) Registration(providerName string) (ProviderRegistration, bool) {
	if r == nil {
		return ProviderRegistration{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	registration, ok := r.registrations[strings.ToLower(providerType(providerName))]
	return registration, ok
}

// Registrations returns a deterministic snapshot for CLI presentation.
func (r *Registry) Registrations() []ProviderRegistration {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	registrations := make([]ProviderRegistration, 0, len(r.registrations))
	for _, registration := range r.registrations {
		registrations = append(registrations, registration)
	}
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].Type < registrations[j].Type })
	return registrations
}

// RegisteredTypes returns a slice of all registered provider type names.
func (r *Registry) RegisteredTypes() []string {
	registrations := r.Registrations()
	types := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		types = append(types, registration.Type)
	}
	return types
}

// RegisterBuiltins mounts all standard provider factories into the target registry.
func RegisterBuiltins(r *Registry) error {
	if r == nil {
		return fmt.Errorf("provider registry is not initialized")
	}
	for _, entry := range []ProviderRegistration{
		{Type: "gemini", DisplayName: "Google Gemini", Description: "Gemini API", DefaultModel: defaultGeminiModel, Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewGeminiProvider(ctx, apiKey, model, endpoint)
		}},
		{Type: "openai", DisplayName: "OpenAI", Description: "OpenAI Responses", Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewOpenAIProvider(ctx, apiKey, model, endpoint)
		}},
		{Type: "anthropic", DisplayName: "Anthropic", Description: "Claude Messages", DefaultEndpoint: anthropicEndpoint, Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewAnthropicProvider(ctx, apiKey, model, endpoint)
		}},
		{Type: "openrouter", DisplayName: "OpenRouter", Description: "OpenAI-compatible routing", DefaultEndpoint: openRouterEndpoint, Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewOpenRouterProvider(ctx, apiKey, model, endpoint)
		}},
		{Type: "groq", DisplayName: "Groq", Description: "Fast OpenAI-compatible API", DefaultModel: defaultGroqModel, DefaultEndpoint: groqEndpoint, Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewGroqProvider(ctx, apiKey, model, endpoint)
		}},
		{Type: "nvidia", DisplayName: "NVIDIA NIM", Description: "NVIDIA hosted models", DefaultModel: defaultNVIDIAModel, DefaultEndpoint: nvidiaEndpoint, Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewNVIDIAProvider(ctx, apiKey, model, endpoint)
		}},
		{Type: "mistral", DisplayName: "Mistral", Description: "Mistral Conversations", DefaultModel: defaultMistralModel, DefaultEndpoint: mistralEndpoint, Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewMistralProvider(ctx, apiKey, model, endpoint)
		}},
		{Type: "openai-compatible", DisplayName: "OpenAI-compatible", Description: "Custom OpenAI-compatible endpoint", RequiresEndpoint: true, AllowsNamedRoutes: true, Factory: func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
			return NewOpenAICompatibleProvider(ctx, apiKey, model, endpoint)
		}},
	} {
		if err := r.Register(entry); err != nil {
			return fmt.Errorf("register built-in provider %q: %w", entry.Type, err)
		}
	}
	return nil
}
