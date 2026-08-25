package selectors

import (
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/ui/theme"
)

// Provider is a provider option displayed by ProviderSelector. The selector
// has no dependency on Supremo's provider configuration or command runner.
type Provider struct {
	ID          string
	ProviderID  string
	Name        string
	Description string
	Active      bool
}

type providerItem struct {
	provider Provider
	ascii    bool
}

func (i providerItem) Title() string {
	marker := "○"
	if i.provider.Active {
		marker = "●"
	}
	if i.ascii {
		marker = "[ ]"
		if i.provider.Active {
			marker = "[*]"
		}
	}
	return marker + " " + i.provider.Name
}

func (i providerItem) Description() string { return i.provider.Description }
func (i providerItem) FilterValue() string {
	return i.provider.ProviderID + " " + i.provider.ID + " " + i.provider.Name + " " + i.provider.Description
}

// ProviderSelectedMsg is emitted when the user confirms the highlighted
// provider. The parent decides how to persist or activate it.
type ProviderSelectedMsg struct{ ID string }

// ModelSelectedMsg is emitted when a cached model is confirmed.
type ModelSelectedMsg struct {
	ProviderID string
	ID         string
}

// ProviderSelectorDismissedMsg is emitted instead of tea.Quit when the menu
// is dismissed, so an embedded selector cannot terminate its parent program.
type ProviderSelectorDismissedMsg struct{}

// ProviderSelector is an independently reusable, single-choice provider menu.
// Supremo has one active runtime provider, so presenting multi-select controls
// here would create a state the backend cannot represent.
type ProviderSelector struct {
	list    list.Model
	card    lipgloss.Style
	muted   lipgloss.Style
	label   string
	model   bool
	width   int
	height  int
	compact bool
}

// NewProviderSelector creates a searchable provider selection model.
func NewProviderSelector(providers []Provider, design theme.Theme) ProviderSelector {
	return newSingleChoiceSelector(providers, design, "Select provider", "Providers", false)
}

// NewModelSelector presents cached models with the provider selector's
// searchable radio-list interaction.
func NewModelSelector(options []Provider, design theme.Theme) ProviderSelector {
	options = append([]Provider(nil), options...)
	for index := range options {
		if options[index].Name == "" {
			options[index].Name = options[index].ID
		}
	}
	return newSingleChoiceSelector(options, design, "Select model", "Models", true)
}

func newSingleChoiceSelector(options []Provider, design theme.Theme, title, label string, model bool) ProviderSelector {
	menu := newThemedList(providerListItems(options, design.NoColor), design, 64, 12)
	menu.Title = title
	menu.KeyMap.CursorUp.SetHelp("↑", "up")
	menu.KeyMap.CursorDown.SetHelp("↓", "down")
	menu.KeyMap.NextPage.Unbind()
	menu.KeyMap.PrevPage.Unbind()
	menu.KeyMap.GoToStart.Unbind()
	menu.KeyMap.GoToEnd.Unbind()
	menu.KeyMap.AcceptWhileFiltering.SetKeys("enter", "tab", "shift+tab", "up", "down")
	for index, provider := range options {
		if provider.Active {
			menu.Select(index)
			break
		}
	}

	selector := ProviderSelector{
		list:  menu,
		card:  design.Card,
		muted: design.Base.Foreground(design.Secondary),
		label: label,
		model: model,
	}
	selector.SetSize(72, 18)
	return selector
}

// Init implements tea.Model.
func (ProviderSelector) Init() tea.Cmd { return nil }

// Update implements tea.Model. Escape clears an active list filter first;
// otherwise it dismisses the selector without ever returning tea.Quit.
func (m ProviderSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil
	case tea.KeyPressMsg:
		if (msg.Code == tea.KeyEsc || msg.String() == "esc") && m.list.FilterState() == list.Unfiltered {
			return m, func() tea.Msg { return ProviderSelectorDismissedMsg{} }
		}
		if m.list.FilterState() != list.Filtering && (msg.Code == tea.KeyEnter || msg.Code == tea.KeySpace || msg.String() == "enter" || msg.String() == "space") {
			if item, ok := m.list.SelectedItem().(providerItem); ok {
				if m.model {
					return m, func() tea.Msg { return ModelSelectedMsg{ProviderID: item.provider.ProviderID, ID: item.provider.ID} }
				}
				return m, func() tea.Msg { return ProviderSelectedMsg{ID: item.provider.ID} }
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// SetSize adjusts the card and its list for a parent terminal resize.
func (m *ProviderSelector) SetSize(width, height int) {
	m.width = max(1, width-m.card.GetHorizontalFrameSize())
	m.height = max(1, height-m.card.GetVerticalFrameSize())
	m.compact = m.width < 20 || m.height < 3
	m.list.SetFilteringEnabled(!m.compact)
	m.list.SetSize(m.width, max(1, m.height-1))
}

// Selected returns the highlighted option without committing it.
func (m ProviderSelector) Selected() (Provider, bool) {
	item, ok := m.list.SelectedItem().(providerItem)
	return item.provider, ok
}

// View renders the selector. Very narrow terminals retain keyboard selection
// without rendering a list that would overflow the terminal grid.
func (m ProviderSelector) View() tea.View {
	if m.compact {
		name := "No " + strings.ToLower(m.label)
		if item, ok := m.list.SelectedItem().(providerItem); ok {
			name = item.provider.Name
		}
		name = truncate(name, max(1, m.width-2))
		if m.height < 3 {
			return tea.NewView(m.card.Padding(0, 1).Width(m.width).Render(truncate(m.label+": "+name, m.width)))
		}
		hint := truncate("↵ select · esc close", m.width)
		content := strings.Join([]string{
			truncate(m.label, m.width),
			"> " + name,
			hint,
		}, "\n")
		return tea.NewView(m.card.Width(m.width).Render(content))
	}
	hint := truncate("↑↓ choose  ·  / filter  ·  space/enter select  ·  esc close", m.width)
	content := m.list.View() + "\n" + m.muted.Render(hint)
	return tea.NewView(m.card.Width(m.width).Render(content))
}

func providerListItems(providers []Provider, ascii bool) []list.Item {
	items := make([]list.Item, 0, len(providers))
	for _, provider := range providers {
		items = append(items, providerItem{provider: provider, ascii: ascii})
	}
	return items
}

func truncate(value string, width int) string {
	if width <= 0 {
		return value
	}
	return ansi.TruncateWc(value, width, "…")
}
