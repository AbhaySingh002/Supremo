package providers

import (
	"context"
	"fmt"
)

// NewProvider builds the configured provider client.
func NewProvider(ctx context.Context, providerName, model, endpoint, apiKey string) (Provider, error) {
	switch providerName {
	case "gemini":
		return NewGeminiProvider(ctx, apiKey, model, endpoint)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}
