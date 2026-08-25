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

const (
	mistralEndpoint     = "https://api.mistral.ai/v1"
	defaultMistralModel = "mistral-medium-latest"
	mistralMaxTokens    = 8192
)

// MistralProvider implements Mistral's Conversations API.
type MistralProvider struct {
	client   *http.Client
	endpoint string
	apiKey   string
	model    string
}

func NewMistralProvider(_ context.Context, apiKey, model, endpoint string) (*MistralProvider, error) {
	if endpoint == "" {
		endpoint = mistralEndpoint
	}
	if model == "" {
		model = defaultMistralModel
	}
	return &MistralProvider{client: &http.Client{Timeout: 60 * time.Second}, endpoint: endpoint, apiKey: apiKey, model: model}, nil
}

type mistralMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mistralConversationRequest struct {
	Model          string                `json:"model"`
	Inputs         []any                 `json:"inputs"`
	Tools          []mistralTool         `json:"tools,omitempty"`
	CompletionArgs mistralCompletionArgs `json:"completion_args"`
	Instructions   string                `json:"instructions"`
	Store          bool                  `json:"store"`
}

type mistralCompletionArgs struct {
	Temperature    float64                `json:"temperature"`
	MaxTokens      int                    `json:"max_tokens"`
	ResponseFormat *mistralResponseFormat `json:"response_format,omitempty"`
}

type mistralResponseFormat struct {
	Type       string             `json:"type"`
	JSONSchema *mistralJSONSchema `json:"json_schema,omitempty"`
}

type mistralJSONSchema struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict"`
}

type mistralTool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type mistralConversationResponse struct {
	Outputs []mistralConversationOutput `json:"outputs"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		InputTokens      int `json:"input_tokens"`
		OutputTokens     int `json:"output_tokens"`
	} `json:"usage"`
}

type mistralConversationOutput struct {
	Type       string          `json:"type"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
}

// Chat creates one non-persistent remote conversation from Supremo's local history.
func (p *MistralProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	request := mistralConversationRequest{
		Model: p.model, Inputs: mistralInputs(prompt), Instructions: prompt.System,
	}
	for _, definition := range prompt.ToolDefinitions {
		var schema map[string]any
		if json.Unmarshal(definition.InputSchema, &schema) == nil {
			request.Tools = append(request.Tools, mistralTool{Type: "function", Function: openAIFunction{Name: definition.Name, Description: definition.Description, Parameters: schema}})
		}
	}
	request.CompletionArgs.Temperature = 0
	request.CompletionArgs.MaxTokens = mistralMaxTokens
	if prompt.OutputReserve > 0 {
		request.CompletionArgs.MaxTokens = prompt.OutputReserve
	}
	var response mistralConversationResponse
	err := doJSON(ctx, p.client, http.MethodPost, apiURL(p.endpoint, "conversations"), p.apiKey, nil, request, &response)
	if err != nil {
		return nil, fmt.Errorf("mistral conversations execution: %w", err)
	}
	completion := &Completion{}
	var content strings.Builder
	for _, output := range response.Outputs {
		if output.Type == "function.call" {
			arguments := output.Arguments
			if len(arguments) > 0 && arguments[0] == '"' {
				var encoded string
				if json.Unmarshal(arguments, &encoded) == nil {
					arguments = json.RawMessage(encoded)
				}
			}
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			id, synthetic := normalizeToolCallID(output.ToolCallID)
			completion.ToolCalls = append(completion.ToolCalls, models.ToolCall{ID: id, Name: canonicalToolName(output.Name, prompt.ActiveTools), Arguments: arguments, Synthetic: synthetic})
			continue
		}
		if output.Type != "" && output.Type != "message.output" {
			continue
		}
		text, err := mistralOutputText(output.Content)
		if err != nil {
			return nil, err
		}
		content.WriteString(text)
	}
	inputTokens, outputTokens := response.Usage.PromptTokens, response.Usage.CompletionTokens
	if inputTokens == 0 {
		inputTokens = response.Usage.InputTokens
	}
	if outputTokens == 0 {
		outputTokens = response.Usage.OutputTokens
	}
	finishReason := FinishStop
	// Conversations omits a finish reason, so an exact output cap is the only
	// signal that the response may have been truncated.
	if outputTokens >= request.CompletionArgs.MaxTokens {
		finishReason = FinishMaxTokens
	} else if len(completion.ToolCalls) > 0 {
		finishReason = FinishToolCalls
	}
	completion.Text, completion.FinishReason, completion.Usage = content.String(), string(finishReason), Usage{InputTokens: inputTokens, OutputTokens: outputTokens}

	if len(completion.ToolCalls) > 0 {
		return completion, nil
	}
	if content.Len() == 0 && len(completion.ToolCalls) == 0 {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: "mistral conversations returned no text or tool output"}
	}
	return completion, nil
}

func mistralInputs(prompt *models.Prompt) []any {
	messages := providerMessages(prompt)
	inputs := make([]any, 0, len(messages))
	for _, message := range messages {
		if len(message.ToolCalls) > 0 {
			if message.Content != "" {
				inputs = append(inputs, mistralMessage{Role: "assistant", Content: message.Content})
			}
			for _, call := range message.ToolCalls {
				arguments, _ := json.Marshal(call.Arguments)
				inputs = append(inputs, map[string]any{"type": "function.call", "tool_call_id": call.ID, "name": call.Name, "arguments": string(arguments)})
			}
			continue
		}
		if message.Role == models.RoleTool {
			inputs = append(inputs, map[string]any{"type": "function.result", "tool_call_id": message.ToolCallID, "result": message.Content})
			continue
		}
		role := "user"
		if message.Role == models.RoleAssistant {
			role = "assistant"
		}
		inputs = append(inputs, mistralMessage{Role: role, Content: message.Content})
	}
	return inputs
}

func mistralOutputText(content json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return text, nil
	}
	var chunks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &chunks); err != nil {
		return "", fmt.Errorf("decode mistral conversation output: %w", err)
	}
	var textContent strings.Builder
	for _, chunk := range chunks {
		if chunk.Type == "text" {
			textContent.WriteString(chunk.Text)
		}
	}
	return textContent.String(), nil
}

// FetchMetadata lists the models exposed to the configured Mistral key.
func (p *MistralProvider) FetchMetadata(ctx context.Context) (Metadata, error) {
	var raw json.RawMessage
	if err := doJSON(ctx, p.client, http.MethodGet, apiURL(p.endpoint, "models"), p.apiKey, nil, nil, &raw); err != nil {
		return Metadata{}, fmt.Errorf("list mistral models: %w", err)
	}
	type modelCard struct {
		ID               string `json:"id"`
		MaxContextLength int    `json:"max_context_length"`
	}
	var models []modelCard
	if err := json.Unmarshal(raw, &models); err != nil {
		var response struct {
			Data []modelCard `json:"data"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return Metadata{}, fmt.Errorf("decode mistral models: %w", err)
		}
		models = response.Data
	}
	metadata := Metadata{Models: make([]ModelInfo, 0, len(models)), FetchedAt: time.Now().UTC()}
	for _, model := range models {
		if model.ID != "" {
			metadata.Models = append(metadata.Models, ModelInfo{ID: model.ID, Name: model.ID, ContextLength: model.MaxContextLength})
		}
	}
	return metadata, nil
}
