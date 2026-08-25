package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// ExitPlanMode implements the model-facing exit_plan_mode tool.
type ExitPlanMode struct {
	Questions *questions.Service
}

func NewExitPlanMode(questions *questions.Service) *ExitPlanMode {
	return &ExitPlanMode{Questions: questions}
}

func (*ExitPlanMode) Name() string { return "exit_plan_mode" }

func (*ExitPlanMode) Description() string {
	return "Submit the final decision-complete Markdown plan for user review and explicit approval to exit Plan Mode."
}

func (*ExitPlanMode) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan": map[string]any{
				"type":        "string",
				"description": "The complete, decision-complete Markdown plan. Must start with '# ' (e.g. '# Implementation Plan')",
			},
		},
		"required": []string{"plan"},
	}
}

func (*ExitPlanMode) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (t *ExitPlanMode) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	sessionID := tools.ProgressScopeFromContext(ctx).SessionID

	var req struct {
		Plan string `json:"plan"`
	}

	if err := tools.ParseInput(input, &req); err != nil {
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "Invalid input format: " + err.Error(),
		}, nil
	}

	planText := strings.TrimSpace(req.Plan)
	if planText == "" || !strings.HasPrefix(planText, "# ") {
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "plan must be a non-empty Markdown string starting with '# ' (e.g. '# Feature Implementation Plan')",
		}, nil
	}

	if t.Questions == nil {
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "NO_QUESTION_PROVIDER: No user question provider is available to review the plan.",
		}, nil
	}

	reviewReq := questions.Request{
		SessionID: sessionID,
		Questions: []questions.Question{
			{
				ID:       "plan_review",
				Header:   "Plan Review",
				Question: "Please review the proposed implementation plan:",
				Detail:   planText,
				Options: []questions.QuestionOption{
					{
						Label:       "Approve",
						Description: "Approve the plan and exit Plan Mode to begin implementation",
					},
					{
						Label:       "Keep planning",
						Description: "Reject the plan or request revisions with feedback",
					},
				},
				MultiSelect: false,
				Intent:      questions.IntentConfirmation,
			},
		},
	}

	answerSet, err := t.Questions.Ask(ctx, reviewReq)
	if err != nil {
		if errors.Is(err, questions.ErrNoQuestionProvider) {
			return &tools.ToolResult{
				Status:    tools.ToolStatusFailed,
				Success:   false,
				Retryable: true,
				Message:   "NO_QUESTION_PROVIDER: No user question provider is available to review the plan.",
			}, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "The plan review was dismissed. Stay in Plan Mode and wait for the user's next message.",
		}, nil
	}

	var reviewAns *questions.Answer
	for _, ans := range answerSet.Answers {
		if ans.ID == "plan_review" {
			a := ans
			reviewAns = &a
			break
		}
	}

	if reviewAns == nil && len(answerSet.Answers) > 0 {
		reviewAns = &answerSet.Answers[0]
	}

	approved := false
	feedback := ""

	if reviewAns != nil {
		hasApproveOption := false
		for _, sel := range reviewAns.Selected {
			if strings.EqualFold(strings.TrimSpace(sel), "Approve") {
				hasApproveOption = true
			}
		}

		custom := strings.TrimSpace(reviewAns.Custom)
		if hasApproveOption && custom == "" {
			approved = true
		} else if custom != "" {
			feedback = custom
			if strings.EqualFold(custom, "approve") || strings.EqualFold(custom, "approved") || strings.EqualFold(custom, "yes") {
				approved = true
			}
		} else {
			for _, sel := range reviewAns.Selected {
				if strings.EqualFold(strings.TrimSpace(sel), "Keep planning") {
					feedback = "User selected 'Keep planning'"
				}
			}
		}
	}

	if approved {
		return &tools.ToolResult{
			Status:              tools.ToolStatusCompleted,
			Success:             true,
			Message:             "Plan approved — Plan Mode will exit before the next step.",
			Data:                map[string]any{"plan": planText, "approved": true},
			RequestPlanModeExit: true,
		}, nil
	}

	if feedback == "" {
		feedback = "User did not approve the plan"
	}

	return &tools.ToolResult{
		Status:    tools.ToolStatusFailed,
		Success:   false,
		Retryable: true,
		Message:   fmt.Sprintf("Plan not approved: %s. Stay in Plan Mode, incorporate the feedback, and revise the plan.", feedback),
		Data:      map[string]any{"plan": planText, "approved": false, "feedback": feedback},
	}, nil
}
