package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"
)

// workspaceSummary formats git branch and uncommitted change metrics.
func workspaceSummary(branch string, changed int, ready bool, err string) string {
	if ready {
		state := "clean"
		if changed > 0 {
			state = fmt.Sprintf("%d changed", changed)
		}
		if branch == "" {
			return state
		}
		return fmt.Sprintf("%s · %s", branch, state)
	}
	if err != "" {
		return "not a git workspace"
	}
	return "workspace checking"
}

// tokenSummary formats used and available context token counts.
func tokenSummary(inputTokens, outputTokens, contextLimit int) string {
	used := inputTokens + outputTokens
	limitStr := "?"
	if contextLimit > 0 {
		limitStr = fmt.Sprintf("%dk", contextLimit/1000)
	}
	return fmt.Sprintf("%dk/%s", used/1000, limitStr)
}

// approvalModeDescription returns the user-facing explanation for the current approval mode.
func approvalModeDescription(mode, planStatus string) string {
	plan := ""
	if planStatus != "" {
		plan = "  ·  " + planStatus
	}
	switch strings.ToLower(mode) {
	case "batman":
		return "ASK RISKY · reads run automatically · risky actions ask" + plan
	case "superman":
		return "AUTO-APPROVE · tools run without confirmation" + plan
	default:
		return "ASK CHANGES · reads run internally · changes and commands ask" + plan
	}
}

// HeaderView renders the top status bar adapting gracefully across terminal widths.
func (m Model) HeaderView() string {
	workspace := workspaceSummary(m.workspaceInfo.branch, m.workspaceInfo.changed, m.workspaceInfo.ready, m.workspaceInfo.err)
	model := m.provider
	if m.modelName != "" {
		model += " · " + m.modelName
	}
	session := m.session.Name
	if session == "" {
		session = m.session.ID
	}
	runState := "○ idle"
	if m.active != nil {
		runState = "● " + m.phase
	}
	mode := "ask changes"
	switch strings.ToLower(string(m.session.ApprovalMode)) {
	case "batman":
		mode = "ask risky"
	case "superman":
		mode = "auto approve"
	}

	parts := []string{
		m.styles.Title.Render("SUPREMO"),
		m.styles.Text.Render(ansi.Truncate(session, 18, "…")),
		m.styles.Muted.Render(m.glyph("◇", "-") + " " + model),
		m.styles.Muted.Render(m.glyph("◈", "#") + " " + mode),
		m.styles.Status.Render(runState),
	}

	// Responsive progressive disclosure:
	// >= 60 cols: show git workspace
	if m.width >= 60 && (m.workspaceInfo.changed > 0 || m.workspaceInfo.err != "") {
		parts = append(parts, m.styles.Muted.Render(workspace))
	}
	if m.width >= 80 && !m.credentialReady {
		parts = append(parts, m.styles.Error.Render(m.glyph("×", "!")+" key needed"))
	}
	// >= 100 cols: show token metrics
	if m.width >= 100 {
		used := m.inputTokens + m.outputTokens
		percent := 0.0
		if m.contextLimit > 0 {
			percent = float64(used) / float64(m.contextLimit)
		}
		parts = append(parts, m.tokenBar.ViewAs(percent)+" "+m.styles.Muted.Render(tokenSummary(m.inputTokens, m.outputTokens, m.contextLimit)))
	}
	if badge := m.PhaseBadge(); badge != "" {
		parts = append(parts, badge)
	}
	if m.session.DryRun {
		parts = append(parts, m.styles.Warning.Render("dry-run"))
	}
	if m.debug {
		parts = append(parts, m.styles.Muted.Render("debug"))
	}
	line := ansi.Truncate(strings.Join(parts, "  "), max(1, m.width-4), "…")
	return m.styles.Header.Width(max(1, m.width-2)).Render(line)
}

// PhaseBadge returns a semantic, styled badge for the current planning/execution phase.
func (m Model) PhaseBadge() string {
	if m.surface == surfacePlanQuestion {
		return m.styles.PlanQuestion.Render("plan: decision")
	}
	if m.planDraft || m.session.PlanModeActive() {
		return m.styles.PlanActive.Render("plan mode")
	}
	if m.surface == surfaceApproval {
		return m.styles.ApprovalDanger.Render("approval")
	}
	return ""
}

// FooterView renders the contextual bottom keyboard hint bar.
func (m Model) FooterView() string {
	if m.surface == surfaceProvider || m.surface == surfaceModel {
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Selector.ShortHelp()))
	}
	if m.surface == surfaceCredential {
		return m.styles.Footer.Render("Enter continue  ·  Esc cancel")
	}
	if m.diffOpen() {
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Viewer.ShortHelp()))
	}
	if m.surface == surfaceApproval && m.approval != nil {
		if m.approval.IsDeciding() {
			return m.styles.Footer.Render(m.spinner.View() + " submitting approval…")
		}
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Approval.ShortHelp()))
	}
	if m.surface == surfaceHelp {
		return m.styles.Footer.Render("Esc return to prompt · Ctrl+P plans")
	}
	if m.surface >= surfaceSessions && m.surface <= surfaceKrypton {
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Overlay.ShortHelp()))
	}
	if m.focus == focusActivity {
		return m.styles.Footer.Render("Esc return to chat · Ctrl+B hide activity")
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
		return m.styles.Footer.Render(hint + "  ·  esc clear")
	}
	if m.surface == surfacePlanQuestion && m.planQuestion != nil {
		if m.planQuestion.CustomAnswer() {
			return m.styles.Footer.Render("Enter submit · Esc cancel")
		}
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.PlanQuestion.ShortHelp()))
	}
	if m.active != nil {
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Streaming.ShortHelp()))
	}
	if m.planDraft {
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.PlanDraft.ShortHelp()))
	}
	if m.mentionOpen || m.paletteOpen {
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Selector.ShortHelp()))
	}
	if m.transcriptFocused() {
		return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Feed.ShortHelp()))
	}
	if !m.followTail && m.newOutput > 0 {
		pill := zone.Mark("unread-pill", m.styles.Warning.Render(fmt.Sprintf("%d new updates · click or End to follow", m.newOutput)))
		return m.styles.Footer.Render(pill)
	}
	return m.styles.Footer.Render(m.help.ShortHelpView(m.keys.Composer.ShortHelp()))
}

// ApprovalModeView renders the authorization status indicator line.
func (m Model) ApprovalModeView() string {
	modeStr := string(m.session.ApprovalMode)
	desc := approvalModeDescription(modeStr, m.planModeStatus())
	switch strings.ToLower(modeStr) {
	case "batman":
		return m.styles.Muted.Render(desc)
	case "superman":
		return m.styles.ApprovalDanger.Render(desc)
	default:
		return m.styles.Success.Render(desc)
	}
}
