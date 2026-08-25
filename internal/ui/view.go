package ui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/composer"
)

func (m Model) View() tea.View {
	if m.width < 44 || m.height < 14 {
		content := m.styles.Title.Render("SUPREMO") + "\n\n" +
			m.styles.Warning.Render("Terminal too small") + "\n" +
			m.styles.Muted.Render("Resize to at least 44×14. Your session is safe.")
		rendered := lipgloss.Place(max(1, m.width), max(1, m.height), lipgloss.Center, lipgloss.Center, content)
		v := tea.NewView(rendered)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		v.ReportFocus = true
		v.KeyboardEnhancements.ReportAlternateKeys = true
		v.BackgroundColor = m.styles.Background
		v.ForegroundColor = m.styles.Foreground
		return v
	}
	parts := []string{m.bodyView()}
	if m.paletteOpen && m.surface == surfaceNone {
		parts = append(parts, m.styles.Palette.Width(max(18, min(m.width-4, 72))).Render(m.palette.View().Content))
	}
	if m.mentionOpen && m.surface == surfaceNone {
		parts = append(parts, m.styles.Palette.Width(max(18, min(m.width-4, 72))).Render(m.mentionMenu.View()))
	}
	if input := m.inputView(); input != "" {
		parts = append(parts, input)
	}
	main := strings.Join(parts, "\n")
	if railWidth := m.activityRailWidth(); railWidth > 0 {
		main = m.joinActivity(main, m.activityView(railWidth, max(1, lipgloss.Height(main))))
	}
	view := strings.Join([]string{m.HeaderView(), main, m.FooterView()}, "\n")
	rendered := view
	if m.selection.active() {
		rendered = highlightSelection(view, m.selection, m.styles.Selection)
	}
	if !m.styles.Ascii {
		rendered = lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, rendered,
			lipgloss.WithWhitespaceChars(" "),
			lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Background(m.styles.Background)),
		)
	}
	rendered = zone.Scan(rendered)
	if m.styles.Ascii {
		rendered = ansi.Strip(rendered)
	}
	v := tea.NewView(rendered)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ReportFocus = true
	v.KeyboardEnhancements.ReportAlternateKeys = true
	v.BackgroundColor = m.styles.Background
	v.ForegroundColor = m.styles.Foreground
	v.Cursor = m.nativeComposerCursor()
	return v
}

func (m Model) bodyView() string {
	height := max(1, m.feed.Height())
	width := m.contentWidth()
	if m.surface == surfaceProvider {
		content := m.styles.Muted.Render(m.spinner.View() + " switching provider…")
		if m.providerSelector != nil {
			content = m.providerSelector.View().Content
		}
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
	}
	if m.surface == surfaceModel {
		content := m.styles.Muted.Render(m.spinner.View() + " refreshing configured providers…")
		if m.modelSelector != nil {
			content = m.modelSelector.View().Content
			if m.catalogNote != "" {
				content += "\n" + m.styles.Warning.Render("! "+m.catalogNote)
			}
		}
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
	}
	if m.surface == surfaceCredential && m.credential != nil {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.credential.View(width, height, m.spinner.View()))
	}
	if m.surface >= surfaceSessions && m.surface <= surfaceKrypton {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.overlayView())
	}
	if m.surface == surfacePlanQuestion && m.planQuestion != nil {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.planQuestion.View(width, height))
	}
	if m.surface == surfaceApproval && m.approval != nil {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.approval.View(width, height))
	}
	if m.diffOpen() {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.diffInspectorView())
	}
	if m.surface == surfaceHelp {
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.helpView())
	}
	if m.activityInspectorOpen() {
		activityWidth := width
		if m.width >= 80 {
			activityWidth = min(72, max(30, width-8))
			return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.activityView(activityWidth, max(8, height-2)))
		}
		return m.activityView(activityWidth, height)
	}
	if len(m.entries) == 0 && m.active == nil {
		welcome := m.welcomeView()
		if lipgloss.Height(welcome) > height {
			provider := m.provider
			if m.modelName != "" {
				provider += " · " + m.modelName
			}
			welcome = strings.Join([]string{
				m.styles.Title.Render("SUPREMO"),
				m.styles.Muted.Render(ansi.Truncate(provider, max(1, width-2), "…")),
				m.styles.Text.Render("Agentic coding in your local workspace"),
				m.styles.Muted.Render("Enter send · / commands · ? help"),
			}, "\n")
		}
		return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, welcome)
	}
	feed := m.feed.View()
	if m.showDebug {
		debugView := m.debugView()
		if debugView != "" {
			feed += "\n" + debugView
		}
	}
	return feed
}

func (m Model) welcomeView() string {
	art := strings.Join([]string{
		"░█▀▀░█░█░█▀█░█▀▄░█▀▀░█▄█░█▀█",
		"░▀▀█░█░█░█▀▀░█▀▄░█▀▀░█░█░█░█",
		"░▀▀▀░▀▀▀░▀░░░▀░▀░▀▀▀░▀░▀░▀▀▀",
	}, "\n")
	providerInfo := m.provider
	if m.modelName != "" {
		providerInfo += " · " + m.modelName
	}
	lines := []string{
		m.styles.Accent.Render(art),
		m.styles.Title.Render("SUPREMO"),
		"",
		m.styles.Muted.Render(providerInfo),
		"",
		m.styles.Text.Render("Agentic coding in your local workspace"),
		m.styles.Muted.Render("Enter send  ·  Ctrl+P plan  ·  / commands  ·  ? help"),
	}
	return strings.Join(lines, "\n")
}

func (m Model) debugView() string {
	return m.styles.Debug.Render("Session: " + m.session.ID + " · Mode: " + string(m.session.ApprovalMode))
}

func (m Model) inputView() string {
	if m.surface != surfaceNone {
		return ""
	}
	prompt := ""
	if m.planDraft {
		prompt = m.styles.Accent.Render("plan") + " "
	}
	var leftSide string
	if m.selection.active() && m.selection.input {
		raw := m.composerView()
		leftSide = highlightSelection(raw, m.selection, m.styles.Selection)
	} else if strings.TrimSpace(m.input.Value()) == "" && m.input.Placeholder != "" && !m.input.Focused() {
		leftSide = m.styles.Muted.Render(m.input.Placeholder)
	} else {
		leftSide = m.composerView()
	}

	mode := m.ApprovalModeView()
	statusLine := mode
	if m.active != nil {
		statusLine = m.spinner.View() + " " + m.phase
	} else if layout := m.composerLayout(); len(layout.rows) > layout.visibleRows {
		scrollHint := fmt.Sprintf(" · lines %d–%d of %d", layout.scrollRow+1, min(len(layout.rows), layout.scrollRow+layout.visibleRows), len(layout.rows))
		statusLine += m.styles.Muted.Render(scrollHint)
	} else {
		statusLine += m.styles.Muted.Render(" · Ctrl+J newline")
	}
	style := m.styles.ComposerBorder
	if m.focus == focusComposer && m.input.Focused() {
		style = m.styles.ComposerFocused
	}
	statusLine = ansi.Truncate(prompt+statusLine, max(1, m.contentWidth()-style.GetHorizontalFrameSize()), "…")
	btnWidth := 6
	leftWidth := max(10, m.contentWidth()-btnWidth-4)
	left := m.styles.ComposerBase.Width(leftWidth).Render(leftSide)
	button := m.sendButtonView()

	inputRow := lipgloss.JoinHorizontal(lipgloss.Bottom, left, " ", button)
	composerContent := strings.Join([]string{
		m.styles.Status.Render(statusLine),
		zone.Mark("composer-input", inputRow),
	}, "\n")
	return style.Width(max(1, m.contentWidth())).Render(composerContent)
}

func (m Model) mentionComposerView() string {
	return m.composerView()
}

func (m Model) composerView() string {
	layout := m.composerLayout()
	if len(layout.rows) == 0 {
		return m.input.View()
	}

	var out strings.Builder
	for index, row := range layout.rows {
		if index < layout.scrollRow || index >= layout.scrollRow+layout.visibleRows {
			continue
		}
		if len(layout.projection.Runes) == 0 && m.input.Placeholder != "" {
			out.WriteString(m.styles.Muted.Render(m.input.Placeholder))
		} else {
			out.WriteString(m.renderComposerRow(layout.projection, row.Start, row.End))
		}
		out.WriteString("\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func (m Model) nativeComposerCursor() *tea.Cursor {
	if m.surface != surfaceNone || m.focus != focusComposer || !m.input.Focused() || (m.selection != nil && m.selection.input && m.selection.active()) {
		return nil
	}
	layout := m.composerLayout()
	if len(layout.rows) == 0 {
		return nil
	}
	display := min(max(0, layout.cursorDisplay), len(layout.projection.Runes))
	rowIndex := 0
	for index, row := range layout.rows {
		if (display >= row.Start && display < row.End) || display == row.End && (index == len(layout.rows)-1 || row.Start == row.End || (row.End < len(layout.projection.Runes) && layout.projection.Runes[row.End] == '\n')) {
			rowIndex = index
			break
		}
	}
	if rowIndex < layout.scrollRow || rowIndex >= layout.scrollRow+layout.visibleRows {
		return nil
	}
	row := layout.rows[rowIndex]
	prefix := m.renderComposerRow(layout.projection, row.Start, min(display, row.End))
	x := ansi.StringWidth(prefix) + m.styles.ComposerFocused.GetBorderLeftSize() + m.styles.ComposerFocused.GetPaddingLeft()
	y := m.composerTopRow + m.styles.ComposerFocused.GetBorderTopSize() + m.styles.ComposerFocused.GetPaddingTop() + 1 + rowIndex - layout.scrollRow
	cursor := m.input.Cursor()
	if cursor == nil {
		cursor = tea.NewCursor(0, 0)
	}
	copy := *cursor
	copy.X, copy.Y = x, y
	return &copy
}

func (m Model) renderMentionText(value string) string {
	projection := composer.ProjectMentions(value, composer.MentionTokens(value))
	return m.renderProjectedMentions(projection, 0, len(projection.Runes))
}

func (m Model) renderProjectedMentions(projection composer.MentionProjection, start, end int) string {
	return m.renderComposerRow(projection, start, end)
}

func (m Model) renderComposerRow(projection composer.MentionProjection, start, end int) string {
	if start >= end {
		return ""
	}
	start = min(max(0, start), len(projection.Runes))
	end = min(max(start, end), len(projection.Runes))
	if start >= end {
		return ""
	}
	if m.styles.Ascii {
		return string(projection.Runes[start:end])
	}
	runes := projection.Runes[start:end]
	mentions := projection.Mention[start:end]

	var out strings.Builder
	i := 0
	for i < len(runes) {
		// 1. Mentions have highest precedence
		if mentions[i] {
			j := i
			for j < len(runes) && mentions[j] {
				j++
			}
			out.WriteString(m.styles.Accent.Render(string(runes[i:j])))
			i = j
			continue
		}

		// 2. Slash commands (/mode, /diff, /plan, /copy, etc.) at line start
		if start == 0 && i == 0 && runes[0] == '/' {
			j := i + 1
			for j < len(runes) && !unicode.IsSpace(runes[j]) {
				j++
			}
			out.WriteString(m.styles.Command.Render(string(runes[i:j])))
			i = j
			continue
		}

		// 3. Inline code `...`
		if runes[i] == '`' {
			j := i + 1
			for j < len(runes) && runes[j] != '`' && runes[j] != '\n' {
				j++
			}
			if j < len(runes) && runes[j] == '`' {
				out.WriteString(m.styles.Info.Render(string(runes[i : j+1])))
				i = j + 1
				continue
			}
		}

		// 4. Bold **...**
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			j := i + 2
			for j+1 < len(runes) && !(runes[j] == '*' && runes[j+1] == '*') && runes[j] != '\n' {
				j++
			}
			if j+1 < len(runes) && runes[j] == '*' && runes[j+1] == '*' {
				out.WriteString(m.styles.Text.Bold(true).Render(string(runes[i : j+2])))
				i = j + 2
				continue
			}
		}

		// 5. Italic *...*
		if runes[i] == '*' {
			j := i + 1
			for j < len(runes) && runes[j] != '*' && runes[j] != '\n' {
				j++
			}
			if j < len(runes) && runes[j] == '*' {
				out.WriteString(m.styles.Text.Italic(true).Render(string(runes[i : j+1])))
				i = j + 1
				continue
			}
		}

		// 6. Heading prefix (# , ## , ### )
		if i == 0 && runes[0] == '#' {
			j := i
			for j < len(runes) && runes[j] == '#' {
				j++
			}
			if j < len(runes) && runes[j] == ' ' {
				out.WriteString(m.styles.Title.Render(string(runes[i : j+1])))
				i = j + 1
				continue
			}
		}

		// 7. List prefix (- , * )
		if i == 0 && (runes[0] == '-' || runes[0] == '*') && len(runes) > 1 && runes[1] == ' ' {
			out.WriteString(m.styles.Accent.Render(string(runes[:2])))
			i = 2
			continue
		}

		// 8. Blockquote prefix (> )
		if i == 0 && runes[0] == '>' && len(runes) > 1 && runes[1] == ' ' {
			out.WriteString(m.styles.Muted.Render(string(runes[:2])))
			i = 2
			continue
		}

		// Plain text run
		j := i + 1
		for j < len(runes) {
			if mentions[j] || runes[j] == '`' || (runes[j] == '*' && (j+1 >= len(runes) || runes[j+1] == '*')) {
				break
			}
			j++
		}
		out.WriteString(m.styles.Text.Render(string(runes[i:j])))
		i = j
	}
	return out.String()
}

func (m Model) sendButtonView() string {
	if strings.TrimSpace(m.input.Value()) == "" {
		return zone.Mark("send-button", m.styles.Muted.Render("send"))
	}
	return zone.Mark("send-button", m.styles.Accent.Render("send ↵"))
}

func (m Model) planModeStatus() string {
	if m.planDraft {
		return "plan mode · describe request"
	}
	if m.planQuestion != nil {
		return "plan mode · decision required"
	}
	if m.session.PlanModeActive() {
		return "plan mode · researching"
	}
	return ""
}

func highlightSelection(content string, selection *textSelection, style lipgloss.Style) string {
	if selection == nil || !selection.active() {
		return content
	}
	lines := strings.Split(content, "\n")
	startX, startY, endX, endY := orderedSelection(selection)
	for row := max(0, startY); row <= min(len(lines)-1, endY); row++ {
		width := ansi.StringWidth(lines[row])
		left := 0
		right := width
		if row == startY {
			left = min(width, startX)
		}
		if row == endY {
			right = min(width, endX)
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

func (m Model) helpView() string {
	groups := [][]key.Binding{
		{m.keys.Composer.Submit, m.keys.Composer.Newline, m.keys.Composer.Complete},
		{m.keys.Composer.Plans, m.keys.Composer.ToggleMode, m.keys.Composer.ToggleDebug},
		{m.keys.Feed.PgUp, m.keys.Feed.PgDown, m.keys.Feed.Bottom},
		{m.keys.Composer.Clear, m.keys.Composer.Help, m.keys.Composer.Cancel},
	}
	body := m.help.FullHelpView(groups) + "\n\n" + m.styles.Muted.Render("Commands: /copy · /diff · /export · /mode · /plan · /tools")
	return components.Card(m.styles.Overlay, max(24, min(m.width-8, 64)), m.styles.Title.Render("SHORTCUTS & COMMANDS"), body)
}
