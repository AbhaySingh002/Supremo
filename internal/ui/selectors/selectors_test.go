package selectors_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/AbhaySingh002/supremo/internal/ui/selectors"
	"github.com/AbhaySingh002/supremo/internal/ui/theme"
)

func TestCommandMenuFiltersAndNavigatesIndependently(t *testing.T) {
	menu := selectors.NewCommandMenu([]selectors.Command{
		{Name: "/plan", Description: "Draft a plan"},
		{Name: "/providers", Description: "List providers"},
		{Name: "/help", Description: "Show help"},
	}, theme.Default())

	updated, _ := menu.Update(selectors.CommandQueryMsg{Query: "/pla"})
	menu = updated.(selectors.CommandMenu)
	if len(menu.Items()) != 1 {
		t.Fatalf("expected 1 match for /pla, got %d", len(menu.Items()))
	}
	selected, ok := menu.Selected()
	if !ok || selected.Name != "/plan" {
		t.Fatalf("expected /plan selected, got %+v", selected)
	}

	updated, _ = menu.Update(selectors.CommandQueryMsg{Query: "/"})
	menu = updated.(selectors.CommandMenu)
	updated, _ = menu.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	menu = updated.(selectors.CommandMenu)
	selected, ok = menu.Selected()
	if !ok || selected.Name != "/providers" {
		t.Fatalf("expected /providers selected after down arrow, got %+v", selected)
	}

	updated, _ = menu.Update(selectors.CommandQueryMsg{Query: "/"})
	menu = updated.(selectors.CommandMenu)
	selected, ok = menu.Selected()
	if !ok || selected.Name != "/plan" {
		t.Fatalf("expected query reset to select first match, got %+v", selected)
	}

	updated, _ = menu.Update(tea.WindowSizeMsg{Width: 40, Height: 6})
	menu = updated.(selectors.CommandMenu)
	if view := menu.View().Content; !strings.Contains(view, "/plan") {
		t.Fatalf("expected resized view to contain /plan, got:\n%s", view)
	}
}

func TestProviderSelectorWorksAsAnEmbeddedModel(t *testing.T) {
	selector := selectors.NewProviderSelector([]selectors.Provider{
		{ID: "gemini", Name: "Google Gemini", Description: "Gemini models", Active: true},
		{ID: "openai", Name: "OpenAI", Description: "GPT models"},
	}, theme.Default())

	updated, _ := selector.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	selector = updated.(selectors.ProviderSelector)
	selected, ok := selector.Selected()
	if !ok || selected.ID != "openai" {
		t.Fatalf("expected OpenAI selected after down arrow, got %+v", selected)
	}

	updated, cmd := selector.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	selector = updated.(selectors.ProviderSelector)
	if cmd == nil {
		t.Fatal("expected selection command")
	}
	msg := cmd()
	selectedMsg, ok := msg.(selectors.ProviderSelectedMsg)
	if !ok || selectedMsg.ID != "openai" {
		t.Fatalf("expected ProviderSelectedMsg for openai, got %#v", msg)
	}

	updated, _ = selector.Update(tea.WindowSizeMsg{Width: 16, Height: 4})
	selector = updated.(selectors.ProviderSelector)
	view := selector.View().Content
	for _, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 16 {
			t.Fatalf("compact line width %d exceeds target width 16 in line %q", width, line)
		}
	}
}

func TestProviderSelectorClearsFilterBeforeDismissing(t *testing.T) {
	selector := selectors.NewProviderSelector([]selectors.Provider{{ID: "mistral", Name: "Mistral", Description: "Mistral Conversations"}}, theme.Default())
	updated, _ := selector.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	selector = updated.(selectors.ProviderSelector)
	updated, _ = selector.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	selector = updated.(selectors.ProviderSelector)
	updated, cmd := selector.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected escape while filtered to clear filter rather than emitting dismissed message")
	}
	_ = updated.(selectors.ProviderSelector)
}

func TestModelSelectorUsesTheSameRadioListInteraction(t *testing.T) {
	models := []selectors.Provider{
		{ID: "gpt-4.1", Name: "GPT-4.1", Description: "Flagship model", Active: true},
		{ID: "gpt-4.1-mini", Name: "GPT-4.1 Mini", Description: "Fast model"},
	}
	selector := selectors.NewModelSelector(models, theme.Default())
	updated, _ := selector.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	selector = updated.(selectors.ProviderSelector)
	selected, ok := selector.Selected()
	if !ok || selected.ID != "gpt-4.1-mini" {
		t.Fatalf("expected mini model selected, got %+v", selected)
	}

	updated, cmd := selector.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected selection command")
	}
	_ = updated.(selectors.ProviderSelector)
	msg := cmd()
	selectedMsg, ok := msg.(selectors.ModelSelectedMsg)
	if !ok || selectedMsg.ID != "gpt-4.1-mini" {
		t.Fatalf("expected ModelSelectedMsg, got %#v", msg)
	}

	updated, cmd = selector.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected dismissal command on unfiltered escape")
	}
	msg = cmd()
	if _, ok := msg.(selectors.ProviderSelectorDismissedMsg); !ok {
		t.Fatalf("expected ProviderSelectorDismissedMsg, got %#v", msg)
	}
}
