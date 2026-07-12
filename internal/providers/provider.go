package providers

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/models"
)

// Completion represents a structured response from an LLM provider.
type Completion struct {
	Raw          string `json:"raw"`
	FinishReason string `json:"finish_reason"`
}

// Provider defines the interface that all LLM adapters must implement.
type Provider interface {
	Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error)
}
