package providers

import (
	"context"
	"fmt"
)

// ProviderFactory defines the interface for selecting and building LLM provider clients.
type ProviderFactory interface {
	Create(ctx context.Context, providerName, model, endpoint, apiKey string) (Provider, error)
}

// Factory implements ProviderFactory.
type Factory struct{}

// NewFactory creates a new Factory instance.
func NewFactory() *Factory {
	return &Factory{}
}

// Create selects and builds the corresponding Provider implementation.
func (f *Factory) Create(ctx context.Context, providerName, model, endpoint, apiKey string) (Provider, error) {
	switch providerName {
	case "gemini":
		return NewGeminiProvider(ctx, apiKey, model, endpoint)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}
