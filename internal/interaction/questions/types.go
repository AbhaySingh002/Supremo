package questions

import (
	"context"
	"errors"
)

type QuestionIntent string

const (
	IntentClarification QuestionIntent = "clarification"
	IntentChoice        QuestionIntent = "choice"
	IntentConfirmation  QuestionIntent = "confirmation"
)

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type Question struct {
	ID          string           `json:"id"`
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	Detail      string           `json:"detail,omitempty"`
	Options     []QuestionOption `json:"options,omitempty"`
	MultiSelect bool             `json:"multi_select,omitempty"`
	Intent      QuestionIntent   `json:"intent,omitempty"`
}

type Answer struct {
	ID       string   `json:"id"`
	Selected []string `json:"selected,omitempty"`
	Custom   string   `json:"custom,omitempty"`
}

type AnswerSet struct {
	Answers []Answer `json:"answers"`
}

type Request struct {
	SessionID string     `json:"session_id,omitempty"`
	RunID     string     `json:"run_id,omitempty"`
	Questions []Question `json:"questions"`
}

var ErrNoQuestionProvider = errors.New("NO_QUESTION_PROVIDER")

type Provider interface {
	Ask(ctx context.Context, req Request) (AnswerSet, error)
}
