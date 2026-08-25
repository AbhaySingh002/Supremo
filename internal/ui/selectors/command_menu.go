// Package selectors contains reusable Bubble Tea selection and menu models.
package selectors

import (
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/AbhaySingh002/supremo/internal/ui/theme"
)

// Command is one command suggestion shown by CommandMenu.
type Command struct {
	Name        string
	Description string
}

type commandItem struct{ command Command }

func (i commandItem) Title() string       { return i.command.Name }
func (i commandItem) Description() string { return i.command.Description }
func (i commandItem) FilterValue() string { return i.command.Name + " " + i.command.Description }

// CommandQueryMsg updates the suggestions shown by a CommandMenu. It lets a
// parent-owned input model drive this component without sharing state.
type CommandQueryMsg struct{ Query string }

// CommandMenu is a compact, externally controlled command suggestion list.
// It deliberately leaves text entry to its parent so a caller can use either a
// one-line textinput or a multiline textarea.
type CommandMenu struct {
	list     list.Model
	commands []Command
	query    string
}

func newThemedList(items []list.Item, design theme.Theme, width, height int) list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	item := design.Base.Foreground(design.Primary).Background(design.Surface).PaddingLeft(1)
	border := lipgloss.NormalBorder()
	if design.NoColor {
		border = lipgloss.ASCIIBorder()
	}
	selected := design.Base.Foreground(design.Accent).Background(design.Surface).Bold(true).
		Border(border, false, false, false, true).BorderForeground(design.Accent)
	delegate.Styles.NormalTitle = item
	delegate.Styles.NormalDesc = item.Foreground(design.Secondary)
	delegate.Styles.SelectedTitle = selected
	delegate.Styles.SelectedDesc = selected.Foreground(design.Primary)
	delegate.Styles.DimmedTitle = item
	delegate.Styles.DimmedDesc = item.Foreground(design.Secondary)

	menu := list.New(items, delegate, width, height)
	menu.SetShowStatusBar(false)
	menu.SetShowPagination(false)
	menu.SetShowHelp(false)
	menu.Styles.TitleBar = design.Base.Background(design.Surface).Padding(0, 1)
	menu.Styles.Title = design.Base.Background(design.Surface).Bold(true).Foreground(design.Accent).Padding(0, 1)
	menu.Styles.NoItems = design.Base.Foreground(design.Secondary)
	menu.KeyMap.Quit.SetEnabled(false)
	menu.KeyMap.ForceQuit.SetEnabled(false)
	menu.KeyMap.CursorUp.SetKeys("up")
	menu.KeyMap.CursorDown.SetKeys("down")
	return menu
}

// NewCommandMenu creates a reusable command autocomplete component.
func NewCommandMenu(commands []Command, design theme.Theme) CommandMenu {
	menu := newThemedList(commandListItems(commands), design, 64, 8)
	menu.Title = "Command suggestions"
	menu.SetFilteringEnabled(false)

	return CommandMenu{list: menu, commands: append([]Command(nil), commands...)}
}

// Init implements tea.Model.
func (CommandMenu) Init() tea.Cmd { return nil }

// Update implements tea.Model. CommandQueryMsg is the component boundary used
// by a parent-owned composer to drive real-time autocomplete.
func (m CommandMenu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case CommandQueryMsg:
		return m, m.SetQuery(msg.Query)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// SetQuery filters the suggestion set without introducing a second text input.
func (m *CommandMenu) SetQuery(query string) tea.Cmd {
	query = strings.TrimSpace(strings.ToLower(query))
	m.query = query
	items := make([]list.Item, 0, len(m.commands))
	for _, command := range m.commands {
		item := commandItem{command: command}
		if strings.Contains(strings.ToLower(item.FilterValue()), query) {
			items = append(items, item)
		}
	}
	cmd := m.list.SetItems(items)
	if len(items) > 0 {
		m.list.Select(0)
	}
	return cmd
}

// SetSize adjusts the list to fit the space allocated by its parent.
func (m *CommandMenu) SetSize(width, height int) {
	m.list.SetSize(max(1, width), max(1, height))
}

// Items exposes the current suggestions for callers that need to inspect them.
func (m CommandMenu) Items() []list.Item { return m.list.Items() }

// Selected returns the highlighted command, if there is one.
func (m CommandMenu) Selected() (Command, bool) {
	item, ok := m.list.SelectedItem().(commandItem)
	return item.command, ok
}

// View implements tea.Model.
func (m CommandMenu) View() tea.View {
	return tea.NewView(m.list.View())
}

func commandListItems(commands []Command) []list.Item {
	items := make([]list.Item, 0, len(commands))
	for _, command := range commands {
		items = append(items, commandItem{command: command})
	}
	return items
}
