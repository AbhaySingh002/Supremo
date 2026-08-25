package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"
)

func (m Model) activityRailWidth() int {
	if m.width < 120 || !m.showActivity || m.hasTransientSurface() {
		return 0
	}
	return min(36, max(30, m.width/4))
}

func (m Model) activityInspectorOpen() bool {
	return m.width < 120 && m.showActivity && m.surface == surfaceActivity
}

func (m Model) hasTransientSurface() bool {
	return m.surface != surfaceNone && m.surface != surfaceActivity
}

func (m Model) activityView(width, height int) string {
	width, height = max(20, width), max(1, height)
	line := func(value string) string { return ansi.Truncate(value, width-4, "…") }
	sections := []string{m.styles.Title.Render(m.glyph("◫", "#") + " ACTIVITY")}
	phase := m.phase
	if phase == "" {
		phase = "idle"
	}
	run := "○ " + phase
	if m.active != nil {
		run = m.spinner.View() + " " + phase
	}
	sections = append(sections, "", m.styles.Muted.Render("run"), line(run))
	if len(m.todos) > 0 {
		sections = append(sections, "", m.styles.Muted.Render("tasks"))
		for _, item := range m.todos {
			symbol := "○"
			if item.Status == "completed" {
				symbol = "✓"
			} else if item.Status == "in_progress" {
				symbol = "●"
			}
			sections = append(sections, line(symbol+" "+item.Content))
		}
	}
	if len(m.activity) > 0 {
		sections = append(sections, "", m.styles.Muted.Render("tools"))
		start := max(0, len(m.activity)-5)
		for idx, item := range m.activity[start:] {
			sections = append(sections, m.activityToolRow(item, start+idx, width-4))
		}
	}
	if len(m.agents) > 0 {
		sections = append(sections, "", m.styles.Muted.Render("agents"))
		for _, item := range m.agents {
			sections = append(sections, line(m.toolIcon("subagent")+" "+item.Label+" "+m.statusSymbol(item.Status)))
		}
	}
	if len(m.todos) == 0 && len(m.activity) == 0 && len(m.agents) == 0 && m.active == nil {
		sections = append(sections, "", m.styles.Muted.Render("Run details, tools, tasks, and subagents will appear here."))
	}
	content := strings.Join(sections, "\n")
	return m.styles.Text.Padding(1, 2).Width(max(1, width-5)).Height(max(1, height-2)).Render(content)
}

func (m Model) activityToolRow(item activityEvent, index int, width int) string {
	status := m.statusSymbol(item.Status)
	label := formatToolSummary(item.Tool, item.Status, item.Arguments)
	if label == "" {
		label = strings.ReplaceAll(item.Tool, "_", " ")
	}
	left := m.toolIcon(item.Tool) + " " + m.styles.Muted.Render(ansi.Truncate(label, max(1, width-lipgloss.Width(status)-4), "…"))
	gap := strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(status)))
	row := left + gap + status
	return zone.Mark(fmt.Sprintf("activity-tool-%d", index), row)
}

func (m Model) statusSymbol(status string) string {
	switch strings.ToLower(status) {
	case "completed", "done", "approved":
		return m.styles.ToolSuccess.Render(m.glyph("✓", "OK"))
	case "running", "in_progress", "queued", "cancelling":
		return m.styles.ToolRunning.Render(m.glyph("●", "*"))
	case "failed", "denied", "cancelled", "interrupted":
		return m.styles.ToolFailure.Render(m.glyph("×", "X"))
	default:
		return m.styles.Muted.Render(m.glyph("○", "-"))
	}
}

func (m Model) joinActivity(primary, activity string) string {
	separator := lipgloss.NewStyle().Foreground(m.styles.ToolDrawer.GetBorderTopForeground()).Render(m.glyph("│", "|"))
	return lipgloss.JoinHorizontal(lipgloss.Top, primary, separator, activity)
}
