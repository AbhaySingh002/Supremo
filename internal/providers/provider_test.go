package providers

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

func TestIsContextOverflow(t *testing.T) {
	for _, test := range []struct {
		err  error
		want bool
	}{
		{err: &httpStatusError{code: http.StatusRequestEntityTooLarge, body: "request too large"}, want: true},
		{err: fmt.Errorf("wrapped: %w", &httpStatusError{code: http.StatusBadRequest, body: "maximum context length exceeded"}), want: true},
		{err: &httpStatusError{code: http.StatusBadRequest, body: "invalid temperature"}, want: false},
	} {
		if got := IsContextOverflow(test.err); got != test.want {
			t.Fatalf("IsContextOverflow(%v) = %t, want %t", test.err, got, test.want)
		}
	}
}

func TestProviderMessagesNormalizesOnlyAssistantTerminalHistory(t *testing.T) {
	for name, test := range map[string]struct {
		messages []models.Message
		want     []models.Message
	}{
		"empty": {},
		"user": {
			messages: []models.Message{{Role: models.RoleUser, Content: "hi"}},
			want:     []models.Message{{Role: models.RoleUser, Content: "hi"}},
		},
		"tool": {
			messages: []models.Message{{Role: models.RoleAssistant, Content: "call"}, {Role: models.RoleTool, Content: "result"}},
			want:     []models.Message{{Role: models.RoleAssistant, Content: "call"}, {Role: models.RoleTool, Content: "result"}},
		},
		"plan approval": {
			messages: []models.Message{{Role: models.RoleUser, Content: "make a plan"}, {Role: models.RoleAssistant, Content: `{"schema_version":4}`}},
			want: []models.Message{
				{Role: models.RoleUser, Content: "make a plan"},
				{Role: models.RoleAssistant, Content: `{"schema_version":4}`},
				{Role: models.RoleUser, Content: continuationInstruction},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			prompt := &models.Prompt{Messages: append([]models.Message(nil), test.messages...)}
			if got := providerMessages(prompt); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("providerMessages() = %#v, want %#v", got, test.want)
			}
			if !reflect.DeepEqual(prompt.Messages, test.messages) {
				t.Fatalf("providerMessages mutated prompt: %#v, want %#v", prompt.Messages, test.messages)
			}
		})
	}
}

func TestOpenAIChatMessagesNormalizesAssistantTerminalHistory(t *testing.T) {
	messages := openAIChatMessages(&models.Prompt{Messages: []models.Message{
		{Role: models.RoleUser, Content: "make a plan"},
		{Role: models.RoleAssistant, Content: `{"schema_version":4}`},
	}})
	want := []openAIMessage{
		{Role: "user", Content: "make a plan"},
		{Role: "assistant", Content: `{"schema_version":4}`},
		{Role: "user", Content: continuationInstruction},
	}
	for i := range messages {
		if len(messages[i].ToolCalls) == 0 {
			messages[i].ToolCalls = nil
		}
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("openAIChatMessages() = %#v, want %#v", messages, want)
	}
}
