package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"google.golang.org/genai"
)

// GeminiProvider implements Provider using the official Google GenAI Go SDK.
type GeminiProvider struct {
	client *genai.Client
	model  string
}

// NewGeminiProvider constructs a new GeminiProvider.
func NewGeminiProvider(ctx context.Context, apiKey, model, endpoint string) (*GeminiProvider, error) {
	cfg := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if endpoint != "" {
		cfg.HTTPOptions.BaseURL = endpoint
	}

	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create gemini genai client: %w", err)
	}

	return &GeminiProvider{
		client: client,
		model:  model,
	}, nil
}

// Chat sends prompt turns to Gemini models.
func (p *GeminiProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	contents, config := geminiRequest(prompt, p.model)
	if logging.IsEnabled() {
		if data, err := json.Marshal(map[string]any{"model": p.model, "contents": contents, "config": config}); err == nil {
			logging.Debug("Gemini SDK request payload: %s", string(data))
		}
	}
	resp, err := p.client.Models.GenerateContent(ctx, p.model, contents, config)
	if err != nil && config != nil && config.ResponseJsonSchema != nil && isUnsupportedStructuredOutput(err) {
		config.ResponseJsonSchema = nil
		resp, err = p.client.Models.GenerateContent(ctx, p.model, contents, config)
	}
	if err != nil {
		return nil, fmt.Errorf("gemini execution error: %w", normalizeGeminiError(err))
	}
	if logging.IsEnabled() && resp != nil {
		if data, err := json.Marshal(resp); err == nil {
			logging.Debug("Gemini SDK response payload: %s", string(data))
		}
	}
	return completeGeminiChat(resp, prompt.ActiveTools)
}

func completeGeminiChat(resp *genai.GenerateContentResponse, activeTools []string) (*Completion, error) {
	if resp == nil {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: "gemini returned empty response"}
	}
	finishReason := FinishStop
	if len(resp.Candidates) > 0 && resp.Candidates[0] != nil {
		finishReason = NormalizeFinishReason(string(resp.Candidates[0].FinishReason))
	}

	completion := &Completion{FinishReason: string(finishReason), Usage: geminiUsage(resp.UsageMetadata)}
	for _, candidate := range resp.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part == nil {
				continue
			}
			if part.Text != "" && !part.Thought {
				completion.Text += part.Text
			}
			if part.FunctionCall == nil {
				continue
			}
			id, synthetic := normalizeToolCallID(part.FunctionCall.ID)
			arguments, marshalErr := json.Marshal(part.FunctionCall.Args)
			if marshalErr != nil {
				return nil, &ProviderFailure{Code: FailureInvalidRequest, Message: "gemini returned malformed native tool arguments"}
			}
			metadata, _ := json.Marshal(struct {
				ThoughtSignature []byte `json:"thought_signature,omitempty"`
			}{ThoughtSignature: part.ThoughtSignature})
			completion.ToolCalls = append(completion.ToolCalls, models.ToolCall{
				ID:               id,
				Name:             canonicalToolName(part.FunctionCall.Name, activeTools),
				Arguments:        json.RawMessage(arguments),
				Synthetic:        synthetic,
				ProviderMetadata: metadata,
			})
		}
	}
	if len(completion.ToolCalls) > 0 {
		if completion.FinishReason == "" || completion.FinishReason == string(FinishStop) {
			completion.FinishReason = string(FinishToolCalls)
		}
		return completion, nil
	}

	completion.Text = streamText(resp)
	if strings.TrimSpace(completion.Text) == "" {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: fmt.Sprintf("gemini returned empty response (finish_reason=%s)", finishReason)}
	}
	return completion, nil
}

// Stream translates Gemini response parts into canonical events.
func (p *GeminiProvider) Stream(ctx context.Context, prompt *models.Prompt, receive func(StreamEvent) error) error {
	contents, config := geminiRequest(prompt, p.model)
	toolIndex := 0
	emit := func(event StreamEvent) error {
		if receive == nil {
			return nil
		}
		return receive(event)
	}

	for response, err := range p.client.Models.GenerateContentStream(ctx, p.model, contents, config) {
		if err != nil {
			return fmt.Errorf("gemini streaming execution error: %w", normalizeGeminiError(err))
		}
		if response == nil {
			continue
		}
		if response.UsageMetadata != nil {
			usage := geminiUsage(response.UsageMetadata)
			if err := emit(StreamEvent{Type: StreamEventUsage, Usage: &usage}); err != nil {
				return err
			}
		}
		for _, candidate := range response.Candidates {
			if candidate == nil {
				continue
			}
			if candidate.FinishReason != "" {
				if err := emit(StreamEvent{
					Type:         StreamEventFinish,
					FinishReason: NormalizeFinishReason(string(candidate.FinishReason)),
				}); err != nil {
					return err
				}
			}
			if candidate.Content == nil {
				continue
			}
			for _, part := range candidate.Content.Parts {
				if part == nil {
					continue
				}
				if part.Thought && part.Text != "" {
					if err := emit(StreamEvent{
						Type:           StreamEventReasoningDelta,
						ReasoningDelta: part.Text,
					}); err != nil {
						return err
					}
				} else if part.Text != "" {
					if err := emit(StreamEvent{
						Type:      StreamEventTextDelta,
						TextDelta: part.Text,
					}); err != nil {
						return err
					}
				}
				if part.FunctionCall != nil {
					arguments, _ := json.Marshal(part.FunctionCall.Args)
					if err := emit(StreamEvent{
						Type: StreamEventToolCallDelta,
						ToolCall: &ToolCallDelta{
							Index:          toolIndex,
							ID:             part.FunctionCall.ID,
							Name:           part.FunctionCall.Name,
							ArgumentsDelta: string(arguments),
						},
					}); err != nil {
						return err
					}
					toolIndex++
				}
			}
		}
	}

	return nil
}

func geminiUsage(metadata *genai.GenerateContentResponseUsageMetadata) Usage {
	if metadata == nil {
		return Usage{}
	}
	return Usage{
		InputTokens:  int(metadata.PromptTokenCount + metadata.ToolUsePromptTokenCount),
		OutputTokens: int(metadata.CandidatesTokenCount + metadata.ThoughtsTokenCount),
	}
}

func geminiRequest(prompt *models.Prompt, model string) ([]*genai.Content, *genai.GenerateContentConfig) {
	var contents []*genai.Content

	for _, msg := range providerMessages(prompt) {
		role := "user"
		if msg.Role == models.RoleAssistant {
			role = "model"
		}
		parts := []*genai.Part{{Text: msg.Content}}
		if len(msg.ToolCalls) > 0 {
			parts = make([]*genai.Part, 0, len(msg.ToolCalls)+1)
			if msg.Content != "" {
				parts = append(parts, &genai.Part{Text: msg.Content})
			}
			for _, call := range msg.ToolCalls {
				arguments, _ := json.Marshal(call.Arguments)
				var args map[string]any
				_ = json.Unmarshal(arguments, &args)
				var metadata struct {
					ThoughtSignature []byte `json:"thought_signature"`
				}
				_ = json.Unmarshal(call.ProviderMetadata, &metadata)
				parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: call.ID, Name: call.Name, Args: args}, ThoughtSignature: metadata.ThoughtSignature})
			}
		}
		if msg.Role == models.RoleTool {
			role = "user"
			parts = []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: msg.ToolCallID, Name: msg.ToolName, Response: map[string]any{"output": msg.Content}}}}
		}
		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: parts,
		})
	}
	config := &genai.GenerateContentConfig{ThinkingConfig: geminiThinkingConfig(model)}
	if len(prompt.ToolDefinitions) > 0 {
		declarations := make([]*genai.FunctionDeclaration, 0, len(prompt.ToolDefinitions))
		for _, definition := range prompt.ToolDefinitions {
			var schema any
			if json.Unmarshal(definition.InputSchema, &schema) != nil {
				continue
			}
			declarations = append(declarations, &genai.FunctionDeclaration{Name: definition.Name, Description: definition.Description, ParametersJsonSchema: schema})
		}
		if len(declarations) > 0 {
			config.Tools = []*genai.Tool{{FunctionDeclarations: declarations}}
		}
	}
	if prompt.System != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: prompt.System}},
		}
	}
	if config.SystemInstruction == nil && config.ThinkingConfig == nil {
		config = nil
	}

	return contents, config
}

// geminiThinkingConfig enables native reasoning only for Gemini families that
// advertise it. Thoughts remain server-side and are filtered from responses.
func geminiThinkingConfig(model string) *genai.ThinkingConfig {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(model, "gemini-3"):
		return &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelMedium}
	case strings.HasPrefix(model, "gemini-2.5"):
		budget := int32(1024)
		return &genai.ThinkingConfig{ThinkingBudget: &budget}
	default:
		return nil
	}
}

func streamText(response *genai.GenerateContentResponse) string {
	if response == nil {
		return ""
	}
	var text strings.Builder
	for _, candidate := range response.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part != nil && !part.Thought {
				text.WriteString(part.Text)
			}
		}
	}
	return text.String()
}

// safeExtractText returns only the response text, never the model's private thoughts.
func safeExtractText(resp *genai.GenerateContentResponse) (text string, err error) {
	if resp == nil {
		return "", fmt.Errorf("model returned no response")
	}
	text = streamText(resp)
	if text == "" {
		return "", fmt.Errorf("model returned empty text")
	}
	return text, nil
}

// FetchMetadata lists Gemini models and their advertised input context limits.
func (p *GeminiProvider) FetchMetadata(ctx context.Context) (Metadata, error) {
	metadata := Metadata{FetchedAt: time.Now().UTC()}
	for model, err := range p.client.Models.All(ctx) {
		if err != nil {
			return Metadata{}, fmt.Errorf("list Gemini models: %w", normalizeGeminiError(err))
		}
		if !isGeminiTextModel(model) {
			continue
		}
		id := strings.TrimPrefix(model.Name, "models/")
		metadata.Models = append(metadata.Models, ModelInfo{ID: id, Name: model.DisplayName, ContextLength: int(model.InputTokenLimit)})
	}
	return metadata, nil
}

func isGeminiTextModel(model *genai.Model) bool {
	return model != nil &&
		slices.Contains(model.SupportedActions, "generateContent") &&
		!strings.Contains(strings.ToLower(model.Name), "image")
}

func normalizeGeminiError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &ProviderFailure{Code: FailureAborted, Message: "gemini request canceled", Err: err}
	}
	if IsContextOverflow(err) {
		return &ProviderFailure{Code: FailureContextWindowExceeded, Message: err.Error(), Err: err}
	}
	if IsAuthenticationError(err) {
		return &ProviderFailure{Code: FailureAuth, Message: err.Error(), Err: err}
	}
	if code, status, msg, ok := GeminiAPIError(err); ok {
		codeFailure := FailureInvalidRequest
		if code == 429 || status == "RESOURCE_EXHAUSTED" {
			codeFailure = FailureRateLimit
		} else if code >= 500 && code <= 599 {
			codeFailure = FailureServer
		}
		return &ProviderFailure{Code: codeFailure, Status: code, Message: fmt.Sprintf("%s (%s): %s", status, codeFailure, msg), Err: err}
	}
	return err
}
