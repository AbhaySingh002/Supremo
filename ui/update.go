package ui

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/commands"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type agentProgressMsg struct{ event agent.ProgressEvent }
type taskResultMsg struct {
	id       int
	session  agent.Session
	response string
	err      error
}
type commandResultMsg struct {
	id      int
	input   string
	session agent.Session
	output  string
	plan    *agent.Plan
	err     error
}
type approvalResultMsg struct {
	resolved bool
	err      error
}
type planLoadedMsg struct {
	plan *agent.Plan
	err  error
}
type workspaceStatusMsg struct {
	info workspaceState
	err  error
}
type markdownRenderedMsg struct {
	run      int
	rendered map[int]string
}
type pulseMsg struct{}
type heroStatusMsg struct{ taskID int }
type selectionCopiedMsg struct{ err error }
type clipboardPasteMsg struct {
	text string
	err  error
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.selection = nil
		m.layout()
		return m, m.renderMarkdown()
	case spinner.TickMsg:
		if m.active == nil {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.rebuildFeed()
		return m, cmd
	case heroStatusMsg:
		if m.active == nil || m.active.id != msg.taskID {
			return m, nil
		}
		if m.heroStatus && m.liveEntry >= 0 && m.liveEntry < len(m.entries) && m.entries[m.liveEntry].kind == entryStatus {
			m.heroAction = randomHeroAction(m.heroAction)
			return m, tea.Batch(m.setHeroStatus(), heroStatusCmd(msg.taskID))
		}
		return m, heroStatusCmd(msg.taskID)
	case tea.MouseMsg:
		if m.sendButtonHit(msg) {
			if strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			return m.submitInput()
		}
		if handled, cmd := m.updateMouseSelection(msg); handled {
			return m, cmd
		}
		if handled, cmd := m.positionComposerCursor(msg); handled {
			return m, cmd
		}
		if m.approval != nil || m.showHelp || m.showSidebar || m.paletteOpen {
			return m, nil
		}
		if m.toggleToolAtMouse(msg) {
			return m, nil
		}
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		return m, cmd
	case selectionCopiedMsg:
		if msg.err != nil {
			m.appendEntry(entryError, "Copy failed: "+msg.err.Error())
		} else if m.selection != nil {
			m.selection.copied = true
		}
		return m, nil
	case clipboardPasteMsg:
		if msg.err != nil {
			m.appendEntry(entryError, "Paste failed: "+msg.err.Error())
			return m, nil
		}
		return m, tea.Batch(m.insertPastedText(msg.text), m.updatePalette())
	case pulseMsg:
		if !m.pulseEnabled {
			return m, nil
		}
		m.pulseTicks++
		target := 1.0
		if m.pulseTicks > 5 {
			target = 0
		}
		m.pulse, m.pulseVelocity = m.spring.Update(m.pulse, m.pulseVelocity, target)
		if m.pulseTicks >= 12 && math.Abs(m.pulse) < 0.03 {
			return m, nil
		}
		return m, pulseCmd()
	case agentProgressMsg:
		cmd := m.applyProgress(msg.event)
		return m, tea.Batch(m.bridge.wait(), cmd)
	case taskResultMsg:
		if m.active == nil || m.active.id != msg.id {
			return m, nil
		}
		m.clearHeroStatus()
		m.active = nil
		m.approval = nil
		m.session = msg.session
		m.refreshProvider()
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) {
				m.finishStreaming(entryStatus, "")
				m.appendEntry(entryStatus, "Task canceled.")
			} else {
				m.finishStreaming(entryStatus, "")
				m.appendEntry(entryError, m.recoveryError(msg.err))
			}
		} else if msg.response != "" {
			if !m.finishStreaming(entryAssistant, msg.response) {
				m.appendEntry(entryAssistant, msg.response)
			}
		}
		return m, tea.Batch(m.renderMarkdown(), workspaceStatusCmd(m.ctx, m.workspace), loadPlanCmd(m.session, m.workspace))
	case commandResultMsg:
		if msg.id != 0 && (m.active == nil || m.active.id != msg.id) {
			return m, nil
		}
		if msg.id != 0 {
			m.active = nil
			m.liveEntry = -1
			m.heroStatus = false
		}
		if errors.Is(msg.err, commands.ErrExit) {
			if m.shutdown != nil {
				m.shutdown()
			}
			return m, tea.Quit
		}
		m.session = msg.session
		m.refreshProvider()
		if msg.session.CurrentPlanID == "" {
			m.plan = nil
		} else if msg.plan != nil {
			m.plan = msg.plan
		}
		if commandIs(msg.input, "/clear") {
			m.entries = nil
		}
		if msg.err != nil {
			m.appendEntry(entryError, m.recoveryError(msg.err))
		} else if msg.output != "" {
			m.setStatus(conciseCommandOutput(msg.input, msg.output))
		}
		return m, tea.Batch(workspaceStatusCmd(m.ctx, m.workspace), loadPlanCmd(m.session, m.workspace))
	case approvalResultMsg:
		if msg.err != nil {
			m.appendEntry(entryError, msg.err.Error())
			return m, nil
		}
		if !msg.resolved {
			m.appendEntry(entryStatus, "No tool call is awaiting approval.")
			m.approval = nil
			return m, m.input.Focus()
		}
		if m.approval != nil {
			m.approval.deciding = true
		}
		return m, nil
	case planLoadedMsg:
		if msg.err != nil && m.session.CurrentPlanID != "" {
			m.appendEntry(entryError, "load active plan: "+msg.err.Error())
		} else if msg.plan != nil {
			m.plan = msg.plan
		}
		return m, nil
	case workspaceStatusMsg:
		if msg.err != nil {
			m.workspaceInfo = workspaceState{err: msg.err.Error()}
		} else {
			m.workspaceInfo = msg.info
		}
		return m, nil
	case markdownRenderedMsg:
		if msg.run != m.markdownRun {
			return m, nil
		}
		for index, rendered := range msg.rendered {
			if index >= 0 && index < len(m.entries) {
				m.entries[index].rendered = rendered
			}
		}
		m.rebuildFeed()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}

	if m.focusFeed {
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeComposer()
	paletteCmd := m.updatePalette()
	return m, tea.Batch(cmd, paletteCmd)
}

func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.selection != nil {
		if msg.Type == tea.KeyEsc {
			m.selection = nil
			return m, nil
		}
		if m.selection.active() && msg.Type == tea.KeyCtrlC {
			return m, copySelectionCmd(m.selectedText())
		}
		if m.selection.active() && m.selection.input {
			switch {
			case selectionDeleteKey(msg, m.input):
				m.deleteInputSelection()
				return m, m.updatePalette()
			case len(msg.Runes) > 0 || key.Matches(msg, m.input.KeyMap.Paste, m.input.KeyMap.InsertNewline):
				m.deleteInputSelection()
			default:
				m.selection = nil
			}
		} else {
			m.selection = nil
		}
	}
	if msg.Type == tea.KeyCtrlC {
		if m.active != nil {
			m.active.cancel()
		}
		if m.shutdown != nil {
			m.shutdown()
		}
		return m, tea.Quit
	}
	if m.approval != nil {
		return m.updateApprovalKey(msg)
	}
	if m.showHelp || m.showSidebar {
		if msg.Type == tea.KeyEsc || (m.showHelp && msg.String() == "?") || (m.showSidebar && key.Matches(msg, m.keys.togglePanel)) {
			m.showHelp, m.showSidebar = false, false
			return m, m.input.Focus()
		}
		return m, nil
	}
	if msg.String() == "?" && strings.TrimSpace(m.input.Value()) == "" {
		m.showHelp = true
		return m, nil
	}
	if key.Matches(msg, m.keys.togglePanel) {
		m.showSidebar = !m.showSidebar
		return m, nil
	}
	if key.Matches(msg, m.keys.toggleDebug) {
		m.showDebug = !m.showDebug
		m.rebuildFeed()
		return m, nil
	}
	if key.Matches(msg, m.keys.focusTranscript) {
		m.focusFeed = true
		m.input.Blur()
		return m, nil
	}
	if key.Matches(msg, m.keys.focusInput) {
		m.focusFeed = false
		return m, m.input.Focus()
	}
	if m.focusFeed {
		if msg.Type == tea.KeyEsc {
			m.focusFeed = false
			return m, m.input.Focus()
		}
		if msg.Type == tea.KeyEnter || msg.Type == tea.KeySpace {
			if m.toggleLatestTool() {
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		return m, cmd
	}
	if msg.Paste {
		return m, tea.Batch(m.insertPastedText(string(msg.Runes)), m.updatePalette())
	}
	if key.Matches(msg, m.input.KeyMap.Paste) {
		return m, clipboardPasteCmd()
	}
	if transcriptNavigation(msg, m.input.Value()) {
		return m.scrollTranscript(msg)
	}
	if m.paletteOpen && (msg.Type == tea.KeyUp || msg.Type == tea.KeyDown) {
		var cmd tea.Cmd
		m.palette, cmd = m.palette.Update(msg)
		return m, cmd
	}
	if handled, cmd := m.navigateInputHistory(msg); handled {
		return m, cmd
	}
	if m.paletteOpen && key.Matches(msg, m.keys.complete) {
		return m.completeSelectedCommand()
	}
	if m.paletteOpen && msg.Type == tea.KeyEnter && !m.exactCommandInput() {
		return m.completeSelectedCommand()
	}
	if key.Matches(msg, m.keys.submit) {
		return m.submitInput()
	}
	if msg.Type == tea.KeyEsc && m.paletteOpen {
		m.paletteOpen = false
		m.layout()
		return m, nil
	}
	value := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeComposer()
	if m.input.Value() != value {
		m.historyIndex = len(m.inputHistory)
		m.historyDraft = ""
	}
	return m, tea.Batch(cmd, m.updatePalette())
}

func (m Model) scrollTranscript(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		switch keyMsg.Type {
		case tea.KeyHome:
			m.feed.GotoTop()
			return m, nil
		case tea.KeyEnd:
			m.feed.GotoBottom()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.feed, cmd = m.feed.Update(msg)
	return m, cmd
}

func transcriptNavigation(msg tea.KeyMsg, input string) bool {
	switch msg.Type {
	case tea.KeyPgUp, tea.KeyPgDown:
		return true
	case tea.KeyHome, tea.KeyEnd:
		return strings.TrimSpace(input) == ""
	default:
		return false
	}
}

func (m *Model) navigateInputHistory(msg tea.KeyMsg) (bool, tea.Cmd) {
	if len(m.inputHistory) == 0 || msg.Type != tea.KeyUp && msg.Type != tea.KeyDown {
		return false, nil
	}
	navigating := m.historyIndex < len(m.inputHistory)
	switch msg.Type {
	case tea.KeyUp:
		info := m.input.LineInfo()
		if !navigating && (m.input.Line() != 0 || info.RowOffset != 0) {
			return false, nil
		}
		if m.historyIndex == len(m.inputHistory) {
			m.historyDraft = m.input.Value()
		}
		if m.historyIndex == 0 {
			return true, nil
		}
		m.historyIndex--
	case tea.KeyDown:
		if !navigating {
			return false, nil
		}
		m.historyIndex++
	}
	value := m.historyDraft
	if m.historyIndex < len(m.inputHistory) {
		value = m.inputHistory[m.historyIndex]
	}
	m.input.SetValue(value)
	m.paletteOpen = false
	m.resizeComposer()
	return true, m.input.Focus()
}

func (m Model) completeSelectedCommand() (tea.Model, tea.Cmd) {
	if item, ok := m.palette.SelectedItem().(commandItem); ok {
		m.input.SetValue(item.command.Name + " ")
		m.historyIndex = len(m.inputHistory)
		m.historyDraft = ""
		m.resizeComposer()
		return m, tea.Batch(m.input.Focus(), m.updatePalette())
	}
	return m, nil
}

func (m Model) exactCommandInput() bool {
	input := strings.TrimSpace(m.input.Value())
	for _, command := range m.registry.List() {
		if input == command.Name {
			return true
		}
	}
	return false
}

func (m Model) updateApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.approval.deciding {
		return m, nil
	}
	if msg.Type == tea.KeyEsc || (msg.String() == "d" && strings.TrimSpace(m.input.Value()) == "") {
		m.approval.deciding = true
		return m, approvalCmd(m.application, true, "denied in Supremo TUI")
	}
	if msg.String() == "a" && strings.TrimSpace(m.input.Value()) == "" {
		m.approval.deciding = true
		return m, approvalCmd(m.application, false, "")
	}
	if msg.Type == tea.KeyEnter {
		input := strings.TrimSpace(m.input.Value())
		if input == "" {
			m.approval.deciding = true
			return m, approvalCmd(m.application, false, "")
		}
		if commandIs(input, "/deny") {
			m.approval.deciding = true
			m.resetComposer()
			return m, approvalCmd(m.application, true, strings.TrimSpace(strings.TrimPrefix(input, "/deny")))
		}
		if commandIs(input, "/cancel") {
			if m.active != nil {
				m.active.cancel()
			}
			m.approval = nil
			m.resetComposer()
			m.appendEntry(entryStatus, "Cancellation requested.")
			return m, nil
		}
		if commandIs(input, "/exit") {
			if m.active != nil {
				m.active.cancel()
			}
			if m.shutdown != nil {
				m.shutdown()
			}
			return m, tea.Quit
		}
		m.appendEntry(entryStatus, "Type /deny <reason>, /cancel, press a to approve, or d to deny.")
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeComposer()
	return m, cmd
}

func (m Model) submitInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input.Value())
	if input == "" {
		return m, nil
	}
	m.rememberInput(input)
	if m.active != nil {
		switch {
		case commandIs(input, "/cancel"):
			m.active.cancel()
			m.resetComposer()
			m.appendEntry(entryStatus, "Cancellation requested.")
			return m, nil
		case commandIs(input, "/exit"):
			m.active.cancel()
			if m.shutdown != nil {
				m.shutdown()
			}
			return m, tea.Quit
		case commandIs(input, "/approve"):
			m.resetComposer()
			return m, approvalCmd(m.application, false, "")
		case commandIs(input, "/deny"):
			m.resetComposer()
			return m, approvalCmd(m.application, true, strings.TrimSpace(strings.TrimPrefix(input, "/deny")))
		case commandIs(input, "/help") || commandIs(input, "/activity"):
			m.resetComposer()
			m.appendEntry(entryCommand, displayCommand(input))
			return m, runCommandCmd(m.ctx, m.registry, m.application, m.session, m.workspace, input, 0)
		default:
			m.appendEntry(entryStatus, "A task is running; use /approve, /deny, /cancel, or /exit.")
			return m, nil
		}
	}
	if planResume(input) {
		return m, m.startTask("", true)
	}
	if strings.HasPrefix(input, "/") {
		return m, m.startCommand(input)
	}
	return m, m.startTask(input, false)
}

func (m *Model) rememberInput(input string) {
	if input == "" || commandIs(input, "/auth") {
		return
	}
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != input {
		m.inputHistory = append(m.inputHistory, input)
	}
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
}

func (m *Model) updatePalette() tea.Cmd {
	value := strings.TrimSpace(strings.ToLower(m.input.Value()))
	if !strings.HasPrefix(value, "/") {
		if m.paletteOpen {
			m.paletteOpen = false
			m.layout()
		}
		return nil
	}
	items := make([]list.Item, 0)
	for _, command := range m.registry.List() {
		item := commandItem{command: command}
		if strings.Contains(strings.ToLower(item.FilterValue()), value) {
			items = append(items, item)
		}
	}
	m.paletteOpen = len(items) > 0
	cmd := m.palette.SetItems(items)
	m.layout()
	return cmd
}

func (m *Model) applyProgress(event agent.ProgressEvent) tea.Cmd {
	switch event.Kind {
	case agent.ProgressStream:
		m.clearHeroStatus()
		m.updateStreaming(event.Message)
	case agent.ProgressDebug:
		m.appendEntry(entryDebug, event.Message)
	case agent.ProgressPlan, agent.ProgressPlanStep:
		if event.Plan != nil {
			m.plan = event.Plan
		}
		if event.Phase != "" {
			m.phase = event.Phase
			return m.startPulse()
		}
	case agent.ProgressPhase:
		m.phase = event.Phase
		m.setStatus(phaseLabel(event.Phase))
		return m.startPulse()
	case agent.ProgressCompletion:
		m.phase = "completion"
		m.setStatus("Complete")
		return m.startPulse()
	case agent.ProgressApproval:
		if event.ToolStatus == "waiting approval" {
			m.approval = &approvalState{tool: event.Tool, arguments: truncate(event.Arguments, 4_000)}
			m.resetComposer()
			return m.input.Focus()
		}
		m.recordToolEvent(event)
		if event.ToolStatus == "approved" || event.ToolStatus == "denied" {
			m.approval = nil
			return m.input.Focus()
		}
	case agent.ProgressTool:
		m.recordToolEvent(event)
	case agent.ProgressIteration:
		return m.setHeroStatus()
	}
	return nil
}

func (m Model) recoveryError(err error) string {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "api key not valid") || strings.Contains(message, "invalid_argument") && strings.Contains(message, "gemini") {
		return "Gemini rejected the configured API key. Run /auth <key> to replace it."
	}
	return err.Error()
}

func (m *Model) recordToolEvent(event agent.ProgressEvent) {
	toolEvent := tools.Event{Tool: event.Tool, Status: event.ToolStatus, Message: event.Message, Arguments: event.Arguments, Output: event.ToolOutput}
	m.activity = append(m.activity, toolEvent)
	if len(m.activity) > 8 {
		m.activity = m.activity[len(m.activity)-8:]
	}
	label := conciseToolLabel(event.Tool, event.ToolStatus, event.Arguments)
	switch event.ToolStatus {
	case "running":
		m.clearHeroStatus()
		m.entries = append(m.entries, transcriptEntry{kind: entryTool, content: label, details: toolDetails(event)})
		if m.active != nil {
			m.liveEntry = len(m.entries) - 1
		}
		m.rebuildFeed()
	case "completed":
		for index := len(m.entries) - 1; index >= 0; index-- {
			if m.entries[index].kind != entryTool {
				continue
			}
			if event.Arguments != "" {
				m.entries[index].content = label
			}
			details := toolDetails(event)
			if event.Arguments == "" && m.entries[index].details != "" {
				if details != "" {
					details = m.entries[index].details + "\n" + details
				} else {
					details = m.entries[index].details
				}
			}
			m.entries[index].details = details
			if m.liveEntry == index {
				m.liveEntry = -1
			}
			m.rebuildFeed()
			return
		}
		m.entries = append(m.entries, transcriptEntry{kind: entryTool, content: label, details: toolDetails(event)})
		m.rebuildFeed()
	case "dry run":
		m.entries = append(m.entries, transcriptEntry{kind: entryTool, content: "Dry run — " + label, details: toolDetails(event)})
		m.rebuildFeed()
	case "failed", "denied":
		if event.Message != "" {
			label += " — " + truncate(event.Message, 240)
		}
		m.appendEntry(entryError, label)
	}
}

func toolDetails(event agent.ProgressEvent) string {
	var details []string
	if arguments := strings.TrimSpace(event.Arguments); arguments != "" {
		details = append(details, "input:\n"+arguments)
	}
	if output := strings.TrimSpace(event.ToolOutput); output != "" {
		details = append(details, "output:\n"+output)
	} else if message := strings.TrimSpace(event.Message); message != "" {
		details = append(details, "result: "+message)
	}
	return truncate(safeText(strings.Join(details, "\n")), 12_000)
}

func (m *Model) toggleLatestTool() bool {
	for index := len(m.entries) - 1; index >= 0; index-- {
		if m.entries[index].kind == entryTool && m.entries[index].details != "" {
			m.entries[index].expanded = !m.entries[index].expanded
			m.rebuildFeed()
			return true
		}
	}
	return false
}

func (m *Model) toggleToolAtMouse(msg tea.MouseMsg) bool {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return false
	}
	line := msg.Y - lipgloss.Height(m.headerView()) + m.feed.YOffset - m.feedPadding
	if line < 0 {
		return false
	}
	for index, entry := range m.entries {
		if entry.kind == entryDebug && !m.showDebug {
			continue
		}
		height := strings.Count(m.renderEntry(index, entry), "\n") + 1
		if line >= 0 && line < height && entry.kind == entryTool && entry.details != "" {
			m.entries[index].expanded = !m.entries[index].expanded
			m.rebuildFeed()
			return true
		}
		line -= height + 2
	}
	return false
}

func (m *Model) updateMouseSelection(msg tea.MouseMsg) (bool, tea.Cmd) {
	if m.approval != nil || m.showHelp || m.showSidebar || m.focusFeed {
		return false, nil
	}
	switch {
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		m.selection = &textSelection{
			startX:   msg.X,
			startY:   msg.Y,
			endX:     msg.X,
			endY:     msg.Y,
			dragging: true,
		}
		handled, cmd := m.positionComposerCursor(msg)
		if handled {
			m.selection.input = true
			m.selection.inputLeft = m.styles.input.GetPaddingLeft() + lipgloss.Width(m.input.Prompt)
			m.selection.inputTop = m.composerTop()
			m.selection.inputBottom = m.selection.inputTop + m.input.Height() - 1
			m.selection.anchor = composerCursorOffset(m.input)
			m.selection.head = m.selection.anchor
		}
		return true, cmd
	case msg.Action == tea.MouseActionMotion && m.selection != nil && m.selection.dragging:
		if m.selection.input {
			target, handled, cmd := m.positionComposerDragCursor(msg)
			if handled {
				m.selection.endX, m.selection.endY = target.X, target.Y
				m.selection.head = composerCursorOffset(m.input)
				return true, cmd
			}
			return true, nil
		}
		m.selection.endX, m.selection.endY = msg.X, msg.Y
		return true, nil
	case msg.Action == tea.MouseActionRelease && m.selection != nil && m.selection.dragging:
		selection := m.selection
		selection.dragging = false
		if selection.input {
			drag := msg
			drag.Button = tea.MouseButtonLeft
			drag.Action = tea.MouseActionMotion
			target, handled, cmd := m.positionComposerDragCursor(drag)
			if handled {
				selection.endX, selection.endY = target.X, target.Y
				selection.head = composerCursorOffset(m.input)
			}
			if selection.active() {
				return true, cmd
			}
		} else {
			selection.endX, selection.endY = msg.X, msg.Y
			if selection.active() {
				text := m.selectedText()
				if text != "" {
					return true, nil
				}
				m.selection = nil
				return true, nil
			}
		}
		click := tea.MouseMsg{X: selection.startX, Y: selection.startY, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
		m.selection = nil
		m.toggleToolAtMouse(click)
		return true, nil
	}
	return false, nil
}

func (m Model) sendButtonHit(msg tea.MouseMsg) bool {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress ||
		m.approval != nil || m.showHelp || m.showSidebar || m.focusFeed {
		return false
	}
	for row, line := range strings.Split(m.View(), "\n") {
		plain := ansi.Strip(line)
		for _, label := range []string{"[ send — ]", "[ send ↵ ]"} {
			if index := strings.Index(plain, label); row == msg.Y && index >= 0 {
				left := lipgloss.Width(plain[:index])
				return msg.X >= left && msg.X < left+lipgloss.Width(label)
			}
		}
	}
	return false
}

func (m Model) composerTop() int {
	return lipgloss.Height(m.View()) - lipgloss.Height(m.footerView()) - m.input.Height()
}

func (m *Model) positionComposerDragCursor(msg tea.MouseMsg) (tea.MouseMsg, bool, tea.Cmd) {
	top := m.composerTop()
	bottom := top + m.input.Height() - 1
	rows, _ := composerMetrics(m.input)
	oldOffset := m.inputOffset
	if msg.Y < top {
		m.inputOffset = max(0, m.inputOffset-1)
		msg.Y = top
	} else if msg.Y > bottom {
		m.inputOffset = min(max(0, rows-m.input.Height()), m.inputOffset+1)
		msg.Y = bottom
	}
	msg.X = min(max(0, msg.X), m.width-1)
	handled, cmd := m.positionComposerCursor(msg)
	if handled && m.selection != nil {
		m.selection.startY -= m.inputOffset - oldOffset
	}
	return msg, handled, cmd
}

func (m *Model) positionComposerCursor(msg tea.MouseMsg) (bool, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress && msg.Action != tea.MouseActionMotion ||
		m.approval != nil || m.showHelp || m.showSidebar || m.focusFeed {
		return false, nil
	}
	top := m.composerTop()
	row := msg.Y - top
	if msg.X < 0 || msg.X >= m.width || row < 0 || row >= m.input.Height() {
		return false, nil
	}

	lineNumber, info := composerTarget(m.input, m.inputOffset+row)
	moveComposerCursor(&m.input, lineNumber)
	lines := strings.Split(m.input.Value(), "\n")
	line := []rune(lines[m.input.Line()])
	start := min(info.StartColumn, len(line))
	end := min(start+info.Width, len(line))
	x := max(0, msg.X-m.styles.input.GetPaddingLeft()-lipgloss.Width(m.input.Prompt))
	column := start
	for column < end && lipgloss.Width(string(line[start:column+1])) <= x {
		column++
	}
	m.input.SetCursor(column)
	focus := m.input.Focus()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(nil)
	rows, cursorRow := composerMetrics(m.input)
	m.syncComposerOffset(rows, cursorRow)
	return true, tea.Batch(focus, cmd)
}

func composerCursorOffset(input textarea.Model) int {
	offset := input.LineInfo().StartColumn + input.LineInfo().ColumnOffset
	for _, line := range strings.Split(input.Value(), "\n")[:input.Line()] {
		offset += len([]rune(line)) + 1
	}
	return offset
}

func (m Model) selectedText() string {
	if !m.selection.active() {
		return ""
	}
	if m.selection.input {
		value := []rune(m.input.Value())
		start, end := min(m.selection.anchor, m.selection.head), max(m.selection.anchor, m.selection.head)
		return string(value[start:end])
	}
	startX, startY, endX, endY := orderedSelection(m.selection)
	lines := strings.Split(m.View(), "\n")
	startY, endY = max(0, startY), min(endY, len(lines)-1)
	if startY > endY {
		return ""
	}
	selected := make([]string, 0, endY-startY+1)
	for row := startY; row <= endY; row++ {
		line := lines[row]
		left, right := 0, lipgloss.Width(line)
		if row == startY {
			left = min(max(0, startX), right)
		}
		if row == endY {
			right = min(max(0, endX), right)
		}
		selected = append(selected, strings.TrimRight(ansiText(line, left, right), " "))
	}
	return strings.TrimRight(strings.Join(selected, "\n"), "\n")
}

func ansiText(line string, left, right int) string {
	return ansi.Strip(ansi.Cut(line, left, right))
}

func copySelectionCmd(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	return func() tea.Msg {
		return selectionCopiedMsg{err: clipboard.WriteAll(text)}
	}
}

func clipboardPasteCmd() tea.Cmd {
	return func() tea.Msg {
		text, err := clipboard.ReadAll()
		return clipboardPasteMsg{text: text, err: err}
	}
}

func (m *Model) insertPastedText(text string) tea.Cmd {
	text = strings.ToValidUTF8(ansi.Strip(text), "")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	m.input.InsertString(text)
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(nil)
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
	m.resizeComposer()
	return cmd
}

func selectionDeleteKey(msg tea.KeyMsg, input textarea.Model) bool {
	return key.Matches(msg,
		input.KeyMap.DeleteAfterCursor,
		input.KeyMap.DeleteBeforeCursor,
		input.KeyMap.DeleteCharacterBackward,
		input.KeyMap.DeleteCharacterForward,
		input.KeyMap.DeleteWordBackward,
		input.KeyMap.DeleteWordForward,
	)
}

func (m *Model) deleteInputSelection() {
	value := []rune(m.input.Value())
	start, end := min(m.selection.anchor, m.selection.head), max(m.selection.anchor, m.selection.head)
	m.input.SetValue(string(append(value[:start], value[end:]...)))
	moveComposerCursor(&m.input, 0)
	offset := start
	for line, text := range strings.Split(m.input.Value(), "\n") {
		width := len([]rune(text))
		if offset <= width {
			moveComposerCursor(&m.input, line)
			m.input.SetCursor(offset)
			break
		}
		offset -= width + 1
	}
	m.selection = nil
	m.historyIndex = len(m.inputHistory)
	m.historyDraft = ""
	m.resizeComposer()
}

func composerTarget(input textarea.Model, row int) (int, textarea.LineInfo) {
	probe := textarea.New()
	probe.Prompt = ""
	probe.ShowLineNumbers = false
	probe.SetWidth(input.Width())
	lines := strings.Split(input.Value(), "\n")
	for lineNumber, line := range lines {
		probe.SetValue(line)
		height := probe.LineInfo().Height
		if row >= height {
			row -= height
			continue
		}
		for column := range len([]rune(line)) + 1 {
			probe.SetCursor(column)
			if info := probe.LineInfo(); info.RowOffset == row {
				return lineNumber, info
			}
		}
	}
	probe.SetValue(lines[len(lines)-1])
	return len(lines) - 1, probe.LineInfo()
}

func phaseLabel(phase string) string {
	switch phase {
	case "planning":
		return "Planning…"
	case "build":
		return "Building…"
	case "audit":
		return "Checking work…"
	case "completion":
		return "Complete"
	default:
		return "Working…"
	}
}

func conciseCommandOutput(input, output string) string {
	switch {
	case commandIs(input, "/help"):
		return "Command palette ready — type /."
	case commandIs(input, "/models"):
		if start := strings.Index(output, "Cached models ("); start >= 0 {
			count := strings.TrimSuffix(strings.Split(strings.TrimPrefix(output[start:], "Cached models ("), ")")[0], ":")
			return "Models ready — " + count + " available."
		}
		return "Models ready."
	case commandIs(input, "/tools"):
		return "Tool catalog ready — approvals follow the selected mode."
	case commandIs(input, "/activity"):
		return "Recent activity is in Ctrl+P."
	case commandIs(input, "/doctor"):
		return "Setup check complete."
	case commandIs(input, "/config"):
		return "Configuration ready."
	case commandIs(input, "/usage"):
		return "Usage updated."
	case commandIs(input, "/plan") && strings.Count(output, "\n") > 0:
		return "Plan details are in Ctrl+P."
	default:
		return output
	}
}
