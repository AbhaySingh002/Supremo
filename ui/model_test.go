package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/internal/providers"
)

func newTestModel(t *testing.T) Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := New(nil, &agent.Session{ID: "test", PlanMode: true}, ctx, cancel)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	return updated.(Model)
}

func renderedRow(t *testing.T, view, text string) int {
	t.Helper()
	for row, line := range strings.Split(view, "\n") {
		if strings.Contains(line, text) {
			return row
		}
	}
	t.Fatalf("rendered view is missing %q:\n%s", text, view)
	return -1
}

func newProviderApplication(t *testing.T, apiKey string) *app.App {
	t.Helper()
	dir := t.TempDir()
	store := providers.NewFileCredentialStore(dir)
	if apiKey != "" {
		if err := store.SetAPIKey("gemini", apiKey); err != nil {
			t.Fatal(err)
		}
	}
	manager := providers.NewManager(dir, store)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &app.App{ProviderManager: manager}
}

func TestPlanHierarchyRendersAndSurvivesResize(t *testing.T) {
	m := newTestModel(t)
	plan := &agent.Plan{ID: "plan", Description: "Ship TUI", Steps: []agent.Step{
		{ID: "one", Description: "Build the UI", Status: agent.StepInProgress},
		{ID: "two", Description: "Run tests", Status: agent.StepPending},
	}}
	updated, _ := m.Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressPlanStep, Phase: "build", Plan: plan}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = updated.(Model)
	for _, want := range []string{"Planning", "Build", "Audit", "Completion", "Build the UI", "Run tests"} {
		if !strings.Contains(m.View(), want) {
			t.Fatalf("view missing %q:\n%s", want, m.View())
		}
	}
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 18, Height: 4})
	if got := updated.(Model).View(); got == "" {
		t.Fatal("tiny terminal produced an empty view")
	}
}

func TestWelcomeGuidesCredentialSetupAndWideStreamHasNoRail(t *testing.T) {
	m := newTestModel(t)
	view := m.View()
	for _, want := range []string{"Welcome to Supremo", "API key required", "/auth <key>"} {
		if !strings.Contains(view, want) {
			t.Fatalf("welcome missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "EXECUTION") {
		t.Fatalf("execution must remain an overlay, not a permanent rail:\n%s", view)
	}
}

func TestApprovalAndSensitiveCommandPresentation(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressApproval, Tool: "write_file", ToolStatus: "waiting approval", Arguments: `{"path":"secret.txt"}`}})
	m = updated.(Model)
	view := m.View()
	for _, want := range []string{"Approval required", "Update file?", "secret.txt", "a approve once"} {
		if !strings.Contains(view, want) {
			t.Fatalf("approval modal missing %q:\n%s", want, view)
		}
	}
	for _, raw := range []string{"write_file", "Mutating tool arguments", `{"path":"secret.txt"}`} {
		if strings.Contains(view, raw) {
			t.Fatalf("approval modal leaked %q:\n%s", raw, view)
		}
	}
	if got := displayCommand("/auth top-secret-key"); strings.Contains(got, "top-secret-key") {
		t.Fatalf("sensitive command was not redacted: %q", got)
	}
	updated, _ = newTestModel(t).Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressApproval, Tool: "run_formatter", ToolStatus: "waiting approval"}})
	if updated.(Model).approval == nil {
		t.Fatal("approval modal should also open for a tool with no arguments")
	}

	updated, _ = newTestModel(t).Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressApproval, Tool: "execute_command", ToolStatus: "waiting approval", Arguments: `{"command":"pwd"}`}})
	view = updated.(Model).View()
	for _, want := range []string{"Run shell command?", "$ pwd"} {
		if !strings.Contains(view, want) {
			t.Fatalf("command approval missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "execute_command") || strings.Contains(view, `{"command":"pwd"}`) || strings.Count(view, "approve once") != 1 {
		t.Fatalf("command approval exposed protocol details or duplicate controls:\n%s", view)
	}
}

func TestCommandPaletteFiltersCommands(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("/pla")
	m.updatePalette()
	if !m.paletteOpen || len(m.palette.Items()) == 0 {
		t.Fatal("expected a filtered command palette")
	}
}

func TestComposerAcceptsTasksAndStartsWork(t *testing.T) {
	m := newTestModel(t)
	if !m.input.Focused() {
		t.Fatal("composer should be focused when the TUI starts")
	}
	for _, r := range "inspect the current package" {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	if got := m.input.Value(); got != "inspect the current package" {
		t.Fatalf("composer value = %q", got)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.active == nil || m.active.kind != taskAgent {
		t.Fatal("enter should start an agent task")
	}
	if m.input.Value() != "" {
		t.Fatal("composer should reset after task submission")
	}
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].content != "inspect the current package" {
		t.Fatal("submitted task should appear in the transcript")
	}
}

func TestProviderReadinessControlsTaskStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	missing := New(newProviderApplication(t, ""), &agent.Session{ID: "missing"}, ctx, cancel)
	updated, _ := missing.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	missing = updated.(Model)
	missing.input.SetValue("inspect")
	updated, _ = missing.Update(tea.KeyMsg{Type: tea.KeyEnter})
	missing = updated.(Model)
	if missing.active != nil || !strings.Contains(missing.View(), "/auth <key>") {
		t.Fatalf("missing key should keep work idle and show recovery:\n%s", missing.View())
	}

	configured := New(newProviderApplication(t, "configured-key"), &agent.Session{ID: "configured"}, ctx, cancel)
	updated, _ = configured.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	configured = updated.(Model)
	configured.input.SetValue("inspect")
	updated, _ = configured.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.(Model).active == nil {
		t.Fatal("configured provider should permit a task to start")
	}
}

func TestComposerStartsSingleLineAndGrowsForAltEnter(t *testing.T) {
	m := newTestModel(t)
	if got := m.input.Height(); got != 1 {
		t.Fatalf("initial composer height = %d, want 1", got)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = updated.(Model)
	if got := m.input.Height(); got != 2 {
		t.Fatalf("composer height after Alt+Enter = %d, want 2", got)
	}
}

func TestComposerWrapsAndKeepsNewLinesVisible(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 30, Height: 24})
	m = updated.(Model)
	for _, r := range strings.Repeat("wrap ", 20) {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	if got := m.input.Height(); got != 4 {
		t.Fatalf("wrapped composer height = %d, want capped height 4", got)
	}
	for _, line := range strings.Split(m.input.View(), "\n") {
		if width := lipgloss.Width(line); width > m.width-3 {
			t.Fatalf("input line width = %d, terminal content width = %d", width, m.width-3)
		}
	}

	m = newTestModel(t)
	m.input.SetValue("first line")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = updated.(Model)
	if view := m.input.View(); !strings.Contains(view, "first line") {
		t.Fatalf("growing the composer hid the previous line:\n%s", view)
	}
	for range 5 {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
		m = updated.(Model)
	}
	if got := m.input.LineCount(); got != 7 {
		t.Fatalf("composer line count = %d, want 7", got)
	}
}

func TestClickPositionsComposerCursor(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue(strings.Join([]string{"zero", "one", "two", "three", "four", "five"}, "\n"))
	m.resizeComposer()
	top := renderedRow(t, m.View(), "two")
	left := m.styles.input.GetPaddingLeft() + lipgloss.Width(m.input.Prompt)
	updated, _ := m.Update(tea.MouseMsg{
		X:      left + 2,
		Y:      top,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	m = updated.(Model)
	if got := m.input.Line(); got != 2 {
		t.Fatalf("clicked input line = %d, want 2", got)
	}
	if got := m.input.LineInfo().ColumnOffset; got != 2 {
		t.Fatalf("clicked input column = %d, want 2", got)
	}

	m = newTestModel(t)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 20, Height: 24})
	m = updated.(Model)
	m.input.SetValue("alpha beta gamma delta")
	m.resizeComposer()
	top = renderedRow(t, m.View(), "alpha")
	left = m.styles.input.GetPaddingLeft() + lipgloss.Width(m.input.Prompt)
	updated, _ = m.Update(tea.MouseMsg{
		X:      left + 2,
		Y:      top + 1,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	m = updated.(Model)
	if info := m.input.LineInfo(); info.RowOffset != 1 || info.CharOffset != 2 {
		t.Fatalf("clicked wrapped position = row %d, column %d; want row 1, column 2", info.RowOffset, info.CharOffset)
	}
}

func TestDragSelectsCopiesAndDeletesComposerText(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("alpha\nbeta")
	m.resizeComposer()
	left := m.styles.input.GetPaddingLeft() + lipgloss.Width(m.input.Prompt)
	startY := renderedRow(t, m.View(), "alpha")
	endY := renderedRow(t, m.View(), "beta")

	updated, _ := m.Update(tea.MouseMsg{
		X:      left + 2,
		Y:      startY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{
		X:      left + 2,
		Y:      endY,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionMotion,
	})
	m = updated.(Model)
	if got := m.selectedText(); got != "pha\nbe" {
		t.Fatalf("selected composer text = %q, want %q", got, "pha\nbe")
	}
	if !strings.Contains(m.View(), "release to copy selection") ||
		!strings.Contains(m.View(), m.styles.selection.Render("pha")) {
		t.Fatalf("selection is not visibly active:\n%s", m.View())
	}

	updated, cmd := m.Update(tea.MouseMsg{
		X:      left + 2,
		Y:      endY,
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionRelease,
	})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("releasing a selection should copy it")
	}
	if !strings.Contains(m.View(), "selection copied") {
		t.Fatalf("released selection lacks copy feedback:\n%s", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)
	if got := m.input.Value(); got != "alta" {
		t.Fatalf("composer after deleting selection = %q, want %q", got, "alta")
	}
	if m.selection != nil {
		t.Fatal("deleting selected text should clear the selection")
	}
}

func TestDragSelectsChatText(t *testing.T) {
	m := newTestModel(t)
	m.appendEntry(entryAssistant, "copy this line")
	row := renderedRow(t, m.View(), "copy this line")
	line := strings.Split(m.View(), "\n")[row]
	left := strings.Index(ansi.Strip(line), "copy")

	updated, _ := m.Update(tea.MouseMsg{
		X:      left,
		Y:      row,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMsg{
		X:      left + 4,
		Y:      row,
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionMotion,
	})
	m = updated.(Model)
	if got := m.selectedText(); got != "copy" {
		t.Fatalf("selected chat text = %q, want %q", got, "copy")
	}
	if !strings.Contains(m.View(), "release to copy selection") {
		t.Fatalf("chat selection is not visibly active:\n%s", m.View())
	}
}

func TestCommandPaletteSupportsAllCommandsAndCompletion(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("/")
	m.updatePalette()
	if got := len(m.palette.Items()); got != 24 {
		t.Fatalf("command palette contains %d commands, want 24", got)
	}
	m.input.SetValue("/pla")
	m.updatePalette()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if got := m.input.Value(); got != "/plan " {
		t.Fatalf("partial command enter = %q, want /plan completion", got)
	}

	m = newTestModel(t)
	m.input.SetValue("/plan")
	m.updatePalette()
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.active == nil || m.active.kind != taskCommand {
		t.Fatal("exact command enter should start command execution")
	}
}

func TestPlanResumeStartsAnInteractiveTask(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("/plan resume")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.active == nil || m.active.kind != taskAgent {
		t.Fatal("plan resume should be owned by the interactive agent task")
	}
}

func TestApprovalModeAppearsAboveComposer(t *testing.T) {
	m := newTestModel(t)
	m.session.ApprovalMode = "batman"
	if !strings.Contains(m.View(), "ASK RISKY · reads run automatically · risky actions ask") {
		t.Fatalf("missing batman safety label:\n%s", m.View())
	}
	m.session.ApprovalMode = "superman"
	if !strings.Contains(m.View(), "AUTO-APPROVE · tools run without confirmation") {
		t.Fatalf("missing superman safety label:\n%s", m.View())
	}
	m.session.ApprovalMode = "strict"
	if !strings.Contains(m.View(), "ASK ALWAYS · every tool requires approval") {
		t.Fatalf("missing strict safety label:\n%s", m.View())
	}
}

func TestToolStreamUsesConciseActionLabels(t *testing.T) {
	m := newTestModel(t)
	m.recordToolEvent(agent.ProgressEvent{
		Kind:       agent.ProgressTool,
		Tool:       "execute_command",
		ToolStatus: "running",
		Arguments:  `{"command":"pwd"}`,
	})
	view := m.View()
	if !strings.Contains(view, "Checking workspace…") {
		t.Fatalf("missing concise action label:\n%s", view)
	}
	if strings.Contains(view, "execute_command") || strings.Contains(view, `{"command":"pwd"}`) {
		t.Fatalf("raw tool protocol leaked into stream:\n%s", view)
	}
	m.recordToolEvent(agent.ProgressEvent{Kind: agent.ProgressTool, Tool: "execute_command", ToolStatus: "completed"})
	if strings.Count(m.View(), "Checking workspace…") != 1 {
		t.Fatalf("completed event should not add a duplicate stream row:\n%s", m.View())
	}
}

func TestToolOutputStaysCollapsedUntilClicked(t *testing.T) {
	m := newTestModel(t)
	m.recordToolEvent(agent.ProgressEvent{
		Kind:       agent.ProgressTool,
		Tool:       "execute_command",
		ToolStatus: "running",
		Arguments:  `execute_command {"command":"pwd"}`,
	})
	m.recordToolEvent(agent.ProgressEvent{
		Kind:       agent.ProgressTool,
		Tool:       "execute_command",
		ToolStatus: "completed",
		Arguments:  `execute_command {"command":"pwd"}`,
		ToolOutput: "{\n  \"stdout\": \"workspace-output\"\n}",
	})
	if view := m.View(); !strings.Contains(view, "Tool · Checking workspace…") || strings.Contains(view, "workspace-output") {
		t.Fatalf("tool output should be collapsed by default:\n%s", view)
	}
	updated, _ := m.Update(tea.MouseMsg{
		X:      0,
		Y:      renderedRow(t, m.View(), "Tool · Checking workspace…"),
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	updated, _ = updated.(Model).Update(tea.MouseMsg{
		X:      0,
		Y:      renderedRow(t, updated.(Model).View(), "Tool · Checking workspace…"),
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionRelease,
	})
	m = updated.(Model)
	if view := m.View(); !strings.Contains(view, "workspace-output") {
		t.Fatalf("clicking a tool row should reveal its output:\n%s", view)
	}
	updated, _ = m.Update(tea.MouseMsg{
		X:      0,
		Y:      renderedRow(t, m.View(), "Tool · Checking workspace…"),
		Button: tea.MouseButtonLeft,
		Action: tea.MouseActionPress,
	})
	updated, _ = updated.(Model).Update(tea.MouseMsg{
		X:      0,
		Y:      renderedRow(t, updated.(Model).View(), "Tool · Checking workspace…"),
		Button: tea.MouseButtonNone,
		Action: tea.MouseActionRelease,
	})
	if strings.Contains(updated.(Model).View(), "workspace-output") {
		t.Fatal("clicking an open tool row should collapse its output")
	}
}

func TestShortTranscriptStaysAboveComposer(t *testing.T) {
	m := newTestModel(t)
	m.appendEntry(entryUser, "terminal-line")
	if row := renderedRow(t, m.bodyView(), "terminal-line"); row != m.feed.Height-1 {
		t.Fatalf("short transcript ended on row %d, want %d:\n%s", row, m.feed.Height-1, m.bodyView())
	}
}

func TestAssistantEntriesHaveASupremoLabel(t *testing.T) {
	m := newTestModel(t)
	m.appendEntry(entryAssistant, "Hello from Supremo.")
	if view := m.View(); !strings.Contains(view, "● Supremo") || !strings.Contains(view, "Hello from Supremo.") {
		t.Fatalf("assistant response should be distinguishable from user input:\n%s", view)
	}
}

func TestTranscriptScrollsWithoutLeavingTheComposer(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 40; i++ {
		m.appendEntry(entryAssistant, "A previous response that gives the transcript another line.")
	}
	if !m.feed.AtBottom() {
		t.Fatal("new transcript entries should follow the latest response")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.feed.YOffset == 0 || !m.input.Focused() {
		t.Fatalf("up should scroll chat while leaving composer ready: offset=%d focused=%t", m.feed.YOffset, m.input.Focused())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !updated.(Model).feed.AtBottom() {
		t.Fatal("end should return to the newest chat")
	}
}

func TestActiveStatusUsesHeroActionsAndLiveSpinner(t *testing.T) {
	m := newTestModel(t)
	m.active = &activeTask{id: 1, cancel: func() {}, kind: taskAgent}
	tick := m.applyProgress(agent.ProgressEvent{Kind: agent.ProgressIteration, Iteration: 2})
	if m.liveEntry != len(m.entries)-1 {
		t.Fatalf("active status = %#v, live entry = %d", m.entries, m.liveEntry)
	}
	firstAction := m.entries[m.liveEntry].content
	firstSpinner := m.spinner.Spinner.Frames[0]
	if !strings.Contains(m.View(), m.spinner.View()) {
		t.Fatalf("live status should render the spinner:\n%s", m.View())
	}
	before := m.View()
	updated, _ := m.Update(tick())
	m = updated.(Model)
	if got := m.View(); got == before {
		t.Fatalf("live spinner did not redraw:\n%s", got)
	}
	updated, _ = m.Update(heroStatusMsg{taskID: 1})
	m = updated.(Model)
	if m.entries[m.liveEntry].content == firstAction {
		t.Fatalf("hero action should not repeat: %q", firstAction)
	}
	if m.spinner.Spinner.Frames[0] == firstSpinner {
		t.Fatal("hero action should rotate to a different spinner style")
	}
}

func TestApprovalRestoresComposerFocusAndNoColorDisablesPulse(t *testing.T) {
	m := newTestModel(t)
	m.input.Blur()
	updated, _ := m.Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressApproval, Tool: "write_file", ToolStatus: "waiting approval", Arguments: `{"path":"main.go"}`}})
	m = updated.(Model)
	if !m.input.Focused() || m.approval == nil {
		t.Fatal("approval should focus the composer and open the approval panel")
	}
	m.input.Blur()
	updated, _ = m.Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressApproval, Tool: "write_file", ToolStatus: "approved"}})
	m = updated.(Model)
	if m.approval != nil || !m.input.Focused() {
		t.Fatal("resolved approval should restore composer focus")
	}

	t.Setenv("NO_COLOR", "1")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if m := New(nil, &agent.Session{ID: "no-color"}, ctx, cancel); m.pulseEnabled {
		t.Fatal("NO_COLOR should disable phase animation")
	}
}

func TestApprovalAllowsCancellationAndTaskCompletionClearsModal(t *testing.T) {
	m := newTestModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	m.active = &activeTask{id: 7, cancel: cancel, kind: taskAgent}
	updated, _ := m.Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressApproval, Tool: "write_file", ToolStatus: "waiting approval"}})
	m = updated.(Model)
	for _, r := range "/cancel" {
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("approval /cancel did not cancel the active task")
	}
	if m.approval != nil {
		t.Fatal("approval /cancel should close the modal")
	}

	m.approval = &approvalState{tool: "write_file"}
	updated, _ = m.Update(taskResultMsg{id: 7, session: m.session, err: context.DeadlineExceeded})
	if updated.(Model).approval != nil {
		t.Fatal("a completed task must clear an approval modal")
	}
}

func TestActiveTaskRejectsToolsOutsideTheAllowList(t *testing.T) {
	m := newTestModel(t)
	m.active = &activeTask{id: 8, cancel: func() {}, kind: taskAgent}
	m.input.SetValue("/tools")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].content, "A task is running") {
		t.Fatalf("active task should reject /tools: %#v", m.entries)
	}
}

func TestLiveSpinnerDisappearsAfterWorkAndCompactExecutionOverlay(t *testing.T) {
	m := newTestModel(t)
	m.input.SetValue("inspect the workspace")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	m.applyProgress(agent.ProgressEvent{Kind: agent.ProgressIteration})
	liveAction := m.entries[m.liveEntry].content
	if !m.heroStatus || !strings.Contains(m.View(), liveAction) {
		t.Fatal("active task should display a live hero action")
	}
	updated, _ = m.Update(taskResultMsg{id: m.active.id, session: m.session, response: "done"})
	m = updated.(Model)
	if m.active != nil || m.heroStatus || strings.Contains(m.View(), liveAction) || strings.Contains(m.View(), "working") {
		t.Fatal("completed task should clear transient activity labels")
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 32})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = updated.(Model)
	if !strings.Contains(m.View(), "EXECUTION") {
		t.Fatal("ctrl+p should open the compact execution overlay")
	}
}

func TestStreamReplacesTransientHeroStatus(t *testing.T) {
	m := newTestModel(t)
	m.active = &activeTask{id: 1, cancel: func() {}, kind: taskAgent}
	m.applyProgress(agent.ProgressEvent{Kind: agent.ProgressIteration})
	m.applyProgress(agent.ProgressEvent{Kind: agent.ProgressStream, Message: "A streamed answer."})
	if strings.Contains(m.View(), "Lasering…") || !strings.Contains(m.View(), "A streamed answer.") {
		t.Fatalf("stream should replace the transient hero row:\n%s", m.View())
	}
}

func TestShortcutOverlayAndProviderErrorRecovery(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	m = updated.(Model)
	if !m.showHelp || !strings.Contains(m.View(), "SHORTCUTS") {
		t.Fatalf("shortcut overlay did not open:\n%s", m.View())
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.showHelp {
		t.Fatal("escape should close shortcut overlay")
	}
	if got := m.recoveryError(errors.New("gemini execution error: API key not valid: INVALID_ARGUMENT")); !strings.Contains(got, "/auth <key>") {
		t.Fatalf("provider recovery should direct the user to /auth: %q", got)
	}
}

func TestStreamEntryFinalizesAndBridgePreservesBoundedEvents(t *testing.T) {
	m := newTestModel(t)
	updated, _ := m.Update(agentProgressMsg{event: agent.ProgressEvent{Kind: agent.ProgressStream, Message: "Fin"}})
	m = updated.(Model)
	if !strings.Contains(m.View(), "Fin") {
		t.Fatalf("stream text did not render:\n%s", m.View())
	}
	m.active = &activeTask{id: 9, cancel: func() {}, kind: taskAgent}
	updated, _ = m.Update(taskResultMsg{id: 9, session: m.session, response: "Finished"})
	m = updated.(Model)
	if m.streamingEntry != -1 || !strings.Contains(m.View(), "Finished") {
		t.Fatalf("stream did not finalize into the response:\n%s", m.View())
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bridge := newEventBridge(ctx, 1)
	bridge.publish(agent.ProgressEvent{Message: "first"})
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		close(started)
		bridge.publish(agent.ProgressEvent{Message: "second"})
		close(done)
	}()
	<-started
	select {
	case <-done:
		t.Fatal("producer should wait for bounded queue capacity")
	default:
	}
	first := bridge.wait()().(agentProgressMsg)
	if first.event.Message != "first" {
		t.Fatalf("first event = %#v", first.event)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("producer did not resume after a dequeue")
	}
	second := bridge.wait()().(agentProgressMsg)
	if second.event.Message != "second" {
		t.Fatalf("second event = %#v", second.event)
	}
}
