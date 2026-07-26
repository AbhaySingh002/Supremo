package providers

import "sync"

// RuntimeConfig manages the active LLM provider configuration and initialized client reference.
type RuntimeConfig struct {
	mu           sync.RWMutex
	providerName string
	model        string
	endpoint     string
	apiKey       string
	activeClient Provider
	metadata     Metadata
	usage        Usage
}

// NewRuntimeConfig builds a new RuntimeConfig reference container.
func NewRuntimeConfig(providerName, model, endpoint, apiKey string, client Provider) *RuntimeConfig {
	return &RuntimeConfig{
		providerName: providerName,
		model:        model,
		endpoint:     endpoint,
		apiKey:       apiKey,
		activeClient: client,
	}
}

// Get reads the active configurations and client reference.
func (r *RuntimeConfig) Get() (string, string, string, string, Provider) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providerName, r.model, r.endpoint, r.apiKey, r.activeClient
}

// GetClient returns the current Provider client.
func (r *RuntimeConfig) GetClient() Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.activeClient
}

// CredentialConfigured reports whether the active provider has a non-placeholder API key.
func (r *RuntimeConfig) CredentialConfigured() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return credentialConfigured(r.apiKey)
}

// Metadata returns the cached provider capabilities and account information.
func (r *RuntimeConfig) Metadata() Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metadata
}

func (r *RuntimeConfig) setMetadata(metadata Metadata) {
	r.metadata = metadata
}

// ContextLimit returns the selected model's advertised context window, if known.
func (r *RuntimeConfig) ContextLimit() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	model, ok := findModel(r.metadata.Models, r.model)
	if !ok {
		return 0
	}
	return model.ContextLength
}

func (r *RuntimeConfig) addUsage(usage Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage.InputTokens += usage.InputTokens
	r.usage.OutputTokens += usage.OutputTokens
	if usage.CostUSD != nil {
		if r.usage.CostUSD == nil {
			cost := 0.0
			r.usage.CostUSD = &cost
		}
		*r.usage.CostUSD += *usage.CostUSD
	}
}

// Usage returns the accumulated usage for the current Supremo runtime.
func (r *RuntimeConfig) Usage() Usage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usage
}
