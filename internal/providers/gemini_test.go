package providers

import (
	"encoding/json"
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

func TestGeminiToolOnlyFunctionCallIsValidCompletion(t *testing.T) {
	resp := &genai.GenerateContentResponse{Candidates: []*genai.Candidate{{
		FinishReason: genai.FinishReasonStop,
		Content: &genai.Content{Parts: []*genai.Part{{
			FunctionCall: &genai.FunctionCall{ID: "call_search_1", Name: "search_file_name", Args: map[string]any{"path": ".", "pattern": "*"}},
		}}},
	}}}
	comp, err := completeGeminiChat(resp, []string{"search_file_name"})
	if err != nil {
		t.Fatalf("tool-only gemini completion rejected: %v", err)
	}
	if comp.Text != "" || len(comp.ToolCalls) != 1 || comp.ToolCalls[0].ID != "call_search_1" || comp.ToolCalls[0].Name != "search_file_name" {
		t.Fatalf("completion=%#v", comp)
	}
	if comp.FinishReason != string(FinishToolCalls) {
		t.Fatalf("finish=%q", comp.FinishReason)
	}
}

func TestGeminiRequestDoesNotForceJSONOutput(t *testing.T) {
	_, config := geminiRequest(&models.Prompt{}, "gemini-3.5-flash-lite")
	if config != nil && config.ResponseMIMEType == "application/json" {
		t.Fatalf("Gemini request unexpectedly forces JSON output: %#v", config)
	}
}

func TestGeminiRequestEndsWithAUserTurn(t *testing.T) {
	contents, _ := geminiRequest(&models.Prompt{Messages: []models.Message{
		{Role: models.RoleUser, Content: "make a plan"},
		{Role: models.RoleAssistant, Content: `{"schema_version":4}`},
	}}, "gemini-3.6-flash")
	if len(contents) != 3 || contents[2].Role != string(genai.RoleUser) || contents[2].Parts[0].Text != continuationInstruction {
		t.Fatalf("Gemini request must end with a continuation user turn: %#v", contents)
	}

	contents, _ = geminiRequest(&models.Prompt{Messages: []models.Message{
		{Role: models.RoleUser, Content: "make a plan"},
		{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`), ProviderMetadata: json.RawMessage(`{"thought_signature":"c2lnbmF0dXJl"}`)}}},
		{Role: models.RoleTool, ToolCallID: "call-1", ToolName: "read_file", Content: "tool output"},
	}}, "gemini-3.6-flash")
	if len(contents) != 3 || contents[1].Parts[0].FunctionCall == nil || string(contents[1].Parts[0].ThoughtSignature) != "signature" || contents[len(contents)-1].Role != string(genai.RoleUser) || contents[len(contents)-1].Parts[0].FunctionResponse == nil || contents[len(contents)-1].Parts[0].FunctionResponse.ID != "call-1" {
		t.Fatalf("request already ending in a user-equivalent turn should remain unchanged: %#v", contents)
	}
}

func TestGeminiUsageIncludesToolAndThoughtTokens(t *testing.T) {
	usage := geminiUsage(&genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        11,
		ToolUsePromptTokenCount: 7,
		CandidatesTokenCount:    5,
		ThoughtsTokenCount:      3,
	})
	if usage.InputTokens != 18 || usage.OutputTokens != 8 {
		t.Fatalf("Gemini usage = %#v, want input=18 output=8", usage)
	}
	if usage := geminiUsage(nil); usage != (Usage{}) {
		t.Fatalf("nil Gemini usage = %#v", usage)
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
