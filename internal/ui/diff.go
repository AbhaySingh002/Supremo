package ui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/components"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

type diffRenderedMsg struct {
	run, index, width int
	rendered          string
}

type workspaceDiffLoadedMsg struct {
	diff    string
	summary string
	err     error
}

func (m Model) diffOpen() bool {
	if m.surface != surfaceDiff {
		return false
	}
	if m.workspaceDiff != "" {
		return true
	}
	return m.diffEntry >= 0 && m.diffEntry < len(m.entries) && m.entries[m.diffEntry].kind == entryDiff
}

func (m *Model) openDiffInspector(index int) tea.Cmd {
	if index < 0 || index >= len(m.entries) || m.entries[index].kind != entryDiff {
		return nil
	}
	m.diffEntry = index
	m.surface = surfaceDiff
	m.workspaceDiff = ""
	m.workspaceDiffSummary = ""
	m.followTail = false
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.input.Blur()
	m.layout()
	return m.renderActiveDiff()
}

func (m *Model) openWorkspaceDiffInspector(diff, summary string) tea.Cmd {
	m.workspaceDiff = diff
	m.surface = surfaceDiff
	m.workspaceDiffSummary = summary
	m.diffEntry = -1
	m.followTail = false
	m.priorFocus, m.focus = m.focus, focusOverlay
	m.input.Blur()
	m.layout()
	return m.renderActiveDiff()
}

func (m *Model) closeDiffInspector() tea.Cmd {
	m.diffEntry = -1
	m.workspaceDiff = ""
	m.workspaceDiffSummary = ""
	m.surface = surfaceNone
	m.layout()
	return m.restoreFocus()
}

func (m *Model) renderActiveDiff() tea.Cmd {
	if !m.diffOpen() {
		return nil
	}
	diff := m.workspaceDiff
	if diff == "" && m.diffEntry >= 0 && m.diffEntry < len(m.entries) {
		diff = m.entries[m.diffEntry].content
	}
	width := max(20, m.diffViewport.Width())
	m.diffRun++
	m.diffViewport.SetContent("Rendering diff…")
	return renderDiffCmd(m.diffRun, m.diffEntry, width, diff, m.styles.Ascii)
}

func renderDiffCmd(run, index, width int, diff string, ascii bool) tea.Cmd {
	return func() tea.Msg {
		rendered := rendering.HighlightDiff(diff, ascii)
		return diffRenderedMsg{run: run, index: index, width: width, rendered: rendered}
	}
}

func loadWorkspaceDiffCmd(ctx context.Context, client api.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return workspaceDiffLoadedMsg{err: fmt.Errorf("backend is unavailable")}
		}
		diff, err := client.WorkspaceDiff(ctx)
		if err != nil {
			return workspaceDiffLoadedMsg{err: err}
		}
		return workspaceDiffLoadedMsg{diff: diff.Content, summary: diff.Summary}
	}
}

func (m Model) updateDiffInspector(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" || msg.Code == tea.KeyEsc {
		return m, m.closeDiffInspector()
	}
	switch msg.String() {
	case "home":
		m.diffViewport.GotoTop()
		return m, nil
	case "end":
		m.diffViewport.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.diffViewport, cmd = m.diffViewport.Update(msg)
	return m, cmd
}

func (m Model) diffInspectorView() string {
	if !m.diffOpen() {
		return ""
	}
	title := "DIFF INSPECTOR"
	summary := m.workspaceDiffSummary
	if summary == "" && m.diffEntry >= 0 && m.diffEntry < len(m.entries) {
		summary = diffSummary(m.entries[m.diffEntry].content)
	}
	if summary == "" {
		summary = "Workspace uncommitted changes"
	}
	content := strings.Join([]string{
		m.styles.Muted.Render(summary),
		m.diffViewport.View(),
	}, "\n")
	return components.Card(m.styles.Overlay, max(24, min(m.width-8, 100)), m.styles.Title.Render(title), content)
}

func (m Model) latestDetailIndex() int {
	for index := len(m.entries) - 1; index >= 0; index-- {
		entry := m.entries[index]
		if entry.kind == entryDiff || entry.kind == entryTool && entry.details != "" {
			return index
		}
	}
	return -1
}
