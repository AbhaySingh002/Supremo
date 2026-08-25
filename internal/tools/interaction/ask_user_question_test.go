package interaction_test

import (
	"context"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/tools"
	"github.com/AbhaySingh002/supremo/internal/tools/interaction"
)

type mockProvider struct {
	answers questions.AnswerSet
	err     error
}

func (m *mockProvider) Ask(ctx context.Context, req questions.Request) (questions.AnswerSet, error) {
	return m.answers, m.err
}

func TestAskUserQuestionValidationAndExecution(t *testing.T) {
	service := questions.NewService(nil)
	tool := interaction.NewAskUserQuestion(service)

	ctx := context.Background()

	// 1. Rejects when called from delegated subagent
	subagentCtx := interaction.WithDelegatedAgent(ctx, true)
	res, err := tool.Execute(subagentCtx, map[string]any{
		"questions": []any{
			map[string]any{"id": "q1", "question": "Which database?"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Status != tools.ToolStatusFailed {
		t.Fatalf("expected ToolStatusFailed when called from delegated agent, got: %#v", res)
	}

	// 2. Rejects empty questions list
	res, err = tool.Execute(ctx, map[string]any{"questions": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Status != tools.ToolStatusFailed {
		t.Fatalf("expected ToolStatusFailed for empty questions, got: %#v", res)
	}

	// 3. Fails cleanly when no provider is registered
	res, err = tool.Execute(ctx, map[string]any{
		"questions": []any{
			map[string]any{"id": "q1", "question": "Which database?"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Status != tools.ToolStatusFailed {
		t.Fatalf("expected ToolStatusFailed when no provider registered, got: %#v", res)
	}

	// 4. Successfully prompts provider and formats answers
	service.SetProvider(&mockProvider{
		answers: questions.AnswerSet{
			Answers: []questions.Answer{
				{ID: "q1", Selected: []string{"PostgreSQL"}},
			},
		},
	})
	res, err = tool.Execute(ctx, map[string]any{
		"questions": []any{
			map[string]any{
				"id":       "q1",
				"question": "Which database?",
				"options": []any{
					map[string]any{"label": "PostgreSQL"},
					map[string]any{"label": "SQLite"},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Status != tools.ToolStatusCompleted {
		t.Fatalf("expected ToolStatusCompleted, got: %#v", res)
	}
	if res.Message == "" {
		t.Fatal("expected formatted response message")
	}
}
