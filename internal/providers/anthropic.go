package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
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
		Content any    `json:"content"`
	}
	type toolDefinition struct {
		Name        string         `json:"name"`
		Description string         `json:"description,omitempty"`
		InputSchema map[string]any `json:"input_schema"`
	}
	type request struct {
		Model        string           `json:"model"`
		MaxTokens    int              `json:"max_tokens"`
		System       string           `json:"system,omitempty"`
		Messages     []message        `json:"messages"`
		Tools        []toolDefinition `json:"tools,omitempty"`
		OutputConfig any              `json:"output_config,omitempty"`
	}
	type response struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	history := providerMessages(prompt)
	messages := make([]message, 0, len(history))
	for _, msg := range history {
		role := "user"
		if msg.Role == models.RoleAssistant {
			role = "assistant"
		}
		content := any(msg.Content)
		if len(msg.ToolCalls) > 0 {
			blocks := make([]map[string]any, 0, len(msg.ToolCalls)+1)
			if msg.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
			}
			for _, call := range msg.ToolCalls {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": call.Arguments})
			}
			content = blocks
		}
		if msg.Role == models.RoleTool {
			role = "user"
			content = []map[string]any{{"type": "tool_result", "tool_use_id": msg.ToolCallID, "content": msg.Content}}
		}
		messages = append(messages, message{Role: role, Content: content})
	}
	maxTokens := 8192
	if prompt.OutputReserve > 0 {
		maxTokens = max(prompt.OutputReserve, 8192)
	}
	var responseBody response
	req := request{Model: p.model, MaxTokens: maxTokens, System: prompt.System, Messages: messages}
	for _, definition := range prompt.ToolDefinitions {
		var schema map[string]any
		if json.Unmarshal(definition.InputSchema, &schema) == nil {
			req.Tools = append(req.Tools, toolDefinition{Name: definition.Name, Description: definition.Description, InputSchema: schema})
		}
	}
	err := doJSON(ctx, p.client, http.MethodPost, apiURL(p.endpoint, "messages"), "", p.headers(), req, &responseBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic execution: %w", err)
	}
	finish := NormalizeFinishReason(responseBody.StopReason)
	completion := &Completion{FinishReason: string(finish), Usage: Usage{InputTokens: responseBody.Usage.InputTokens, OutputTokens: responseBody.Usage.OutputTokens}}
	var text strings.Builder
	for _, block := range responseBody.Content {
		if block.Type == "text" && block.Text != "" {
			text.WriteString(block.Text)
		} else if block.Type == "tool_use" {
			rawInput := block.Input
			if len(rawInput) == 0 {
				rawInput = json.RawMessage(`{}`)
			}
			id, synthetic := normalizeToolCallID(block.ID)
			completion.ToolCalls = append(completion.ToolCalls, models.ToolCall{ID: id, Name: canonicalToolName(block.Name, prompt.ActiveTools), Arguments: rawInput, Synthetic: synthetic})
		}
	}
	completion.Text = text.String()
	if len(completion.ToolCalls) > 0 {
		if completion.FinishReason == "" || completion.FinishReason == string(FinishStop) {
			completion.FinishReason = string(FinishToolCalls)
		}
		return completion, nil
	}
	if completion.Text == "" && len(completion.ToolCalls) == 0 {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: "anthropic returned no text or tool content"}
	}
	return completion, nil
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
