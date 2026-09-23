package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) activityRailWidth() int {
	if m.width < 120 || !m.contextualActivityVisible() || m.hasTransientSurface() {
		return 0
	}
	return min(36, max(30, m.width/4))
}

func (m Model) contextualActivityVisible() bool {
	return m.planDraft || m.session.PlanModeActive() || m.hasActiveSubagent()
}

func (m Model) hasActiveSubagent() bool {
	for _, agent := range m.agents {
		switch strings.ToLower(agent.Status) {
		case "queued", "running", "cancelling":
			return true
		}
	}
	return false
}

func (m Model) hasTransientSurface() bool {
	return m.surface != surfaceNone
}

func (m Model) activityView(width, height int) string {
	width, height = max(20, width), max(1, height)
	line := func(value string) string { return ansi.Truncate(value, width-4, "…") }
	planActive := m.planDraft || m.session.PlanModeActive()
	agentsActive := m.hasActiveSubagent()
	title := "PLAN"
	if agentsActive && !planActive {
		title = "AGENTS"
	} else if agentsActive {
		title = "PLAN · AGENTS"
	}
	sections := []string{m.styles.Title.Render(m.glyph("◫", "#") + " " + title)}
	if planActive {
		phase := m.phase
		if phase == "" {
			phase = "planning"
		}
		run := m.glyph("●", "*") + " " + phase
		if m.active != nil {
			run = m.spinner.View() + " " + phase
		}
		sections = append(sections, "", m.styles.Muted.Render("plan"), line(run))
	}
	if planActive && len(m.todos) > 0 {
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
	if agentsActive {
		sections = append(sections, "", m.styles.Muted.Render("agents"))
		for _, item := range m.agents {
			switch strings.ToLower(item.Status) {
			case "queued", "running", "cancelling":
			default:
				continue
			}
			sections = append(sections, line(m.toolIcon("subagent")+" "+item.Label+" "+m.statusSymbol(item.Status)))
		}
	}
	content := strings.Join(sections, "\n")
	return m.styles.Text.Padding(1, 2).Width(max(1, width-5)).Height(max(1, height-2)).Render(content)
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
