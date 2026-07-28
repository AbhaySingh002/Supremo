package providers

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

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
	resp, err := p.client.Models.GenerateContent(ctx, p.model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("gemini execution error: %w", err)
	}

	finishReason := ""
	if len(resp.Candidates) > 0 {
		finishReason = string(resp.Candidates[0].FinishReason)
	}

	// Read parts directly so private thought parts cannot enter the response.
	text, extractErr := safeExtractText(resp)
	if extractErr != nil {
		return nil, fmt.Errorf("gemini returned empty response (finish_reason=%s): %w", finishReason, extractErr)
	}

	return &Completion{
		Raw:          text,
		FinishReason: finishReason,
	}, nil
}

// Stream sends incremental Gemini text to receive and returns the complete response.
func (p *GeminiProvider) Stream(ctx context.Context, prompt *models.Prompt, receive func(string)) (*Completion, error) {
	contents, config := geminiRequest(prompt, p.model)
	var text strings.Builder
	finishReason := ""
	for response, err := range p.client.Models.GenerateContentStream(ctx, p.model, contents, config) {
		if err != nil {
			return nil, fmt.Errorf("gemini streaming execution error: %w", err)
		}
		if response == nil {
			continue
		}
		if len(response.Candidates) > 0 {
			finishReason = string(response.Candidates[0].FinishReason)
		}
		chunk := streamText(response)
		if chunk == "" {
			continue
		}
		text.WriteString(chunk)
		if receive != nil {
			receive(chunk)
		}
	}
	if text.Len() == 0 {
		return nil, fmt.Errorf("gemini stream returned empty response (finish_reason=%s)", finishReason)
	}
	return &Completion{Raw: text.String(), FinishReason: finishReason}, nil
}

func geminiRequest(prompt *models.Prompt, model string) ([]*genai.Content, *genai.GenerateContentConfig) {
	var contents []*genai.Content

	for _, msg := range prompt.Messages {
		role := "user"
		if msg.Role == models.RoleAssistant {
			role = "model"
		}
		contents = append(contents, &genai.Content{
			Role:  role,
			Parts: []*genai.Part{{Text: msg.Content}},
		})
	}

	config := &genai.GenerateContentConfig{ThinkingConfig: geminiThinkingConfig(model)}
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

// FetchMetadata lists Gemini models and their advertised input context limits.
func (p *GeminiProvider) FetchMetadata(ctx context.Context) (Metadata, error) {
	metadata := Metadata{FetchedAt: time.Now().UTC()}
	for model, err := range p.client.Models.All(ctx) {
		if err != nil {
			return Metadata{}, fmt.Errorf("list Gemini models: %w", err)
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
