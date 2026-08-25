package ui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/ui/composer"
)

func (m *Model) updateMentionMenu() tea.Cmd {
	token, active := composer.ActiveMention(m.input.Value(), composerCursorOffset(m.input))
	if !active {
		m.closeMentionMenu()
		return nil
	}
	if m.mentionOpen && m.mentionQuery == token.Path {
		return nil
	}
	if !m.mentionOpen {
		m.mentionCatalog = composer.MentionCatalog(m.workspace)
		m.mentionOpen = true
		m.paletteOpen = false
	}
	items, filter := composer.MentionSuggestions(m.mentionCatalog, token.Path)
	m.mentionMenu.ResetFilter()
	m.mentionMenu.SetItems(items)
	m.mentionMenu.Select(0)
	if filter != "" {
		m.mentionMenu.SetFilterText(filter)
	}
	m.mentionMenu.Title = "Workspace references"
	m.mentionQuery = token.Path
	m.layout()
	return nil
}

func (m *Model) closeMentionMenu() {
	if !m.mentionOpen {
		return
	}
	m.mentionOpen = false
	m.mentionQuery = ""
	m.mentionCatalog = nil
	m.mentionMenu.ResetFilter()
	m.layout()
}

func (m *Model) selectMention() bool {
	item, ok := m.mentionMenu.SelectedItem().(composer.MentionItem)
	if !ok {
		return false
	}
	token, active := composer.ActiveMention(m.input.Value(), composerCursorOffset(m.input))
	if !active {
		return false
	}
	replacement := composer.MentionReference(item.Path, item.IsDir)
	runes := []rune(m.input.Value())
	tail := ""
	if !item.IsDir && (token.End == len(runes) || !unicode.IsSpace(runes[token.End])) {
		tail = " "
	}
	updated := string(runes[:token.Start]) + replacement + tail + string(runes[token.End:])
	m.input.SetValue(updated)
	m.input.CursorEnd()
	if item.IsDir {
		m.mentionQuery = ""
		m.updateMentionMenu()
	} else {
		m.closeMentionMenu()
	}
	m.resizeComposer()
	return true
}
