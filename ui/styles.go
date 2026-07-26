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
	item := base.Copy().Foreground(fg).PaddingLeft(1)
	selected := base.Copy().Foreground(coral).Bold(true).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(coral)

	return styles{
		header:                     base.Copy().Foreground(fg).Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(border).Padding(0, 1),
		welcome:                    base.Copy().Border(lipgloss.NormalBorder()).BorderForeground(coral).Padding(1, 2),
		overlay:                    base.Copy().Background(surface).Border(lipgloss.NormalBorder()).BorderForeground(coral).Padding(1, 2),
		palette:                    base.Copy().Background(surface).Border(lipgloss.NormalBorder()).BorderForeground(border).Padding(0, 1),
		title:                      base.Copy().Bold(true).Foreground(fg),
		text:                       base.Copy().Foreground(fg),
		muted:                      base.Copy().Foreground(muted),
		accent:                     base.Copy().Bold(true).Foreground(coral),
		success:                    base.Copy().Bold(true).Foreground(green),
		warning:                    base.Copy().Bold(true).Foreground(yellow),
		error:                      base.Copy().Bold(true).Foreground(red),
		user:                       base.Copy().Bold(true).Foreground(coral),
		tool:                       base.Copy().Foreground(fg),
		input:                      base.Copy().Foreground(fg).PaddingLeft(1),
		composerBase:               base.Copy().Foreground(fg),
		footer:                     base.Copy().Foreground(muted).Padding(0, 1),
		divider:                    base.Copy().BorderTop(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(border),
		modal:                      base.Copy().Background(surface).Border(lipgloss.DoubleBorder()).BorderForeground(red).Padding(1, 2),
		command:                    base.Copy().Foreground(coral),
		debug:                      base.Copy().Foreground(muted),
		status:                     base.Copy().Foreground(muted),
		paletteTitleBar:            base.Copy().Background(surface).Padding(0, 1),
		paletteTitle:               base.Copy().Background(surface).Bold(true).Foreground(coral).Padding(0, 1),
		commandItem:                item.Copy().Background(surface),
		commandDescription:         item.Copy().Background(surface).Foreground(muted),
		commandSelected:            selected.Copy().Background(surface),
		commandSelectedDescription: selected.Copy().Background(surface).Foreground(fg),
	}
}
