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
