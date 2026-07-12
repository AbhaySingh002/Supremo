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

// Update changes active details.
func (r *RuntimeConfig) Update(providerName, model, endpoint, apiKey string, client Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providerName = providerName
	r.model = model
	r.endpoint = endpoint
	r.apiKey = apiKey
	r.activeClient = client
}
