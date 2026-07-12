package providers

import (
	"context"
	"fmt"

	"github.com/AbhaySingh002/supremo/internal/models"
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

	var config *genai.GenerateContentConfig
	if prompt.System != "" {
		config = &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: prompt.System}},
			},
		}
	}

	resp, err := p.client.Models.GenerateContent(ctx, p.model, contents, config)
	if err != nil {
		return nil, fmt.Errorf("gemini execution error: %w", err)
	}

	finishReason := ""
	if len(resp.Candidates) > 0 {
		finishReason = string(resp.Candidates[0].FinishReason)
	}

	// ponytail: resp.Text() panics when candidates have no text parts.
	// Recover and return a proper error instead of crashing the process.
	text, extractErr := safeExtractText(resp)
	if extractErr != nil {
		return nil, fmt.Errorf("gemini returned empty response (finish_reason=%s): %w", finishReason, extractErr)
	}

	return &Completion{
		Raw:          text,
		FinishReason: finishReason,
	}, nil
}

// safeExtractText calls resp.Text() with panic recovery.
func safeExtractText(resp *genai.GenerateContentResponse) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	text = resp.Text()
	if text == "" {
		return "", fmt.Errorf("model returned empty text")
	}
	return text, nil
}
