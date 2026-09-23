package selectors

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/ui/theme"
)

// Provider is a provider or model option displayed by ProviderSelector.
type Provider struct {
	ID           string
	ProviderID   string
	ProviderName string
	Name         string
	Description  string
	Active       bool
}

// ProviderSelectedMsg is emitted when the user confirms the highlighted
// provider. The parent decides how to persist or activate it.
type ProviderSelectedMsg struct{ ID string }

// ModelSelectedMsg is emitted when a model is confirmed.
type ModelSelectedMsg struct {
	ProviderID string
	ID         string
}

// ProviderSelectorDismissedMsg is emitted instead of tea.Quit when the menu
// is dismissed, so an embedded selector cannot terminate its parent program.
type ProviderSelectorDismissedMsg struct{}

type modelGroup struct {
	name  string
	items []Provider
}

// ProviderSelector is an independently reusable, single-choice selector
// supporting grouped provider/model listings with live search and keyboard navigation.
type ProviderSelector struct {
	rawOptions   []Provider
	search       textinput.Model
	cursor       int
	scrollOffset int
	design       theme.Theme
	card         lipgloss.Style
	muted        lipgloss.Style
	title        string
	label        string
	model        bool
	width        int
	height       int
	compact      bool
}

// NewProviderSelector creates a searchable provider selection model.
func NewProviderSelector(providers []Provider, design theme.Theme) ProviderSelector {
	return newSelector(providers, design, "Select provider", "Providers", false)
}

// NewModelSelector presents models grouped by provider with live search filtering.
func NewModelSelector(options []Provider, design theme.Theme) ProviderSelector {
	options = append([]Provider(nil), options...)
	for index := range options {
		if options[index].Name == "" {
			options[index].Name = options[index].ID
		}
	}
	return newSelector(options, design, "Select model", "Models", true)
}

func newSelector(options []Provider, design theme.Theme, title, label string, isModel bool) ProviderSelector {
	ti := textinput.New()
	ti.Prompt = "Search  "
	ti.Placeholder = "Search..."
	ti.Focus()

	tiStyles := ti.Styles()
	tiStyles.Focused.Prompt = design.Base.Foreground(design.Accent).Bold(true)
	tiStyles.Focused.Text = design.Base.Foreground(design.Primary)
	tiStyles.Focused.Placeholder = design.Base.Foreground(design.Secondary)
	tiStyles.Blurred.Prompt = design.Base.Foreground(design.Secondary)
	tiStyles.Blurred.Text = design.Base.Foreground(design.Primary)
	tiStyles.Cursor.Color = design.Accent
	ti.SetStyles(tiStyles)

	initialCursor := 0
	for index, opt := range options {
		if opt.Active {
			initialCursor = index
			break
		}
	}

	borderStyle := lipgloss.NormalBorder()
	if design.NoColor {
		borderStyle = lipgloss.ASCIIBorder()
	}
	// Reduced border padding (0 vertical, 1 horizontal) for clean compact aesthetics
	cardStyle := design.Base.Background(design.Surface).Border(borderStyle).BorderForeground(design.Border).Padding(0, 1)

	selector := ProviderSelector{
		rawOptions:   options,
		search:       ti,
		cursor:       initialCursor,
		scrollOffset: 0,
		design:       design,
		card:         cardStyle,
		muted:        design.Base.Foreground(design.Secondary),
		title:        title,
		label:        label,
		model:        isModel,
		width:        72,
		height:       18,
	}
	selector.SetSize(72, 18)
	return selector
}

// Init implements tea.Model.
func (m ProviderSelector) Init() tea.Cmd { return textinput.Blink }

// Update implements tea.Model.
func (m ProviderSelector) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.SetSize(msg.Width, msg.Height)
		return m, nil

	case tea.PasteMsg:
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.cursor = 0
		m.scrollOffset = 0
		return m, cmd

	case tea.MouseMsg:
		mouse := msg.Mouse()
		flatItems := m.flatVisibleItems()
		if len(flatItems) > 0 {
			switch mouse.Button {
			case tea.MouseWheelUp:
				m.cursor = max(0, m.cursor-1)
			case tea.MouseWheelDown:
				m.cursor = min(len(flatItems)-1, m.cursor+1)
			}
		}
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case msg.Code == tea.KeyEsc || msg.String() == "esc":
			if m.search.Value() != "" {
				m.search.Reset()
				m.cursor = 0
				m.scrollOffset = 0
				return m, nil
			}
			return m, func() tea.Msg { return ProviderSelectorDismissedMsg{} }

		case msg.Code == tea.KeyEnter || msg.String() == "enter":
			flatItems := m.flatVisibleItems()
			if len(flatItems) > 0 && m.cursor >= 0 && m.cursor < len(flatItems) {
				item := flatItems[m.cursor]
				if m.model {
					return m, func() tea.Msg { return ModelSelectedMsg{ProviderID: item.ProviderID, ID: item.ID} }
				}
				return m, func() tea.Msg { return ProviderSelectedMsg{ID: item.ID} }
			}
			return m, nil

		case msg.Code == tea.KeyUp || msg.String() == "up" || (msg.Mod == tea.ModCtrl && (msg.Code == 'p' || msg.Code == 'k')):
			flatItems := m.flatVisibleItems()
			if len(flatItems) > 0 {
				m.cursor = max(0, m.cursor-1)
			}
			return m, nil

		case msg.Code == tea.KeyDown || msg.String() == "down" || (msg.Mod == tea.ModCtrl && (msg.Code == 'n' || msg.Code == 'j')):
			flatItems := m.flatVisibleItems()
			if len(flatItems) > 0 {
				m.cursor = min(len(flatItems)-1, m.cursor+1)
			}
			return m, nil

		case msg.Code == tea.KeyHome:
			m.cursor = 0
			return m, nil

		case msg.Code == tea.KeyEnd:
			flatItems := m.flatVisibleItems()
			if len(flatItems) > 0 {
				m.cursor = len(flatItems) - 1
			}
			return m, nil

		case msg.Code == tea.KeyPgUp:
			m.cursor = max(0, m.cursor-5)
			return m, nil

		case msg.Code == tea.KeyPgDown:
			flatItems := m.flatVisibleItems()
			if len(flatItems) > 0 {
				m.cursor = min(len(flatItems)-1, m.cursor+5)
			}
			return m, nil
		}
	}

	oldVal := m.search.Value()
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if m.search.Value() != oldVal {
		m.cursor = 0
		m.scrollOffset = 0
	}
	return m, cmd
}

// SetSize adjusts the card and dimensions for terminal resizing.
func (m *ProviderSelector) SetSize(width, height int) {
	m.width = max(1, width-m.card.GetHorizontalFrameSize())
	m.height = max(1, height-m.card.GetVerticalFrameSize())
	m.compact = m.width < 24 || m.height < 6
	innerWidth := max(20, m.width)
	m.search.SetWidth(max(10, innerWidth-lipgloss.Width(m.search.Prompt)-12))
}

// Selected returns the highlighted option without committing it.
func (m ProviderSelector) Selected() (Provider, bool) {
	flatItems := m.flatVisibleItems()
	if len(flatItems) == 0 || m.cursor < 0 || m.cursor >= len(flatItems) {
		return Provider{}, false
	}
	return flatItems[m.cursor], true
}

func (m ProviderSelector) flatVisibleItems() []Provider {
	if m.model {
		_, flat := m.filteredModelGroups()
		return flat
	}
	return m.filteredProviders()
}

func (m ProviderSelector) filteredProviders() []Provider {
	query := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(m.search.Value())), "/")
	if query == "" {
		return m.rawOptions
	}
	matched := make([]Provider, 0)
	for _, p := range m.rawOptions {
		if strings.Contains(strings.ToLower(p.Name), query) ||
			strings.Contains(strings.ToLower(p.ID), query) ||
			strings.Contains(strings.ToLower(p.Description), query) {
			matched = append(matched, p)
		}
	}
	return matched
}

func (m ProviderSelector) filteredModelGroups() ([]modelGroup, []Provider) {
	query := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(m.search.Value())), "/")

	groupMap := make(map[string]*modelGroup)
	groupOrder := make([]string, 0)

	// Identify active provider to sort first
	activeProviderName := ""
	for _, opt := range m.rawOptions {
		if opt.Active {
			pName := opt.ProviderName
			if pName == "" {
				pName = opt.ProviderID
			}
			activeProviderName = pName
			break
		}
	}

	for _, opt := range m.rawOptions {
		pName := opt.ProviderName
		if pName == "" {
			if opt.ProviderID != "" {
				pName = opt.ProviderID
			} else {
				pName = "Other"
			}
		}

		if query != "" {
			match := strings.Contains(strings.ToLower(opt.Name), query) ||
				strings.Contains(strings.ToLower(opt.ID), query) ||
				strings.Contains(strings.ToLower(opt.ProviderName), query) ||
				strings.Contains(strings.ToLower(opt.ProviderID), query) ||
				strings.Contains(strings.ToLower(opt.Description), query)
			if !match {
				continue
			}
		}

		g, exists := groupMap[pName]
		if !exists {
			g = &modelGroup{name: pName, items: make([]Provider, 0)}
			groupMap[pName] = g
			if pName == activeProviderName {
				// Insert active provider group at the beginning
				groupOrder = append([]string{pName}, groupOrder...)
			} else {
				groupOrder = append(groupOrder, pName)
			}
		}
		g.items = append(g.items, opt)
	}

	groups := make([]modelGroup, 0, len(groupOrder))
	flatList := make([]Provider, 0)
	for _, name := range groupOrder {
		g := *groupMap[name]
		groups = append(groups, g)
		flatList = append(flatList, g.items...)
	}
	return groups, flatList
}

// View renders the selector.
func (m ProviderSelector) View() tea.View {
	if m.compact {
		name := "No " + strings.ToLower(m.label)
		if item, ok := m.Selected(); ok {
			name = item.Name
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

	cardWidth := max(28, m.width)
	cardHeight := max(6, m.height)
	innerWidth := max(20, cardWidth-m.card.GetHorizontalFrameSize())

	// Top line: Clean search input on left, "esc" shortcut on right
	searchView := m.search.View()
	escView := m.muted.Render("esc")
	gap := max(1, innerWidth-lipgloss.Width(searchView)-lipgloss.Width("esc"))
	topLine := searchView + strings.Repeat(" ", gap) + escView

	var rows []string
	modelIndexToRow := make(map[int]int)
	globalModelIdx := 0

	if m.model {
		groups, flatItems := m.filteredModelGroups()
		if len(flatItems) == 0 {
			rows = append(rows, m.muted.Render("  No models matching \""+m.search.Value()+"\""))
		} else {
			for gIdx, g := range groups {
				if gIdx > 0 {
					rows = append(rows, "")
				}
				// Provider section header in bold lavender
				headerStyle := lipgloss.NewStyle().Foreground(m.design.Plan).Bold(true)
				if m.design.NoColor {
					headerStyle = lipgloss.NewStyle().Bold(true)
				}
				rows = append(rows, headerStyle.Render(g.name))

				for _, item := range g.items {
					rowIdx := len(rows)
					modelIndexToRow[globalModelIdx] = rowIdx
					isSelected := (globalModelIdx == m.cursor)

					marker := "  "
					if item.Active {
						marker = "● "
						if m.design.NoColor {
							marker = "* "
						}
					}

					nameText := item.Name
					tagText := item.Description
					availableWidth := max(10, innerWidth-2)

					if isSelected {
						selStyle := lipgloss.NewStyle().Background(m.design.Accent).Foreground(m.design.Background).Bold(true)
						if m.design.NoColor {
							selStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
						}

						left := marker + nameText
						right := tagText
						leftW := lipgloss.Width(left)
						rightW := lipgloss.Width(right)
						if leftW+rightW+1 > availableWidth {
							maxNameW := max(6, availableWidth-rightW-lipgloss.Width(marker)-2)
							left = marker + truncate(nameText, maxNameW)
							leftW = lipgloss.Width(left)
						}
						rowGap := max(1, availableWidth-leftW-rightW)
						lineContent := left + strings.Repeat(" ", rowGap) + right
						rows = append(rows, selStyle.Render(" "+lineContent+" "))
					} else {
						markerStyled := m.muted.Render(marker)
						if item.Active {
							markerStyled = lipgloss.NewStyle().Foreground(m.design.Accent).Render(marker)
						}
						nameStyled := m.design.Base.Foreground(m.design.Primary).Render(nameText)
						tagStyled := m.muted.Render(tagText)

						left := markerStyled + nameStyled
						right := tagStyled
						leftW := lipgloss.Width(marker + nameText)
						rightW := lipgloss.Width(tagText)
						if leftW+rightW+1 > availableWidth {
							maxNameW := max(6, availableWidth-rightW-lipgloss.Width(marker)-2)
							nameStyled = m.design.Base.Foreground(m.design.Primary).Render(truncate(nameText, maxNameW))
							leftW = lipgloss.Width(marker + truncate(nameText, maxNameW))
							left = markerStyled + nameStyled
						}
						rowGap := max(1, availableWidth-leftW-rightW)
						lineContent := " " + left + strings.Repeat(" ", rowGap) + right + " "
						rows = append(rows, lineContent)
					}
					globalModelIdx++
				}
			}
		}
	} else {
		// Flat provider listing
		providers := m.filteredProviders()
		if len(providers) == 0 {
			rows = append(rows, m.muted.Render("  No providers matching \""+m.search.Value()+"\""))
		} else {
			for _, item := range providers {
				rowIdx := len(rows)
				modelIndexToRow[globalModelIdx] = rowIdx
				isSelected := (globalModelIdx == m.cursor)

				marker := "  "
				if item.Active {
					marker = "● "
					if m.design.NoColor {
						marker = "* "
					}
				}

				nameText := item.Name
				tagText := item.Description
				availableWidth := max(10, innerWidth-2)

				if isSelected {
					selStyle := lipgloss.NewStyle().Background(m.design.Accent).Foreground(m.design.Background).Bold(true)
					if m.design.NoColor {
						selStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
					}

					left := marker + nameText
					right := tagText
					leftW := lipgloss.Width(left)
					rightW := lipgloss.Width(right)
					if leftW+rightW+1 > availableWidth {
						maxNameW := max(6, availableWidth-rightW-lipgloss.Width(marker)-2)
						left = marker + truncate(nameText, maxNameW)
						leftW = lipgloss.Width(left)
					}
					rowGap := max(1, availableWidth-leftW-rightW)
					lineContent := left + strings.Repeat(" ", rowGap) + right
					rows = append(rows, selStyle.Render(" "+lineContent+" "))
				} else {
					markerStyled := m.muted.Render(marker)
					if item.Active {
						markerStyled = lipgloss.NewStyle().Foreground(m.design.Accent).Render(marker)
					}
					nameStyled := m.design.Base.Foreground(m.design.Primary).Render(nameText)
					tagStyled := m.muted.Render(tagText)

					left := markerStyled + nameStyled
					right := tagStyled
					leftW := lipgloss.Width(marker + nameText)
					rightW := lipgloss.Width(tagText)
					if leftW+rightW+1 > availableWidth {
						maxNameW := max(6, availableWidth-rightW-lipgloss.Width(marker)-2)
						nameStyled = m.design.Base.Foreground(m.design.Primary).Render(truncate(nameText, maxNameW))
						leftW = lipgloss.Width(marker + truncate(nameText, maxNameW))
						left = markerStyled + nameStyled
					}
					rowGap := max(1, availableWidth-leftW-rightW)
					lineContent := " " + left + strings.Repeat(" ", rowGap) + right + " "
					rows = append(rows, lineContent)
				}
				globalModelIdx++
			}
		}
	}

	// Calculate visible body height budget (overhead: topLine + 1 blank + footer = 3)
	bodyHeight := max(3, cardHeight-3)
	selectedRow := 0
	if r, ok := modelIndexToRow[m.cursor]; ok {
		selectedRow = r
	}

	scrollOffset := m.scrollOffset
	if selectedRow < scrollOffset {
		scrollOffset = selectedRow
	}
	if selectedRow >= scrollOffset+bodyHeight {
		scrollOffset = selectedRow - bodyHeight + 1
	}
	if scrollOffset > len(rows)-bodyHeight {
		scrollOffset = max(0, len(rows)-bodyHeight)
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	endRow := min(len(rows), scrollOffset+bodyHeight)
	var visibleRows []string
	if scrollOffset < len(rows) {
		visibleRows = rows[scrollOffset:endRow]
	}

	// Footer with scrolling indicator if list overflows
	scrollInfo := ""
	flatItems := m.flatVisibleItems()
	if len(rows) > bodyHeight && len(flatItems) > 0 {
		scrollInfo = fmt.Sprintf("(%d/%d) · ", m.cursor+1, len(flatItems))
	}
	footer := m.muted.Render(truncate(scrollInfo+"↑↓ navigate  ·  enter select  ·  esc close", innerWidth))

	content := strings.Join([]string{
		topLine,
		"",
		strings.Join(visibleRows, "\n"),
		footer,
	}, "\n")

	return tea.NewView(m.card.Width(cardWidth).Render(content))
}

func truncate(value string, width int) string {
	if width <= 0 {
		return value
	}
	return ansi.TruncateWc(value, width, "…")
}
