package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/agent"
)

func (m Model) View() string {
	if m.width < 2 || m.height < 2 {
		return "Supremo"
	}
	parts := []string{m.headerView(), m.bodyView()}
	if m.paletteOpen && m.approval == nil && !m.showSidebar && !m.showHelp {
		parts = append(parts, m.styles.palette.Width(max(18, min(m.width-4, 72))).Render(m.palette.View()))
	}
	parts = append(parts, m.inputView(), m.footerView())
	view := strings.Join(parts, "\n")
	if m.selection.active() {
		return highlightSelection(view, m.selection, m.styles.selection)
	}
	return view
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
	workspaceView := m.styles.muted.Render(workspace)
	if m.workspaceInfo.err != "" {
		workspaceView = m.styles.warning.Render(workspace)
	}
	parts = append(parts, m.styles.muted.Render(model), workspaceView, credential)
	if m.session.PlanMode {
		parts = append(parts, m.styles.accent.Render("plan"))
	}
	if m.session.DryRun {
		parts = append(parts, m.styles.warning.Render("dry-run"))
	}
	if m.debug {
		parts = append(parts, m.styles.muted.Render("debug"))
	}
	return m.styles.header.Width(max(1, m.width-2)).Render(strings.Join(parts, "  "))
}

func (m Model) bodyView() string {
	height := max(1, m.feed.Height)
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
	content := []string{
		m.styles.title.Render("Welcome to Supremo"),
		"",
	}
	if !m.credentialReady {
		content = append(content, m.styles.error.Render("! API key required — run /auth <key>"), "")
	}
	content = append(content,
		m.styles.text.Render("Describe a task, or start with:"),
		m.styles.command.Render("/plan")+m.styles.muted.Render(" plan work")+"   "+m.styles.command.Render("/tools")+m.styles.muted.Render(" inspect tools")+"   "+m.styles.command.Render("/help")+m.styles.muted.Render(" all commands"),
	)
	return m.styles.welcome.Width(max(20, min(m.width-6, 76))).Render(strings.Join(content, "\n"))
}

func (m Model) inputView() string {
	divider := m.styles.divider.Width(max(1, m.width-2)).Render("")
	mode := m.approvalModeView()
	if m.approval != nil {
		return strings.Join([]string{divider, mode}, "\n")
	}
	if m.focusFeed {
		return strings.Join([]string{divider, mode, m.styles.muted.Render("Transcript focused. Press Esc or Ctrl+N to return to the prompt.")}, "\n")
	}
	button := m.sendButtonView()
	if gap := m.width - 2 - lipgloss.Width(mode) - lipgloss.Width(button); gap > 0 {
		mode += strings.Repeat(" ", gap) + button
	}
	return strings.Join([]string{divider, mode, m.styles.input.Width(max(1, m.width-2)).Render(m.composerView())}, "\n")
}

func (m Model) composerView() string {
	view := m.input.View()
	rows := composerRows(m.input)
	if rows <= m.input.Height() {
		return view
	}
	lines := strings.Split(view, "\n")
	height := min(m.input.Height(), len(lines))
	thumbHeight := max(1, height*height/rows)
	thumbStart := 0
	if maxOffset := rows - height; maxOffset > 0 {
		thumbStart = (height - thumbHeight) * m.inputOffset / maxOffset
	}
	for row := range height {
		marker := m.styles.muted.Render("│")
		if row >= thumbStart && row < thumbStart+thumbHeight {
			marker = m.styles.accent.Render("┃")
		}
		lines[row] += marker
	}
	return strings.Join(lines, "\n")
}

func (m Model) sendButtonView() string {
	if strings.TrimSpace(m.input.Value()) == "" {
		return m.styles.muted.Render("[ send — ]")
	}
	return m.styles.accent.Render("[ send ↵ ]")
}

func (m Model) approvalModeView() string {
	switch m.session.ApprovalMode {
	case "batman":
		return m.styles.warning.Render("ASK RISKY · reads run automatically · risky actions ask")
	case "superman":
		return m.styles.error.Render("AUTO-APPROVE · tools run without confirmation")
	default:
		return m.styles.success.Render("ASK ALWAYS · every tool requires approval")
	}
}

func (m Model) footerView() string {
	if m.approval != nil {
		if m.approval.deciding {
			return m.styles.footer.Render(m.spinner.View() + " submitting approval…")
		}
		return ""
	}
	if m.showHelp || m.showSidebar {
		return m.styles.footer.Render("esc return to prompt")
	}
	if m.selection.active() {
		hint := "selecting"
		if !m.selection.dragging {
			hint = "ctrl+c copy"
		}
		if m.selection.copied {
			hint = "copied"
		}
		if m.selection.input {
			hint += "  ·  backspace/delete remove"
		}
		return m.styles.footer.Render(hint + "  ·  esc clear")
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
		return m.styles.footer.Render(toolHint + "↑↓ request history  ·  PgUp/PgDn chat  ·  ? shortcuts")
	}
	return m.styles.footer.Render(toolHint + "PgUp/PgDn page  ·  End latest chat  ·  ? shortcuts")
}

func highlightSelection(view string, selection *textSelection, style lipgloss.Style) string {
	startX, startY, endX, endY := orderedSelection(selection)
	lines := strings.Split(view, "\n")
	startClipped, endClipped := false, false
	if selection.input {
		startClipped = startY < selection.inputTop
		endClipped = endY > selection.inputBottom
		startY = max(startY, selection.inputTop)
		endY = min(endY, selection.inputBottom)
	}
	startY = max(0, startY)
	endY = min(endY, len(lines)-1)
	for row := startY; row <= endY; row++ {
		width := lipgloss.Width(lines[row])
		content := strings.TrimRight(ansi.Strip(lines[row]), " ")
		contentWidth := lipgloss.Width(content)
		if selection.input && (strings.HasSuffix(content, "│") || strings.HasSuffix(content, "┃")) {
			contentWidth--
		}
		left, right := 0, contentWidth
		if row == startY && !startClipped {
			left = min(max(0, startX), width)
		} else if selection.input {
			left = min(selection.inputLeft, width)
		}
		if row == endY && !endClipped {
			right = min(max(0, endX), width)
		}
		if right <= left {
			continue
		}
		lines[row] = ansi.Cut(lines[row], 0, left) +
			style.Render(ansi.Strip(ansi.Cut(lines[row], left, right))) +
			ansi.Cut(lines[row], right, width)
	}
	return strings.Join(lines, "\n")
}

func orderedSelection(selection *textSelection) (startX, startY, endX, endY int) {
	startX, startY = selection.startX, selection.startY
	endX, endY = selection.endX, selection.endY
	if endY < startY || endY == startY && endX < startX {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	return
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
				out.WriteString("  ")
				out.WriteString(planStepMark(step.Status))
				out.WriteString(" ")
				out.WriteString(truncate(step.Description, 48))
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
		out.WriteString("\n")
		out.WriteString(m.styles.muted.Render("  No tool activity"))
	}
	for _, event := range m.activity {
		label := conciseToolLabel(event.Tool, event.Status, event.Arguments)
		out.WriteString("\n  ")
		out.WriteString(toolMark(event.Status))
		out.WriteString(" ")
		out.WriteString(truncate(label, 48))
	}
	return m.styles.overlay.Width(max(24, min(m.width-8, 64))).Render(strings.TrimRight(out.String(), "\n"))
}

func (m Model) helpView() string {
	content := strings.Join([]string{
		m.styles.title.Render("SHORTCUTS"),
		"",
		"Enter  send task or command",
		"Shift+Enter / Alt+Enter / Ctrl+J  add a line",
		"/  open commands",
		"↑↓  browse request history",
		"PgUp/PgDn  page through chat · End latest",
		"Ctrl+P  execution",
		"Ctrl+T  show or hide thoughts",
		"Ctrl+L  focus transcript",
		"Ctrl+C  exit Supremo",
		"Esc  return to the prompt",
	}, "\n")
	return m.styles.overlay.Width(max(24, min(m.width-8, 58))).Render(content)
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
	title, details := approvalPrompt(m.approval)
	content := strings.Join([]string{
		m.styles.warning.Render("? " + state),
		"",
		m.styles.title.Render(title),
		m.styles.tool.Render(details),
		"",
		m.styles.accent.Render("a approve once") + m.styles.muted.Render("  ·  d or esc deny"),
	}, "\n")
	return m.styles.modal.Width(max(20, min(m.width-10, 64))).Render(content)
}

func approvalPrompt(approval *approvalState) (title, details string) {
	path := toolArgument(approval.arguments, "path")
	switch approval.tool {
	case "execute_command":
		command := append([]string{toolArgument(approval.arguments, "command")}, toolArguments(approval.arguments, "args")...)
		details = "$ " + strings.TrimSpace(strings.Join(command, " "))
		if directory := toolArgument(approval.arguments, "directory"); directory != "" {
			details += "\nWorking directory: " + directory
		}
		return "Run shell command?", details
	case "write_file":
		title = "Update file?"
	case "create_file":
		title = "Create file?"
	case "delete_file":
		title = "Delete file?"
	case "create_directory":
		title = "Create directory?"
	case "rename_file":
		return "Rename file?", toolArgument(approval.arguments, "old_path") + " → " + toolArgument(approval.arguments, "new_path")
	case "run_formatter":
		return "Format files?", "The formatter may update workspace files."
	default:
		return "Allow " + strings.ReplaceAll(approval.tool, "_", " ") + "?", safeText(approval.arguments)
	}
	if path == "" {
		path = safeText(approval.arguments)
	}
	return title, path
}
