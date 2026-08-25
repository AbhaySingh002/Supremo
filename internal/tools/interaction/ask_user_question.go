package interaction

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/interaction/questions"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// WithDelegatedAgent marks context as executing under a delegated subagent.
func WithDelegatedAgent(ctx context.Context, delegated bool) context.Context {
	if delegated {
		return tools.WithDelegated(ctx)
	}
	return ctx
}

func isDelegatedAgent(ctx context.Context) bool {
	return tools.IsDelegated(ctx)
}

// AskUserQuestion enables the model to ask structured questions to resolve user-owned ambiguity.
type AskUserQuestion struct {
	Service *questions.Service
}

func NewAskUserQuestion(service *questions.Service) *AskUserQuestion {
	return &AskUserQuestion{Service: service}
}

func (*AskUserQuestion) Name() string { return "ask_user_question" }

func (*AskUserQuestion) Description() string {
	return "Ask the user structured questions to resolve material ambiguity, preferences, or product choices that cannot be discovered by inspecting the repository."
}

func (*AskUserQuestion) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"description": "List of structured questions to present to the user.",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"id", "question"},
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "Stable unique identifier for this question",
						},
						"question": map[string]any{
							"type":        "string",
							"description": "The question to ask the user",
						},
						"header": map[string]any{
							"type":        "string",
							"description": "Short category header (e.g. 'Compatibility')",
						},
						"detail": map[string]any{
							"type":        "string",
							"description": "Detailed explanation of background and tradeoffs",
						},
						"options": map[string]any{
							"type":        "array",
							"description": "Selectable options",
							"items": map[string]any{
								"type":     "object",
								"required": []string{"label"},
								"properties": map[string]any{
									"label": map[string]any{
										"type":        "string",
										"description": "Short label for the choice",
									},
									"description": map[string]any{
										"type":        "string",
										"description": "Description of what choosing this option implies",
									},
								},
							},
						},
						"multi_select": map[string]any{
							"type":        "boolean",
							"description": "Whether multiple options may be selected",
						},
					},
				},
			},
		},
		"required": []string{"questions"},
	}
}

func (*AskUserQuestion) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (t *AskUserQuestion) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	if isDelegatedAgent(ctx) {
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "human interaction is unavailable from a delegated agent; return the unresolved decision to the parent",
		}, nil
	}

	var req struct {
		Questions []struct {
			ID       string `json:"id"`
			Question string `json:"question"`
			Header   string `json:"header"`
			Detail   string `json:"detail"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
			MultiSelect bool `json:"multi_select"`
		} `json:"questions"`
	}

	if err := tools.ParseInput(input, &req); err != nil {
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "Invalid input format: " + err.Error(),
		}, nil
	}

	if len(req.Questions) == 0 {
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "ask_user_question requires at least one question",
		}, nil
	}

	domainQuestions := make([]questions.Question, len(req.Questions))
	for i, q := range req.Questions {
		qID := strings.TrimSpace(q.ID)
		qText := strings.TrimSpace(q.Question)
		if qID == "" || qText == "" {
			return &tools.ToolResult{
				Status:    tools.ToolStatusFailed,
				Success:   false,
				Retryable: true,
				Message:   fmt.Sprintf("question at index %d must have non-empty 'id' and 'question'", i),
			}, nil
		}
		opts := make([]questions.QuestionOption, len(q.Options))
		for j, opt := range q.Options {
			opts[j] = questions.QuestionOption{
				Label:       strings.TrimSpace(opt.Label),
				Description: strings.TrimSpace(opt.Description),
			}
		}
		domainQuestions[i] = questions.Question{
			ID:          qID,
			Question:    qText,
			Header:      strings.TrimSpace(q.Header),
			Detail:      strings.TrimSpace(q.Detail),
			Options:     opts,
			MultiSelect: q.MultiSelect,
		}
	}

	if t == nil || t.Service == nil {
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "NO_QUESTION_PROVIDER: No user question provider is available to answer questions.",
		}, nil
	}

	scope := tools.ProgressScopeFromContext(ctx)
	answerSet, err := t.Service.Ask(ctx, questions.Request{SessionID: scope.SessionID, RunID: scope.RunID, Questions: domainQuestions})
	if err != nil {
		if errors.Is(err, questions.ErrNoQuestionProvider) {
			return &tools.ToolResult{
				Status:    tools.ToolStatusFailed,
				Success:   false,
				Retryable: true,
				Message:   "NO_QUESTION_PROVIDER: No user question provider is available to answer questions.",
			}, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return &tools.ToolResult{
			Status:    tools.ToolStatusFailed,
			Success:   false,
			Retryable: true,
			Message:   "Question interaction error: " + err.Error(),
		}, nil
	}

	var sb strings.Builder
	sb.WriteString("User answers received:\n")
	answersMap := make(map[string]any)
	for _, ans := range answerSet.Answers {
		parts := []string{}
		if len(ans.Selected) > 0 {
			parts = append(parts, strings.Join(ans.Selected, ", "))
		}
		if ans.Custom != "" {
			parts = append(parts, "Custom: "+ans.Custom)
		}
		summary := strings.Join(parts, "; ")
		if summary == "" {
			summary = "(empty answer)"
		}
		fmt.Fprintf(&sb, "- [%s]: %s\n", ans.ID, summary)
		answersMap[ans.ID] = map[string]any{
			"selected": ans.Selected,
			"custom":   ans.Custom,
		}
	}

	return &tools.ToolResult{
		Status:  tools.ToolStatusCompleted,
		Success: true,
		Message: strings.TrimSpace(sb.String()),
		Data:    map[string]any{"answers": answersMap},
	}, nil
}
