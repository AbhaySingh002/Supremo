package providers

import (
	"context"
	"time"

	"github.com/AbhaySingh002/supremo/internal/models"
)

// Completion represents a structured response from an LLM provider.
type Completion struct {
	Raw          string `json:"raw"`
	FinishReason string `json:"finish_reason"`
	Usage        Usage  `json:"usage"`
}

// Usage is the provider-reported cost and token usage for one completion.
type Usage struct {
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
}

// ModelInfo is the provider metadata needed to choose and size a model at runtime.
type ModelInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int    `json:"context_length,omitempty"`
}

// AccountInfo is intentionally optional: not every provider exposes a balance to a normal API key.
type AccountInfo struct {
	TotalCredits float64 `json:"total_credits"`
	TotalUsage   float64 `json:"total_usage"`
}

// Metadata is cached locally so normal startup never needs a provider request.
type Metadata struct {
	Models    []ModelInfo  `json:"models"`
	Account   *AccountInfo `json:"account,omitempty"`
	FetchedAt time.Time    `json:"fetched_at"`
}

// Provider defines the interface that all LLM adapters must implement.
type Provider interface {
	Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error)
}

// StreamProvider is implemented by providers that can emit response text before completion.
// The callback receives ordered text deltas; callers must not retain provider state in it.
type StreamProvider interface {
	Stream(ctx context.Context, prompt *models.Prompt, receive func(string)) (*Completion, error)
}

// MetadataProvider is implemented when a provider can list its available models.
type MetadataProvider interface {
	FetchMetadata(ctx context.Context) (Metadata, error)
}
