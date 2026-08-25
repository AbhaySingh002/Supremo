package plan_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/plan"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

func TestPlanQuestionModelNavigationAndCustomAnswer(t *testing.T) {
	zone.NewGlobal()
	st := rendering.NewStyles()
	req := api.QuestionRequest{
		Questions: []api.Question{
			{
				ID:       "arch",
				Question: "Choose application architecture",
				Detail:   "Determines component layout.",
				Options: []api.QuestionOption{
					{Label: "Monolith", Description: "Recommended: Simple deployment"},
					{Label: "Microservices", Description: "Complex orchestration"},
				},
			},
		},
	}
	m := plan.NewPlanQuestionModel(req, st, 100)

	// 1. Initial view checks
	view := m.View(100, 20)
	if !strings.Contains(view, "PLAN DECISION") || !strings.Contains(view, "Choose application architecture") || !strings.Contains(view, "RECOMMENDED") {
		t.Fatalf("unexpected plan question view:\n%s", view)
	}

	// 2. Down arrow moves option
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if m.Option() != 1 {
		t.Fatalf("expected option 1, got %d", m.Option())
	}

	// 3. 'r' selects recommended option
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if m.Answers()["arch"] != "Monolith" {
		t.Fatalf("expected Monolith recorded, got %q", m.Answers()["arch"])
	}
	if cmd == nil {
		t.Fatal("expected completion command when single question answered")
	}

	// 4. Custom answer flow
	m = plan.NewPlanQuestionModel(req, st, 100)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if !m.CustomAnswer() {
		t.Fatal("expected custom answer mode active after 'c'")
	}
	customView := m.View(100, 20)
	if !strings.Contains(customView, "Custom answer:") {
		t.Fatalf("expected custom answer prompt in view:\n%s", customView)
	}

	// Esc cancels custom answer mode
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.CustomAnswer() {
		t.Fatal("expected Esc to exit custom answer mode")
	}
}

func TestPlanQuestionIsHeightBoundedAndScrollable(t *testing.T) {
	zone.NewGlobal()
	options := make([]api.QuestionOption, 12)
	for i := range options {
		options[i] = api.QuestionOption{Label: fmt.Sprintf("Option %d", i+1), Description: strings.Repeat("wrapped tradeoff ", 6)}
	}
	m := plan.NewPlanQuestionModel(api.QuestionRequest{Questions: []api.Question{{
		ID: "long", Question: strings.Repeat("Long architectural question ", 6), Detail: strings.Repeat("Background detail ", 12), Options: options,
	}}}, rendering.NewStyles(), 60)

	view := m.View(60, 12)
	if height := lipgloss.Height(view); height > 12 {
		t.Fatalf("plan view height = %d, want <= 12\n%s", height, ansi.Strip(view))
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if bottom := ansi.Strip(zone.Scan(m.View(60, 12))); !strings.Contains(bottom, "Option 12") {
		t.Fatalf("End did not reveal final option:\n%s", bottom)
	}
	m.SetOption(11)
	if selected := ansi.Strip(zone.Scan(m.View(60, 12))); !strings.Contains(selected, "Option 12") {
		t.Fatalf("selected option was not revealed:\n%s", selected)
	}
}

func TestPlanQuestionShortcuts(t *testing.T) {
	st := rendering.NewStyles()
	req := api.QuestionRequest{
		Questions: []api.Question{
			{
				ID:       "deploy",
				Question: "Confirm deployment?",
				Options: []api.QuestionOption{
					{Label: "Yes, deploy now"},
					{Label: "No, cancel"},
				},
			},
		},
	}

	// Test 'y' shortcut selects "Yes, deploy now"
	m := plan.NewPlanQuestionModel(req, st, 100)
	m, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if m.Answers()["deploy"] != "Yes, deploy now" {
		t.Fatalf("expected 'y' to match Yes option, got %q", m.Answers()["deploy"])
	}
	if cmd == nil {
		t.Fatal("expected completion cmd")
	}

	// Test 'n' shortcut selects "No, cancel"
	m = plan.NewPlanQuestionModel(req, st, 100)
	m, cmd = m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.Answers()["deploy"] != "No, cancel" {
		t.Fatalf("expected 'n' to match No option, got %q", m.Answers()["deploy"])
	}
	if cmd == nil {
		t.Fatal("expected completion cmd")
	}
}

func TestPlanQuestionMultiQuestionProgression(t *testing.T) {
	st := rendering.NewStyles()
	req := api.QuestionRequest{
		Questions: []api.Question{
			{
				ID:       "q1",
				Question: "Frontend framework?",
				Options: []api.QuestionOption{
					{Label: "React"},
					{Label: "Vue"},
				},
			},
			{
				ID:       "q2",
				Question: "Styling library?",
				Options: []api.QuestionOption{
					{Label: "Tailwind"},
					{Label: "CSS Modules"},
				},
			},
		},
	}
	m := plan.NewPlanQuestionModel(req, st, 100)

	// Answer q1 with '2' (Vue)
	m, cmd := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	if cmd != nil {
		t.Fatal("should not complete after first question of multi-question set")
	}
	if m.Question() != 1 {
		t.Fatalf("expected advance to question 1, got %d", m.Question())
	}
	if m.Answers()["q1"] != "Vue" {
		t.Fatalf("expected answer 'Vue', got %q", m.Answers()["q1"])
	}

	// Answer q2 with Enter (defaults to index 0: Tailwind)
	m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected completion cmd after final question")
	}
	if m.Answers()["q2"] != "Tailwind" {
		t.Fatalf("expected answer 'Tailwind', got %q", m.Answers()["q2"])
	}
	if !m.IsComplete() {
		t.Fatal("expected IsComplete true")
	}
}
