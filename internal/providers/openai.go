package providers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

const (
	openAIEndpoint     = "https://api.openai.com/v1"
	openRouterEndpoint = "https://openrouter.ai/api/v1"
)

// OpenAIProvider implements the OpenAI chat-completions protocol. It also covers
// OpenRouter and self-hosted OpenAI-compatible servers.
type OpenAIProvider struct {
	client     *http.Client
	endpoint   string
	apiKey     string
	model      string
	openRouter bool
}

func NewOpenAIProvider(_ context.Context, apiKey, model, endpoint string) (*OpenAIProvider, error) {
	return newOpenAIProvider(apiKey, model, endpoint, false), nil
}

func NewOpenRouterProvider(_ context.Context, apiKey, model, endpoint string) (*OpenAIProvider, error) {
	return newOpenAIProvider(apiKey, model, endpoint, true), nil
}

func NewOpenAICompatibleProvider(_ context.Context, apiKey, model, endpoint string) (*OpenAIProvider, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("an endpoint is required for openai-compatible providers")
	}
	return newOpenAIProvider(apiKey, model, endpoint, false), nil
}

func newOpenAIProvider(apiKey, model, endpoint string, openRouter bool) *OpenAIProvider {
	if endpoint == "" {
		if openRouter {
			endpoint = openRouterEndpoint
		} else {
			endpoint = openAIEndpoint
		}
	}
	return &OpenAIProvider{client: &http.Client{Timeout: 60 * time.Second}, endpoint: endpoint, apiKey: apiKey, model: model, openRouter: openRouter}
}

func (p *OpenAIProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type request struct {
		Model    string    `json:"model"`
		Messages []message `json:"messages"`
	}
	type response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int      `json:"prompt_tokens"`
			CompletionTokens int      `json:"completion_tokens"`
			Cost             *float64 `json:"cost"`
		} `json:"usage"`
	}

	messages := make([]message, 0, len(prompt.Messages)+1)
	if prompt.System != "" {
		messages = append(messages, message{Role: "system", Content: prompt.System})
	}
	for _, msg := range prompt.Messages {
		role := string(msg.Role)
		if role != "assistant" {
			role = "user"
		}
		messages = append(messages, message{Role: role, Content: msg.Content})
	}
	var responseBody response
	if err := doJSON(ctx, p.client, http.MethodPost, apiURL(p.endpoint, "chat/completions"), p.apiKey, nil, request{Model: p.model, Messages: messages}, &responseBody); err != nil {
		return nil, fmt.Errorf("openai-compatible execution: %w", err)
	}
	if len(responseBody.Choices) == 0 || responseBody.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("openai-compatible provider returned empty response")
	}
	choice := responseBody.Choices[0]
	return &Completion{Raw: choice.Message.Content, FinishReason: choice.FinishReason, Usage: Usage{InputTokens: responseBody.Usage.PromptTokens, OutputTokens: responseBody.Usage.CompletionTokens, CostUSD: responseBody.Usage.Cost}}, nil
}

func (p *OpenAIProvider) FetchMetadata(ctx context.Context) (Metadata, error) {
	type model struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int    `json:"context_length"`
	}
	var response struct {
		Data []model `json:"data"`
	}
	if err := doJSON(ctx, p.client, http.MethodGet, apiURL(p.endpoint, "models"), p.apiKey, nil, nil, &response); err != nil {
		return Metadata{}, fmt.Errorf("list models: %w", err)
	}
	metadata := Metadata{Models: make([]ModelInfo, 0, len(response.Data)), FetchedAt: time.Now().UTC()}
	for _, item := range response.Data {
		name := item.Name
		if name == "" {
			name = item.ID
		}
		metadata.Models = append(metadata.Models, ModelInfo{ID: item.ID, Name: name, ContextLength: item.ContextLength})
	}
	if p.openRouter {
		var credits struct {
			Data AccountInfo `json:"data"`
		}
		if err := doJSON(ctx, p.client, http.MethodGet, apiURL(p.endpoint, "credits"), p.apiKey, nil, nil, &credits); err == nil {
			metadata.Account = &credits.Data
		}
	}
	return metadata, nil
}
