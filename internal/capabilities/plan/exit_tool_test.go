package plan_test

import (
	"context"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/capabilities/plan"
	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type mockQuestionProvider struct {
	answers questions.AnswerSet
	err     error
}

func (m *mockQuestionProvider) Ask(ctx context.Context, req questions.Request) (questions.AnswerSet, error) {
	return m.answers, m.err
}

func TestExitPlanModeValidationAndReviewFlow(t *testing.T) {
	qService := questions.NewService(nil)
	exitTool := plan.NewExitPlanMode(qService)

	ctx := tools.WithProgressScope(context.Background(), tools.ProgressScope{SessionID: "s1"})

	// 1. Invalid Markdown (missing '# ' title) -> failed.
	res, err := exitTool.Execute(ctx, map[string]any{"plan": "No title heading"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Status != tools.ToolStatusFailed {
		t.Fatalf("expected failure when plan has no '# ' heading, got: %#v", res)
	}

	// 2. No question provider -> failed with NO_QUESTION_PROVIDER
	res, err = exitTool.Execute(ctx, map[string]any{"plan": "# Implementation Plan\n1. Do thing"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Status != tools.ToolStatusFailed {
		t.Fatalf("expected failure without provider, got: %#v", res)
	}

	// 3. User provides feedback / rejects, so no transition is requested.
	qService.SetProvider(&mockQuestionProvider{
		answers: questions.AnswerSet{
			Answers: []questions.Answer{
				{ID: "plan_review", Custom: "Please use PostgreSQL instead of SQLite"},
			},
		},
	})
	res, err = exitTool.Execute(ctx, map[string]any{"plan": "# Implementation Plan\n1. Use SQLite"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.RequestPlanModeExit {
		t.Fatalf("expected plan rejection to avoid an exit request, got res=%#v", res)
	}

	// 4. User approval requests an Agent-owned transition.
	qService.SetProvider(&mockQuestionProvider{
		answers: questions.AnswerSet{
			Answers: []questions.Answer{
				{ID: "plan_review", Selected: []string{"Approve"}},
			},
		},
	})
	res, err = exitTool.Execute(ctx, map[string]any{"plan": "# Implementation Plan\n1. Use PostgreSQL"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Status != tools.ToolStatusCompleted || !res.RequestPlanModeExit {
		t.Fatalf("expected approval to request Agent-owned exit, got res=%#v", res)
	}
}
