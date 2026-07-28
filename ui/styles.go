package ui

import "github.com/charmbracelet/lipgloss"

type styles struct {
	header                     lipgloss.Style
	welcome                    lipgloss.Style
	overlay                    lipgloss.Style
	palette                    lipgloss.Style
	title                      lipgloss.Style
	text                       lipgloss.Style
	muted                      lipgloss.Style
	accent                     lipgloss.Style
	success                    lipgloss.Style
	warning                    lipgloss.Style
	error                      lipgloss.Style
	user                       lipgloss.Style
	tool                       lipgloss.Style
	input                      lipgloss.Style
	composerBase               lipgloss.Style
	footer                     lipgloss.Style
	divider                    lipgloss.Style
	modal                      lipgloss.Style
	command                    lipgloss.Style
	debug                      lipgloss.Style
	status                     lipgloss.Style
	paletteTitleBar            lipgloss.Style
	paletteTitle               lipgloss.Style
	commandItem                lipgloss.Style
	commandDescription         lipgloss.Style
	commandSelected            lipgloss.Style
	commandSelectedDescription lipgloss.Style
}

func newStyles() styles {
	charcoal := lipgloss.AdaptiveColor{Light: "#1E1E1E", Dark: "#1E1E1E"}
	surface := lipgloss.AdaptiveColor{Light: "#242424", Dark: "#242424"}
	fg := lipgloss.AdaptiveColor{Light: "#F4F4F1", Dark: "#F4F4F1"}
	muted := lipgloss.AdaptiveColor{Light: "#9A9A9A", Dark: "#9A9A9A"}
	border := lipgloss.AdaptiveColor{Light: "#5E5E5E", Dark: "#5E5E5E"}
	coral := lipgloss.AdaptiveColor{Light: "#E07A5F", Dark: "#E07A5F"}
	green := lipgloss.AdaptiveColor{Light: "#65C466", Dark: "#65C466"}
	yellow := lipgloss.AdaptiveColor{Light: "#F1C453", Dark: "#F1C453"}
	red := lipgloss.AdaptiveColor{Light: "#EF6461", Dark: "#EF6461"}

	base := lipgloss.NewStyle().Background(charcoal)
	item := base.Foreground(fg).PaddingLeft(1)
	selected := base.Foreground(coral).Bold(true).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(coral)

	return styles{
		header:                     base.Foreground(fg).Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(border).Padding(0, 1),
		welcome:                    base.Border(lipgloss.NormalBorder()).BorderForeground(coral).Padding(1, 2),
		overlay:                    base.Background(surface).Border(lipgloss.NormalBorder()).BorderForeground(coral).Padding(1, 2),
		palette:                    base.Background(surface).Border(lipgloss.NormalBorder()).BorderForeground(border).Padding(0, 1),
		title:                      base.Bold(true).Foreground(fg),
		text:                       base.Foreground(fg),
		muted:                      base.Foreground(muted),
		accent:                     base.Bold(true).Foreground(coral),
		success:                    base.Bold(true).Foreground(green),
		warning:                    base.Bold(true).Foreground(yellow),
		error:                      base.Bold(true).Foreground(red),
		user:                       base.Bold(true).Foreground(coral),
		tool:                       base.Foreground(fg),
		input:                      base.Foreground(fg).PaddingLeft(1),
		composerBase:               base.Foreground(fg),
		footer:                     base.Foreground(muted).Padding(0, 1),
		divider:                    base.BorderTop(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(border),
		modal:                      base.Background(surface).Border(lipgloss.DoubleBorder()).BorderForeground(red).Padding(1, 2),
		command:                    base.Foreground(coral),
		debug:                      base.Foreground(muted),
		status:                     base.Foreground(muted),
		paletteTitleBar:            base.Background(surface).Padding(0, 1),
		paletteTitle:               base.Background(surface).Bold(true).Foreground(coral).Padding(0, 1),
		commandItem:                item.Background(surface),
		commandDescription:         item.Background(surface).Foreground(muted),
		commandSelected:            selected.Background(surface),
		commandSelectedDescription: selected.Background(surface).Foreground(fg),
	}
}
