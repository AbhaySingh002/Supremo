package providers

import (
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"google.golang.org/genai"
)

func TestGeminiThinkingStaysPrivate(t *testing.T) {
	_, config := geminiRequest(&models.Prompt{}, "gemini-3.5-flash-lite")
	if config == nil || config.ThinkingConfig == nil || config.ThinkingConfig.ThinkingLevel != genai.ThinkingLevelMedium {
		t.Fatalf("Gemini 3 thinking config = %#v", config)
	}

	response := &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{
		{Text: "private reasoning", Thought: true},
		{Text: "visible answer"},
	}}}}}
	if got := streamText(response); got != "visible answer" {
		t.Fatalf("stream text leaked thoughts: %q", got)
	}
	if got, err := safeExtractText(response); err != nil || got != "visible answer" {
		t.Fatalf("response text leaked thoughts: %q, %v", got, err)
	}
}

func TestGeminiTextModelFilter(t *testing.T) {
	tests := []struct {
		name  string
		model *genai.Model
		want  bool
	}{
		{name: "text", model: &genai.Model{Name: "models/gemini-3.6-flash", SupportedActions: []string{"generateContent"}}, want: true},
		{name: "image", model: &genai.Model{Name: "models/gemini-3.1-flash-image", SupportedActions: []string{"generateContent"}}},
		{name: "imagen", model: &genai.Model{Name: "models/imagen-4.0-generate-001", SupportedActions: []string{"predict"}}},
		{name: "embedding", model: &genai.Model{Name: "models/gemini-embedding-001", SupportedActions: []string{"embedContent"}}},
		{name: "nil"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isGeminiTextModel(test.model); got != test.want {
				t.Fatalf("isGeminiTextModel() = %v, want %v", got, test.want)
			}
		})
	}
}
