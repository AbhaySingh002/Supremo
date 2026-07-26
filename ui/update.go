package ui

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
	handled bool
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
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
		if m.approval != nil || m.showHelp || m.showSidebar || m.paletteOpen {
			return m, nil
		}
		if m.toggleToolAtMouse(msg) {
			return m, nil
		}
		var cmd tea.Cmd
		m.feed, cmd = m.feed.Update(msg)
		return m, cmd
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
	if transcriptNavigation(msg, m.input.Value()) {
		return m.scrollTranscript(msg)
	}
	if m.paletteOpen && (msg.Type == tea.KeyUp || msg.Type == tea.KeyDown) {
		var cmd tea.Cmd
		m.palette, cmd = m.palette.Update(msg)
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
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.resizeComposer()
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
	case tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd:
		return strings.TrimSpace(input) == ""
	default:
		return false
	}
}

func (m Model) completeSelectedCommand() (tea.Model, tea.Cmd) {
	if item, ok := m.palette.SelectedItem().(commandItem); ok {
		m.input.SetValue(item.command.Name + " ")
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
	line := msg.Y - lipgloss.Height(m.headerView()) - 1 + m.feed.YOffset
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
