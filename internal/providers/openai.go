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

const (
	openAIEndpoint     = "https://api.openai.com/v1"
	groqEndpoint       = "https://api.groq.com/openai/v1"
	defaultGroqModel   = "llama-3.1-8b-instant"
	nvidiaEndpoint     = "https://integrate.api.nvidia.com/v1"
	defaultNVIDIAModel = "nvidia/nemotron-3-ultra-550b-a55b"
)

// OpenAIProvider implements the OpenAI chat-completions protocol and covers
// self-hosted OpenAI-compatible servers.
type OpenAIProvider struct {
	client   *http.Client
	endpoint string
	apiKey   string
	model    string
	nvidia   bool
}

func NewOpenAIProvider(_ context.Context, apiKey, model, endpoint string) (*OpenAIProvider, error) {
	return newOpenAIProvider(apiKey, model, endpoint), nil
}

// NewGroqProvider implements Groq's OpenAI-compatible chat-completions API.
func NewGroqProvider(_ context.Context, apiKey, model, endpoint string) (*OpenAIProvider, error) {
	if endpoint == "" {
		endpoint = groqEndpoint
	}
	if model == "" {
		model = defaultGroqModel
	}
	return newOpenAIProvider(apiKey, model, endpoint), nil
}

// NewNVIDIAProvider implements NVIDIA NIM's OpenAI-compatible chat-completions API.
func NewNVIDIAProvider(_ context.Context, apiKey, model, endpoint string) (*OpenAIProvider, error) {
	if endpoint == "" {
		endpoint = nvidiaEndpoint
	}
	if model == "" {
		model = defaultNVIDIAModel
	}
	provider := newOpenAIProvider(apiKey, model, endpoint)
	provider.nvidia = true
	return provider, nil
}

func NewOpenAICompatibleProvider(_ context.Context, apiKey, model, endpoint string) (*OpenAIProvider, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("an endpoint is required for openai-compatible providers")
	}
	return newOpenAIProvider(apiKey, model, endpoint), nil
}

func newOpenAIProvider(apiKey, model, endpoint string) *OpenAIProvider {
	if endpoint == "" {
		endpoint = openAIEndpoint
	}
	return &OpenAIProvider{client: &http.Client{Timeout: 60 * time.Second}, endpoint: endpoint, apiKey: apiKey, model: model}
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	Name       string           `json:"name,omitempty"`
}

type openAIChatRequest struct {
	Model              string                    `json:"model"`
	Messages           []openAIMessage           `json:"messages"`
	ResponseFormat     *openAIResponseFormat     `json:"response_format,omitempty"`
	Temperature        *float64                  `json:"temperature,omitempty"`
	TopP               *float64                  `json:"top_p,omitempty"`
	MaxTokens          *int                      `json:"max_tokens,omitempty"`
	ReasoningBudget    *int                      `json:"reasoning_budget,omitempty"`
	ChatTemplateKwargs *openAIChatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
	Stream             bool                      `json:"stream,omitempty"`
	Tools              []openAITool              `json:"tools,omitempty"`
}

type openAIResponseFormat struct {
	Type       string            `json:"type"`
	JSONSchema *openAIJSONSchema `json:"json_schema,omitempty"`
}

type openAIJSONSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIChatTemplateKwargs struct {
	EnableThinking bool `json:"enable_thinking"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content,omitempty"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int      `json:"prompt_tokens"`
		CompletionTokens int      `json:"completion_tokens"`
		Cost             *float64 `json:"cost"`
	} `json:"usage"`
}

func openAIChatMessages(prompt *models.Prompt) []openAIMessage {
	history := providerMessages(prompt)
	messages := make([]openAIMessage, 0, len(history)+1)
	if prompt.System != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: prompt.System})
	}
	for _, msg := range history {
		var content any = msg.Content
		message := openAIMessage{Role: string(msg.Role), Content: content}
		switch msg.Role {
		case models.RoleAssistant:
			message.ToolCalls = openAIHistoryToolCalls(msg.ToolCalls)
			if len(message.ToolCalls) > 0 && msg.Content == "" {
				message.Content = nil
			}
		case models.RoleTool:
			message.ToolCallID, message.Name = msg.ToolCallID, msg.ToolName
		default:
			message.Role = "user"
		}
		messages = append(messages, message)
	}
	return messages
}

func (p *OpenAIProvider) chatRequest(prompt *models.Prompt, stream bool) openAIChatRequest {
	request := openAIChatRequest{Model: p.model, Messages: openAIChatMessages(prompt), Stream: stream}
	request.Tools = openAITools(prompt.ToolDefinitions)
	if !p.nvidia {
		return request
	}
	temperature, topP := 1.0, 0.95
	maxTokens, reasoningBudget := 16384, 16384
	if prompt.OutputReserve > 0 {
		maxTokens, reasoningBudget = max(prompt.OutputReserve, 16384), max(prompt.OutputReserve, 16384)
	}
	request.Temperature = &temperature
	request.TopP = &topP
	request.MaxTokens = &maxTokens
	request.ReasoningBudget = &reasoningBudget
	request.ChatTemplateKwargs = &openAIChatTemplateKwargs{EnableThinking: true}
	return request
}

func (p *OpenAIProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	request := p.chatRequest(prompt, false)
	var responseBody openAIChatResponse
	err := doJSON(ctx, p.client, http.MethodPost, apiURL(p.endpoint, "chat/completions"), p.apiKey, nil, request, &responseBody)
	if err != nil {
		return nil, fmt.Errorf("openai-compatible execution: %w", err)
	}
	if len(responseBody.Choices) == 0 {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: "openai-compatible provider returned no choices"}
	}
	choice := responseBody.Choices[0]
	finish := NormalizeFinishReason(choice.FinishReason)
	if len(choice.Message.ToolCalls) > 0 && finish == FinishStop {
		finish = FinishToolCalls
	}

	completion := &Completion{
		Text:         choice.Message.Content,
		FinishReason: string(finish),
		Usage:        Usage{InputTokens: responseBody.Usage.PromptTokens, OutputTokens: responseBody.Usage.CompletionTokens, CostUSD: responseBody.Usage.Cost},
	}
	for _, call := range choice.Message.ToolCalls {
		rawArgs := call.Function.Arguments
		if strings.TrimSpace(rawArgs) == "" {
			rawArgs = "{}"
		}
		arguments := json.RawMessage(rawArgs)
		id, synthetic := normalizeToolCallID(call.ID)
		completion.ToolCalls = append(completion.ToolCalls, models.ToolCall{ID: id, Name: canonicalToolName(call.Function.Name, prompt.ActiveTools), Arguments: arguments, Synthetic: synthetic})
	}
	if completion.Text == "" && len(completion.ToolCalls) == 0 && choice.Message.ReasoningContent == "" {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: "openai-compatible provider returned empty response"}
	}
	return completion, nil
}

func openAITools(definitions []models.ToolDefinition) []openAITool {
	tools := make([]openAITool, 0, len(definitions))
	for _, definition := range definitions {
		var parameters map[string]any
		if json.Unmarshal(definition.InputSchema, &parameters) != nil {
			continue
		}
		tools = append(tools, openAITool{Type: "function", Function: openAIFunction{Name: definition.Name, Description: definition.Description, Parameters: parameters}})
	}
	return tools
}

func openAIHistoryToolCalls(calls []models.ToolCall) []openAIToolCall {
	result := make([]openAIToolCall, 0, len(calls))
	for _, call := range calls {
		arguments, err := json.Marshal(call.Arguments)
		if err != nil || call.ID == "" {
			continue
		}
		wire := openAIToolCall{ID: call.ID, Type: "function"}
		wire.Function.Name, wire.Function.Arguments = call.Name, string(arguments)
		result = append(result, wire)
	}
	return result
}

type openAIStreamToolCallChunk struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// Stream translates OpenAI-compatible server-sent events into canonical events.
func (p *OpenAIProvider) Stream(ctx context.Context, prompt *models.Prompt, receive func(StreamEvent) error) error {
	accept := "text/event-stream"
	if p.nvidia {
		accept = "application/json"
	}
	body, _, err := doJSONStream(ctx, p.client, apiURL(p.endpoint, "chat/completions"), p.apiKey, accept, p.chatRequest(prompt, true))
	if err != nil {
		return fmt.Errorf("openai-compatible streaming execution: %w", err)
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
			Choices []struct {
				Delta struct {
					Content          string                      `json:"content"`
					ReasoningContent string                      `json:"reasoning_content"`
					Reasoning        string                      `json:"reasoning"`
					ToolCalls        []openAIStreamToolCallChunk `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return fmt.Errorf("decode openai-compatible stream event: %w", err)
		}
		if event.Usage.PromptTokens != 0 || event.Usage.CompletionTokens != 0 {
			if err := emit(StreamEvent{
				Type:  StreamEventUsage,
				Usage: &Usage{InputTokens: event.Usage.PromptTokens, OutputTokens: event.Usage.CompletionTokens},
			}); err != nil {
				return err
			}
		}
		for _, choice := range event.Choices {
			if choice.Delta.Content != "" {
				if err := emit(StreamEvent{
					Type:      StreamEventTextDelta,
					TextDelta: choice.Delta.Content,
				}); err != nil {
					return err
				}
			}
			reasoning := choice.Delta.ReasoningContent
			if reasoning == "" {
				reasoning = choice.Delta.Reasoning
			}
			if reasoning != "" {
				if err := emit(StreamEvent{
					Type:           StreamEventReasoningDelta,
					ReasoningDelta: reasoning,
				}); err != nil {
					return err
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				if err := emit(StreamEvent{
					Type: StreamEventToolCallDelta,
					ToolCall: &ToolCallDelta{
						Index:          tc.Index,
						ID:             tc.ID,
						Name:           tc.Function.Name,
						ArgumentsDelta: tc.Function.Arguments,
					},
				}); err != nil {
					return err
				}
			}
			if choice.FinishReason != "" {
				if err := emit(StreamEvent{
					Type:         StreamEventFinish,
					FinishReason: NormalizeFinishReason(choice.FinishReason),
				}); err != nil {
					return err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read openai-compatible stream: %w", err)
	}
	return nil
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
	return metadata, nil
}
