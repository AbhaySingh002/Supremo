package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/capabilities/repeat"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
)

// This is the one repeat-guard test kept at the agent layer. Unit behavior lives
// with the repeat capability; here we only verify the durable loop ordering.
func TestRepeatGuardDriverTurnStepIntegration(t *testing.T) {
	life := &driverLifecycle{activeTools: []string{"read_file"}}
	provider := &scriptedProvider{chat: func(_ context.Context, n int, prompt *models.Prompt) (*providers.Completion, error) {
		switch n {
		case 0, 1, 2:
			return &providers.Completion{
				FinishReason: string(providers.FinishToolCalls),
				ToolCalls: []models.ToolCall{{
					ID:        "call_" + string(rune('1'+n)),
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"a.go"}`),
				}},
			}, nil
		case 3:
			if !hasRepeatReminder(prompt.Messages) {
				t.Fatal("provider did not receive repeat reminder after the third identical call")
			}
			return &providers.Completion{
				FinishReason: string(providers.FinishToolCalls),
				ToolCalls: []models.ToolCall{{
					ID:        "call_4",
					Name:      "read_file",
					Arguments: json.RawMessage(`{"path":"b.go"}`),
				}},
			}, nil
		default:
			return &providers.Completion{FinishReason: string(providers.FinishStop), Text: "Found the solution."}, nil
		}
	}}

	worker, session := driverAgent(t, provider, &probeTool{name: "read_file"}, life)
	hooks, guard := repeatHooks(repeat.Config{Thresholds: []int{3, 5, 8}})
	worker.hooks = hooks

	answer, err := worker.Run(context.Background(), session, "Find foo in codebase")
	if err != nil || answer != "Found the solution." {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	chain := guard.Chain()
	if chain == nil || chain.Count != 1 || !strings.Contains(chain.Key, `"path":"b.go"`) {
		t.Fatalf("changed arguments did not reset repeat chain: %#v", chain)
	}

	messages := session.DeriveMessages()
	resultIndex := -1
	for i, message := range messages {
		if message.Role == models.RoleTool && message.ToolCallID == "call_3" {
			resultIndex = i
			break
		}
	}
	if resultIndex < 0 || resultIndex+1 >= len(messages) {
		t.Fatalf("third tool result or following reminder missing: %#v", messages)
	}
	if !strings.Contains(messages[resultIndex].Content, "ok") {
		t.Fatalf("tool result was replaced by advisory: %#v", messages[resultIndex])
	}
	if messages[resultIndex+1].Role != models.RoleUser || !strings.Contains(messages[resultIndex+1].Content, "You are repeating") {
		t.Fatalf("repeat reminder must follow the persisted result: %#v", messages[resultIndex+1])
	}
}

func hasRepeatReminder(messages []models.Message) bool {
	for _, message := range messages {
		if message.Role == models.RoleUser && strings.Contains(message.Content, "You are repeating") {
			return true
		}
	}
	return false
}
