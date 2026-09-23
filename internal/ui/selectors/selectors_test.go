package selectors_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/AbhaySingh002/supremo/internal/ui/selectors"
	"github.com/AbhaySingh002/supremo/internal/ui/theme"
)

func TestCommandMenuFiltersAndNavigatesIndependently(t *testing.T) {
	menu := selectors.NewCommandMenu([]selectors.Command{
		{Name: "/plan", Description: "Draft a plan"},
		{Name: "/provider", Description: "Choose a provider"},
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
	if !ok || selected.Name != "/provider" {
		t.Fatalf("expected /provider selected after down arrow, got %+v", selected)
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

func runFast(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() {
		ch <- cmd()
	}()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(20 * time.Millisecond):
		return nil
	}
}

func updateSelector(s selectors.ProviderSelector, msg tea.Msg) (selectors.ProviderSelector, tea.Cmd) {
	updated, cmd := s.Update(msg)
	s = updated.(selectors.ProviderSelector)
	if cmd != nil {
		res := runFast(cmd)
		if batch, ok := res.(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					subMsg := runFast(c)
					if subMsg != nil {
						subUpdated, _ := s.Update(subMsg)
						s = subUpdated.(selectors.ProviderSelector)
					}
				}
			}
		} else if res != nil {
			subUpdated, _ := s.Update(res)
			s = subUpdated.(selectors.ProviderSelector)
		}
	}
	return s, cmd
}

func TestModelSelectorFilterNavigationAndSelection(t *testing.T) {
	models := []selectors.Provider{
		{ID: "gpt-4.1", Name: "GPT-4.1", Description: "Flagship model"},
		{ID: "claude-3-7-sonnet", Name: "Claude 3.7 Sonnet", Description: "Anthropic model"},
		{ID: "claude-3-5-haiku", Name: "Claude 3.5 Haiku", Description: "Fast Anthropic model"},
	}
	selector := selectors.NewModelSelector(models, theme.Default())

	// Start filtering with '/' and type 'claude'
	selector, _ = updateSelector(selector, tea.KeyPressMsg{Code: '/', Text: "/"})
	for _, char := range "claude" {
		selector, _ = updateSelector(selector, tea.KeyPressMsg{Code: rune(char), Text: string(char)})
	}

	// Verify first filtered item is highlighted
	selected, ok := selector.Selected()
	if !ok || selected.ID != "claude-3-7-sonnet" {
		t.Fatalf("expected claude-3-7-sonnet selected first, got %+v", selected)
	}

	// Down arrow navigates to next item
	selector, _ = updateSelector(selector, tea.KeyPressMsg{Code: tea.KeyDown})
	selected, ok = selector.Selected()
	if !ok || selected.ID != "claude-3-5-haiku" {
		t.Fatalf("expected claude-3-5-haiku selected after down arrow, got %+v", selected)
	}

	// Hit Enter to select while filtering
	_, cmd := selector.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected Enter while filtering to emit selection command")
	}
	msg := cmd()
	selectedMsg, ok := msg.(selectors.ModelSelectedMsg)
	if !ok || selectedMsg.ID != "claude-3-5-haiku" {
		t.Fatalf("expected claude-3-5-haiku selected, got %#v", msg)
	}
}

func TestModelSelectorGroupedProviderView(t *testing.T) {
	models := []selectors.Provider{
		{ID: "gpt-4o", ProviderID: "openai", ProviderName: "OpenAI", Name: "GPT-4o", Description: "128k", Active: true},
		{ID: "o3-mini", ProviderID: "openai", ProviderName: "OpenAI", Name: "o3-mini", Description: "200k"},
		{ID: "nemotron-3.5", ProviderID: "opencode-zen", ProviderName: "OpenCode Zen", Name: "Nemotron 3.5 Lightning Free", Description: "Free"},
	}
	selector := selectors.NewModelSelector(models, theme.Default())
	selector.SetSize(80, 24)

	view := selector.View().Content
	if !strings.Contains(view, "Search") {
		t.Fatalf("expected search prompt 'Search', got:\n%s", view)
	}
	if !strings.Contains(view, "esc") {
		t.Fatalf("expected 'esc' key hint, got:\n%s", view)
	}
	if !strings.Contains(view, "OpenAI") {
		t.Fatalf("expected provider section header 'OpenAI', got:\n%s", view)
	}
	if !strings.Contains(view, "OpenCode Zen") {
		t.Fatalf("expected provider section header 'OpenCode Zen', got:\n%s", view)
	}
	if !strings.Contains(view, "Nemotron 3.5 Lightning Free") {
		t.Fatalf("expected model name 'Nemotron 3.5 Lightning Free', got:\n%s", view)
	}
	if !strings.Contains(view, "128k") {
		t.Fatalf("expected context length tag '128k', got:\n%s", view)
	}
}

func TestModelSelectorMouseWheelScroll(t *testing.T) {
	models := []selectors.Provider{
		{ID: "gpt-4o", Name: "GPT-4o"},
		{ID: "o3-mini", Name: "o3-mini"},
		{ID: "claude-3-7-sonnet", Name: "Claude 3.7 Sonnet"},
	}
	selector := selectors.NewModelSelector(models, theme.Default())
	selector.SetSize(80, 24)

	// Cursor starts at 0
	selected, ok := selector.Selected()
	if !ok || selected.ID != "gpt-4o" {
		t.Fatalf("expected gpt-4o, got %+v", selected)
	}

	// Trackpad / mouse wheel down scrolls down
	updated, _ := selector.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	selector = updated.(selectors.ProviderSelector)
	selected, ok = selector.Selected()
	if !ok || selected.ID != "o3-mini" {
		t.Fatalf("expected o3-mini after wheel down, got %+v", selected)
	}

	// Trackpad / mouse wheel up scrolls back up
	updated, _ = selector.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	selector = updated.(selectors.ProviderSelector)
	selected, ok = selector.Selected()
	if !ok || selected.ID != "gpt-4o" {
		t.Fatalf("expected gpt-4o after wheel up, got %+v", selected)
	}
}
