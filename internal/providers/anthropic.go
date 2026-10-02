package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicRequest struct {
	Model        string                    `json:"model"`
	MaxTokens    int                       `json:"max_tokens"`
	System       string                    `json:"system,omitempty"`
	Messages     []anthropicMessage        `json:"messages"`
	Tools        []anthropicToolDefinition `json:"tools,omitempty"`
	OutputConfig any                       `json:"output_config,omitempty"`
	Stream       bool                      `json:"stream,omitempty"`
}

func (p *AnthropicProvider) buildRequest(prompt *models.Prompt, stream bool) anthropicRequest {
	history := providerMessages(prompt)
	messages := make([]anthropicMessage, 0, len(history))
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
		messages = append(messages, anthropicMessage{Role: role, Content: content})
	}
	maxTokens := 8192
	if prompt.OutputReserve > 0 {
		maxTokens = max(prompt.OutputReserve, 8192)
	}
	req := anthropicRequest{Model: p.model, MaxTokens: maxTokens, System: prompt.System, Messages: messages, Stream: stream}
	for _, definition := range prompt.ToolDefinitions {
		var schema map[string]any
		if json.Unmarshal(definition.InputSchema, &schema) == nil {
			req.Tools = append(req.Tools, anthropicToolDefinition{Name: definition.Name, Description: definition.Description, InputSchema: schema})
		}
	}
	return req
}

func (p *AnthropicProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
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
	req := p.buildRequest(prompt, false)
	var responseBody response
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

// Stream translates Claude Messages API server-sent events into canonical events.
func (p *AnthropicProvider) Stream(ctx context.Context, prompt *models.Prompt, receive func(StreamEvent) error) error {
	headers := p.headers()
	headers.Set("Accept", "text/event-stream")
	req := p.buildRequest(prompt, true)
	body, _, err := doJSONStreamWithHeaders(ctx, p.client, apiURL(p.endpoint, "messages"), "", headers, req)
	if err != nil {
		return fmt.Errorf("anthropic streaming execution: %w", err)
	}
	defer body.Close()

	emit := func(event StreamEvent) error {
		if receive == nil {
			return nil
		}
		return receive(event)
	}

	scanner := bufio.NewScanner(io.LimitReader(body, maxResponseBytes))
	scanner.Buffer(make([]byte, 4096), maxResponseBytes)
	var inputTokens, outputTokens int
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
				Thinking    string `json:"thinking"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode anthropic stream event: %w", err)
		}
		if event.Error != nil {
			return fmt.Errorf("anthropic stream error (%s): %s", event.Error.Type, event.Error.Message)
		}
		switch event.Type {
		case "message_start":
			if event.Message.Usage.InputTokens > 0 {
				inputTokens = event.Message.Usage.InputTokens
			}
			if event.Message.Usage.OutputTokens > 0 {
				outputTokens = event.Message.Usage.OutputTokens
			}
			if inputTokens > 0 || outputTokens > 0 {
				if err := emit(StreamEvent{
					Type:  StreamEventUsage,
					Usage: &Usage{InputTokens: inputTokens, OutputTokens: outputTokens},
				}); err != nil {
					return err
				}
			}
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				id, _ := normalizeToolCallID(event.ContentBlock.ID)
				name := canonicalToolName(event.ContentBlock.Name, prompt.ActiveTools)
				if err := emit(StreamEvent{
					Type: StreamEventToolCallDelta,
					ToolCall: &ToolCallDelta{
						Index: event.Index,
						ID:    id,
						Name:  name,
					},
				}); err != nil {
					return err
				}
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				if event.Delta.Text != "" {
					if err := emit(StreamEvent{
						Type:      StreamEventTextDelta,
						TextDelta: event.Delta.Text,
					}); err != nil {
						return err
					}
				}
			case "thinking_delta":
				if event.Delta.Thinking != "" {
					if err := emit(StreamEvent{
						Type:           StreamEventReasoningDelta,
						ReasoningDelta: event.Delta.Thinking,
					}); err != nil {
						return err
					}
				}
			case "input_json_delta":
				if event.Delta.PartialJSON != "" {
					if err := emit(StreamEvent{
						Type: StreamEventToolCallDelta,
						ToolCall: &ToolCallDelta{
							Index:          event.Index,
							ArgumentsDelta: event.Delta.PartialJSON,
						},
					}); err != nil {
						return err
					}
				}
			}
		case "message_delta":
			if event.Usage.OutputTokens > 0 {
				outputTokens = event.Usage.OutputTokens
				if err := emit(StreamEvent{
					Type:  StreamEventUsage,
					Usage: &Usage{InputTokens: inputTokens, OutputTokens: outputTokens},
				}); err != nil {
					return err
				}
			}
			if event.Delta.StopReason != "" {
				if err := emit(StreamEvent{
					Type:         StreamEventFinish,
					FinishReason: NormalizeFinishReason(event.Delta.StopReason),
				}); err != nil {
					return err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read anthropic stream: %w", err)
	}
	return nil
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
