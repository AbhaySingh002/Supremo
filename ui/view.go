package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/AbhaySingh002/supremo/internal/agent"
)

func (m Model) View() string {
	if m.width < 2 || m.height < 2 {
		return "Supremo"
	}
	parts := []string{m.headerView(), m.bodyView()}
	if m.paletteOpen && m.approval == nil && !m.showSidebar && !m.showHelp {
		parts = append(parts, m.styles.palette.Width(maxInt(18, minInt(m.width-4, 72))).Render(m.palette.View()))
	}
	parts = append(parts, m.inputView(), m.footerView())
	return strings.Join(parts, "\n")
}

func (m Model) headerView() string {
	workspace := "workspace checking"
	if m.workspaceInfo.ready {
		state := "clean"
		if m.workspaceInfo.changed > 0 {
			state = fmt.Sprintf("%d changed", m.workspaceInfo.changed)
		}
		workspace = fmt.Sprintf("%s · %s", m.workspaceInfo.branch, state)
	} else if m.workspaceInfo.err != "" {
		workspace = "not a git workspace"
	}
	model := m.provider
	if m.modelName != "" {
		model += " · " + m.modelName
	}
	credential := m.styles.success.Render("● key ready")
	if !m.credentialReady {
		credential = m.styles.error.Render("! key needed")
	}
	parts := []string{m.styles.title.Render("SUPREMO")}
	parts = append(parts, m.styles.muted.Render(model), m.styles.muted.Render(workspace), credential)
	if m.session.PlanMode {
		parts = append(parts, m.styles.accent.Render("plan"))
	}
	if m.session.DryRun {
		parts = append(parts, m.styles.warning.Render("dry-run"))
	}
	if m.debug {
		parts = append(parts, m.styles.muted.Render("debug"))
	}
	return m.styles.header.Width(maxInt(1, m.width-2)).Render(strings.Join(parts, "  "))
}

func (m Model) bodyView() string {
	height := maxInt(1, m.feed.Height)
	if m.approval != nil {
		return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, m.approvalView())
	}
	if m.showHelp {
		return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, m.helpView())
	}
	if m.showSidebar {
		return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, m.executionView())
	}
	if m.welcomeVisible() {
		return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, m.welcomeView())
	}
	return m.feed.View()
}

func (m Model) welcomeVisible() bool {
	if m.active != nil {
		return false
	}
	for _, entry := range m.entries {
		switch entry.kind {
		case entryUser, entryCommand, entryTool, entryAssistant, entryStreaming, entryError:
			return false
		}
	}
	return true
}

func (m Model) welcomeView() string {
	model := m.provider
	if m.modelName != "" {
		model += " · " + m.modelName
	}
	credential := m.styles.success.Render("● API key ready")
	if !m.credentialReady {
		credential = m.styles.error.Render("! API key required — run /auth <key>")
	}
	content := strings.Join([]string{
		m.styles.title.Render("Welcome to Supremo"),
		m.styles.muted.Render(model),
		m.styles.muted.Render(m.workspace),
		"",
		credential,
		"",
		m.styles.text.Render("Describe a task below, or use a command:"),
		m.styles.command.Render("/plan") + m.styles.muted.Render(" plan work") + "   " + m.styles.command.Render("/tools") + m.styles.muted.Render(" inspect tools") + "   " + m.styles.command.Render("/help") + m.styles.muted.Render(" all commands"),
	}, "\n")
	return m.styles.welcome.Width(maxInt(20, minInt(m.width-6, 76))).Render(content)
}

func (m Model) inputView() string {
	divider := m.styles.divider.Width(maxInt(1, m.width-2)).Render("")
	mode := m.approvalModeView()
	if m.approval != nil {
		return strings.Join([]string{divider, mode, m.styles.muted.Render("Approval controls are active above.")}, "\n")
	}
	if m.focusFeed {
		return strings.Join([]string{divider, mode, m.styles.muted.Render("Transcript focused. Press Esc or Ctrl+N to return to the prompt.")}, "\n")
	}
	return strings.Join([]string{divider, mode, m.styles.input.Width(maxInt(1, m.width-2)).Render(m.input.View())}, "\n")
}

func (m Model) approvalModeView() string {
	switch m.session.ApprovalMode {
	case "batman":
		return m.styles.warning.Render("BATMAN · normal work runs · risky changes ask")
	case "superman":
		return m.styles.success.Render("SUPERMAN · every tool runs automatically")
	default:
		return m.styles.error.Render("STRICT · every tool asks first")
	}
}

func (m Model) footerView() string {
	if m.approval != nil {
		if m.approval.deciding {
			return m.styles.footer.Render(m.spinner.View() + " submitting approval…")
		}
		return m.styles.footer.Render("a approve  d/esc deny  /deny <reason>")
	}
	if m.showHelp || m.showSidebar {
		return m.styles.footer.Render("esc return to prompt")
	}
	if m.active != nil {
		return m.styles.footer.Render("/cancel to stop  ·  ? shortcuts")
	}
	if m.paletteOpen {
		return m.styles.footer.Render("↑↓ select  tab complete  enter complete/run  esc close")
	}
	toolHint := ""
	for _, entry := range m.entries {
		if entry.kind == entryTool && entry.details != "" {
			toolHint = "click tool row for output  ·  "
			break
		}
	}
	if m.feed.AtBottom() {
		return m.styles.footer.Render(toolHint + "↑↓ chat  ·  PgUp/PgDn scroll  ·  ? shortcuts")
	}
	return m.styles.footer.Render(toolHint + "↑↓ scroll  ·  End latest chat  ·  ? shortcuts")
}

func (m Model) executionView() string {
	var out strings.Builder
	out.WriteString(m.styles.title.Render("EXECUTION"))
	out.WriteString("\n")
	for _, phase := range []string{"planning", "build", "audit", "completion"} {
		out.WriteString(m.phaseLine(phase))
		out.WriteString("\n")
		if phase == "build" && m.plan != nil {
			for _, step := range m.plan.Steps {
				out.WriteString("  " + planStepMark(step.Status) + " " + truncate(step.Description, 48))
				out.WriteString("\n")
			}
		}
	}
	if m.plan == nil {
		out.WriteString(m.styles.muted.Render("  No active plan"))
		out.WriteString("\n")
	}
	out.WriteString("\n")
	out.WriteString(m.styles.title.Render("RECENT TOOLS"))
	if len(m.activity) == 0 {
		out.WriteString("\n" + m.styles.muted.Render("  No tool activity"))
	}
	for _, event := range m.activity {
		label := conciseToolLabel(event.Tool, event.Status, event.Arguments)
		out.WriteString("\n  " + toolMark(event.Status) + " " + truncate(label, 48))
	}
	return m.styles.overlay.Width(maxInt(24, minInt(m.width-8, 64))).Render(strings.TrimRight(out.String(), "\n"))
}

func (m Model) helpView() string {
	content := strings.Join([]string{
		m.styles.title.Render("SHORTCUTS"),
		"",
		"Enter  send task or command",
		"Alt+Enter  add a line",
		"/  open commands",
		"↑↓  browse chat when prompt is empty",
		"PgUp/PgDn  page through chat · End latest",
		"Ctrl+P  execution",
		"Ctrl+T  show or hide thoughts",
		"Ctrl+L  focus transcript",
		"Ctrl+C  exit Supremo",
		"Esc  return to the prompt",
	}, "\n")
	return m.styles.overlay.Width(maxInt(24, minInt(m.width-8, 58))).Render(content)
}

func (m Model) phaseLine(phase string) string {
	label := strings.ToUpper(phase[:1]) + phase[1:]
	if m.phase == phase {
		if m.pulse > 0.25 {
			return m.styles.accent.Render("› " + label)
		}
		return m.styles.warning.Render("› " + label)
	}
	if phaseOrder(phase) < phaseOrder(m.phase) {
		return m.styles.success.Render("✓ " + label)
	}
	return m.styles.muted.Render("· " + label)
}

func phaseOrder(phase string) int {
	switch phase {
	case "planning":
		return 1
	case "build":
		return 2
	case "audit":
		return 3
	case "completion":
		return 4
	default:
		return 0
	}
}

func planStepMark(status string) string {
	switch status {
	case agent.StepCompleted:
		return "✓"
	case agent.StepInProgress:
		return "›"
	case agent.StepFailed:
		return "!"
	default:
		return "·"
	}
}

func toolMark(status string) string {
	switch status {
	case "completed", "approved", "dry run":
		return "✓"
	case "failed", "denied":
		return "!"
	case "waiting approval":
		return "?"
	default:
		return "·"
	}
}

func (m Model) approvalView() string {
	state := "Approval required"
	if m.approval.deciding {
		state = "Submitting approval"
	}
	content := strings.Join([]string{
		m.styles.error.Render("! " + state),
		"",
		m.styles.title.Render(m.approval.tool),
		m.styles.warning.Render("Mutating tool arguments:"),
		m.styles.tool.Render(m.approval.arguments),
		"",
		m.styles.muted.Render("a approve · d or esc deny · /deny <reason> then enter"),
	}, "\n")
	return m.styles.modal.Width(maxInt(20, minInt(m.width-10, 76))).Render(content)
}
