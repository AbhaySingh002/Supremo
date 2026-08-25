package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/hostexec"
	"github.com/AbhaySingh002/supremo/internal/ui/plan"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
	"github.com/AbhaySingh002/supremo/internal/ui/terminal"
)

func TestResponsiveFooterHelp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "help-test"}, ctx, cancel)

	// 1. Normal composer footer
	model.width, model.height = 100, 30
	model.layout()
	footer := model.FooterView()
	if !strings.Contains(footer, "send") || !strings.Contains(footer, "plans") {
		t.Fatalf("expected composer hints in footer, got:\n%s", footer)
	}

	// 2. Narrow width auto-truncation via help.Model
	model.width = 30
	model.layout()
	narrowFooter := model.FooterView()
	if len(narrowFooter) == 0 {
		t.Fatal("narrow footer should not be empty")
	}

	// 3. Plan Question mode footer
	req := api.QuestionRequest{Questions: []api.Question{
		{ID: "q1", Question: "Pick database", Options: []api.QuestionOption{{Label: "SQLite"}, {Label: "Postgres"}}},
	}}
	model.planQuestion = plan.NewPlanQuestionModel(req, rendering.NewStyles(), 80)
	model.surface = surfacePlanQuestion
	qFooter := model.FooterView()
	if !strings.Contains(qFooter, "pick") {
		t.Fatalf("expected plan question hints in footer, got:\n%s", qFooter)
	}

	// 4. Custom answer sub-mode
	model.planQuestion.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	customFooter := model.FooterView()
	if !strings.Contains(customFooter, "submit") || !strings.Contains(customFooter, "cancel") {
		t.Fatalf("expected custom answer hints in footer, got:\n%s", customFooter)
	}
}

func TestOverlayTextInputKryptonConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "krypton-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.layout()

	// Open /krypton overlay
	model.openKryptonOverlay()
	if model.surface != surfaceKrypton {
		t.Fatal("expected Krypton surface to be active")
	}

	// Incomplete input should fail confirmation
	for _, ch := range "KRYP" {
		updated, _ := model.Update(tea.KeyPressMsg{Code: rune(ch), Text: string(ch)})
		model = updated.(Model)
	}
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)
	if model.overlayError == "" {
		t.Fatal("expected error on incomplete KRYPTON input")
	}

	// Type remaining "TON"
	for _, ch := range "TON" {
		updated, _ := model.Update(tea.KeyPressMsg{Code: rune(ch), Text: string(ch)})
		model = updated.(Model)
	}

	// Verify overlayView contains textinput view
	view := model.overlayView()
	if !strings.Contains(view, "KRYPTON") {
		t.Fatalf("expected KRYPTON in overlay view, got:\n%s", view)
	}

	// Press Esc to dismiss
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(Model)
	if model.surface != surfaceNone {
		t.Fatal("expected overlay to close on Esc")
	}
}

func TestViewportBoundaryAndHomeEndNavigation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "scroll-test"}, ctx, cancel)
	model.width, model.height = 80, 10
	model.layout()

	for i := 0; i < 40; i++ {
		model.appendEntry(entryUser, "Message line")
	}
	model.rebuildFeed()

	// Initial position: at bottom, following tail
	if !model.followTail {
		t.Fatal("expected followTail true initially")
	}

	// Home jumps to top
	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	model = updated.(Model)
	if model.followTail {
		t.Fatal("expected followTail false after Home")
	}
	if model.feed.YOffset() != 0 {
		t.Fatalf("expected YOffset 0 after Home, got %d", model.feed.YOffset())
	}

	// End jumps to bottom and restores followTail
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	model = updated.(Model)
	if !model.followTail {
		t.Fatal("expected followTail true after End")
	}
	if model.newOutput != 0 {
		t.Fatalf("expected newOutput 0 after End, got %d", model.newOutput)
	}
}

func TestWelcomeViewEmptyState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "welcome-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.layout()

	// Empty state shows welcome card
	body := model.bodyView()
	if !strings.Contains(body, "SUPREMO") || !strings.Contains(body, "Agentic coding") {
		t.Fatalf("expected welcome card in empty state, got:\n%s", body)
	}

	// Appending an entry immediately transitions to feed
	model.appendEntry(entryUser, "Hello Supremo")
	bodyAfter := model.bodyView()
	if strings.Contains(bodyAfter, "Agentic coding paired with your local workspace") {
		t.Fatalf("welcome card should disappear once entries exist, got:\n%s", bodyAfter)
	}
}

func TestFooterViewFocusFeedAndSelector(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "footer-focus-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.layout()

	// 1. When focusFeed is true, footer shows feed navigation keys
	model.focus = focusTranscript
	feedFooter := model.FooterView()
	if !strings.Contains(feedFooter, "scroll") {
		t.Fatalf("expected feed scroll hints when focusFeed is active, got:\n%s", feedFooter)
	}

	// 2. When openProviderSelector is active, footer shows selector keys
	model.focus = focusComposer
	model.openProviderSelector()
	selFooter := model.FooterView()
	if !strings.Contains(selFooter, "select") || !strings.Contains(selFooter, "close") {
		t.Fatalf("expected selector hints when provider selector is open, got:\n%s", selFooter)
	}
}

func TestModelSelectorDismissalRestoresLayoutAndHeight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "model-dismiss-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.layout()

	initialFeedHeight := model.feed.Height()

	model.modelCatalog = []api.Provider{{
		ID: "openai", Name: "OpenAI", Configured: true,
		Models: []api.Model{{ID: "gpt-4", Name: "GPT-4"}},
	}}
	if !model.openModelSelector() {
		t.Fatal("expected openModelSelector to succeed")
	}

	if model.surface != surfaceModel {
		t.Fatalf("expected surfaceModel, got %v", model.surface)
	}
	if model.feed.Height() <= initialFeedHeight {
		t.Fatalf("expected modal feed height > %d, got %d", initialFeedHeight, model.feed.Height())
	}

	// Dismiss with Esc
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(Model)
	if cmd != nil {
		updated, _ = model.Update(cmd())
		model = updated.(Model)
	}

	if model.surface != surfaceNone {
		t.Fatalf("expected surfaceNone after Esc, got %v", model.surface)
	}
	if model.feed.Height() != initialFeedHeight {
		t.Fatalf("expected restored feed height %d, got %d", initialFeedHeight, model.feed.Height())
	}

	rendered := model.View().Content
	if height := lipgloss.Height(rendered); height > model.height {
		t.Fatalf("expected rendered height <= %d, got %d", model.height, height)
	}
}

func TestProviderSelectorDismissalRestoresLayoutAndHeight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "provider-dismiss-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.providers = []api.Provider{{ID: "openai", Name: "OpenAI", Configured: true}}
	model.layout()

	initialFeedHeight := model.feed.Height()

	model.openProviderSelector()
	if model.surface != surfaceProvider {
		t.Fatalf("expected surfaceProvider, got %v", model.surface)
	}
	if model.feed.Height() <= initialFeedHeight {
		t.Fatalf("expected modal feed height > %d, got %d", initialFeedHeight, model.feed.Height())
	}

	// Dismiss with Esc
	updated, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(Model)
	if cmd != nil {
		updated, _ = model.Update(cmd())
		model = updated.(Model)
	}

	if model.surface != surfaceNone {
		t.Fatalf("expected surfaceNone after Esc, got %v", model.surface)
	}
	if model.feed.Height() != initialFeedHeight {
		t.Fatalf("expected restored feed height %d, got %d", initialFeedHeight, model.feed.Height())
	}

	rendered := model.View().Content
	if height := lipgloss.Height(rendered); height > model.height {
		t.Fatalf("expected rendered height <= %d, got %d", model.height, height)
	}
}

func TestLocalShellCommandExpandsOutputByDefault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "local-shell-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.layout()

	// Run ! pwd
	model.startShell("pwd")
	if len(model.entries) < 1 {
		t.Fatalf("expected at least 1 entry, got %d", len(model.entries))
	}
	if !model.entries[0].expanded {
		t.Fatal("expected running local shell entry to be expanded by default")
	}

	taskID := model.active.id

	// Receive shell output
	updated, _ := model.Update(terminal.ShellResultMsg{
		ID:      taskID,
		Command: "pwd",
		Output: hostexec.Output{
			Stdout:   []byte("/Users/test/workspace\n"),
			ExitCode: 0,
		},
	})
	model = updated.(Model)

	if len(model.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(model.entries))
	}
	if !model.entries[0].expanded {
		t.Fatal("expected completed local shell entry to remain expanded")
	}
	if model.entries[0].toolStatus != "completed" {
		t.Fatalf("expected completed status, got %s", model.entries[0].toolStatus)
	}

	rendered := model.renderEntry(0, model.entries[0])
	if !strings.Contains(rendered, "/Users/test/workspace") {
		t.Fatalf("expected shell output in rendered entry, got:\n%s", rendered)
	}
}

func TestComposerShiftEnterExpandsAndScrolls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "shift-enter-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.layout()

	// Initial height should be 1
	if h := model.input.Height(); h != 1 {
		t.Fatalf("expected initial composer height 1, got %d", h)
	}

	// 1. Type "line 1"
	updated, _ := model.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	model = updated.(Model)
	model.input.SetValue("line 1")
	model.input.CursorEnd()
	model.resizeComposer()

	if h := model.input.Height(); h != 1 {
		t.Fatalf("expected composer height 1 for single line, got %d", h)
	}

	// 2. Press Shift+Enter (via ModShift)
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	model = updated.(Model)

	if !strings.Contains(model.input.Value(), "\n") {
		t.Fatalf("expected newline inserted into input value, got %q", model.input.Value())
	}
	if h := model.input.Height(); h != 2 {
		t.Fatalf("expected composer height 2 after Shift+Enter, got %d", h)
	}

	// 3. Press Alt+Enter
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	model = updated.(Model)
	if h := model.input.Height(); h != 3 {
		t.Fatalf("expected composer height 3 after Alt+Enter, got %d", h)
	}

	// 4. Press Ctrl+Enter
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	model = updated.(Model)
	if h := model.input.Height(); h != 4 {
		t.Fatalf("expected composer height 4 after Ctrl+Enter, got %d", h)
	}

	// 5. Expand beyond maxComposerRows (height is 24, so max rows is 6)
	for i := 0; i < 6; i++ {
		updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
		model = updated.(Model)
	}

	maxRows := model.maxComposerRows()
	if h := model.input.Height(); h != maxRows {
		t.Fatalf("expected composer height capped at maxComposerRows %d, got %d", maxRows, h)
	}

	// The input has 10 lines now (line 1 + 9 newlines). Cursor should be at the end, causing inputOffset to scroll.
	if model.inputOffset <= 0 {
		t.Fatalf("expected inputOffset > 0 when lines exceed maxComposerRows, got %d", model.inputOffset)
	}

	// View should render scroll indicator
	inputView := model.inputView()
	if !strings.Contains(inputView, "lines") || !strings.Contains(inputView, "of 10") {
		t.Fatalf("expected scroll indicator in inputView, got:\n%s", inputView)
	}

	// 6. Test scrolling up and down with scrollComposer
	prevOffset := model.inputOffset
	if !model.scrollComposer(-1) {
		t.Fatal("expected scrollComposer(-1) to succeed")
	}
	if model.inputOffset != prevOffset-1 {
		t.Fatalf("expected offset %d, got %d", prevOffset-1, model.inputOffset)
	}

	// 7. Plain Enter should submit the input (or try to send) and reset composer height after acceptance
	model.input.SetValue("multi\nline\nprompt")
	model.resizeComposer()
	if h := model.input.Height(); h != 3 {
		t.Fatalf("expected composer height 3 for 3 lines, got %d", h)
	}
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)

	// Accept prompt
	if model.active != nil {
		updated, _ = model.Update(promptAcceptedMsg{
			id:      model.active.id,
			prompt:  "multi\nline\nprompt",
			display: "multi\nline\nprompt",
			receipt: api.Receipt{Accepted: true, RunID: "test-run"},
		})
		model = updated.(Model)
	}

	if h := model.input.Height(); h != 1 {
		t.Fatalf("expected composer height reset to 1 after submit and acceptance, got %d", h)
	}
	if model.inputOffset != 0 {
		t.Fatalf("expected composer inputOffset reset to 0 after submit and acceptance, got %d", model.inputOffset)
	}
}

func TestComposerBackslashContinuationAndCtrlO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "backslash-test"}, ctx, cancel)
	model.width, model.height = 80, 24
	model.layout()

	// 1. Backslash at end of line + plain Enter inserts a newline instead of submitting
	model.input.SetValue("hello world\\")
	model.input.CursorEnd()
	model.resizeComposer()

	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	model = updated.(Model)

	if val := model.input.Value(); val != "hello world\n" {
		t.Fatalf("expected backslash replaced with newline, got %q", val)
	}
	if h := model.input.Height(); h != 2 {
		t.Fatalf("expected composer height 2, got %d", h)
	}
	if model.active != nil {
		t.Fatal("expected prompt NOT to submit when backslash continuation is used")
	}

	// 2. Ctrl+O inserts a newline
	updated, _ = model.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	model = updated.(Model)

	if val := model.input.Value(); val != "hello world\n\n" {
		t.Fatalf("expected newline inserted via Ctrl+O, got %q", val)
	}
	if h := model.input.Height(); h != 3 {
		t.Fatalf("expected composer height 3, got %d", h)
	}
}
