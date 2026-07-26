package providers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/AbhaySingh002/supremo/internal/models"
)

const anthropicEndpoint = "https://api.anthropic.com/v1"

// AnthropicProvider implements Claude's Messages API.
type AnthropicProvider struct {
	client   *http.Client
	endpoint string
	apiKey   string
	model    string
}

func NewAnthropicProvider(_ context.Context, apiKey, model, endpoint string) (*AnthropicProvider, error) {
	if endpoint == "" {
		endpoint = anthropicEndpoint
	}
	return &AnthropicProvider{client: &http.Client{Timeout: 60 * time.Second}, endpoint: endpoint, apiKey: apiKey, model: model}, nil
}

func (p *AnthropicProvider) headers() http.Header {
	headers := make(http.Header)
	headers.Set("x-api-key", p.apiKey)
	headers.Set("anthropic-version", "2023-06-01")
	return headers
}

func (p *AnthropicProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type request struct {
		Model     string    `json:"model"`
		MaxTokens int       `json:"max_tokens"`
		System    string    `json:"system,omitempty"`
		Messages  []message `json:"messages"`
	}
	type response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	messages := make([]message, 0, len(prompt.Messages))
	for _, msg := range prompt.Messages {
		role := "user"
		if msg.Role == models.RoleAssistant {
			role = "assistant"
		}
		messages = append(messages, message{Role: role, Content: msg.Content})
	}
	var responseBody response
	if err := doJSON(ctx, p.client, http.MethodPost, apiURL(p.endpoint, "messages"), "", p.headers(), request{Model: p.model, MaxTokens: 4096, System: prompt.System, Messages: messages}, &responseBody); err != nil {
		return nil, fmt.Errorf("anthropic execution: %w", err)
	}
	for _, block := range responseBody.Content {
		if block.Type == "text" && block.Text != "" {
			return &Completion{Raw: block.Text, FinishReason: responseBody.StopReason, Usage: Usage{InputTokens: responseBody.Usage.InputTokens, OutputTokens: responseBody.Usage.OutputTokens}}, nil
		}
	}
	return nil, fmt.Errorf("anthropic returned no text content")
}

func (p *AnthropicProvider) FetchMetadata(ctx context.Context) (Metadata, error) {
	var response struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := doJSON(ctx, p.client, http.MethodGet, apiURL(p.endpoint, "models"), "", p.headers(), nil, &response); err != nil {
		return Metadata{}, fmt.Errorf("list models: %w", err)
	}
	metadata := Metadata{Models: make([]ModelInfo, 0, len(response.Data)), FetchedAt: time.Now().UTC()}
	for _, item := range response.Data {
		metadata.Models = append(metadata.Models, ModelInfo{ID: item.ID, Name: item.DisplayName})
	}
	return metadata, nil
}
