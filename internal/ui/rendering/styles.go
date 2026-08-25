package rendering

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/AbhaySingh002/supremo/internal/ui/theme"
)

// Styles holds all semantic, theme-aware Lip Gloss styles used across Supremo TUI.
type Styles struct {
	Ascii                      bool
	Background                 color.Color
	Foreground                 color.Color
	Header                     lipgloss.Style
	Welcome                    lipgloss.Style
	Overlay                    lipgloss.Style
	Palette                    lipgloss.Style
	Title                      lipgloss.Style
	Text                       lipgloss.Style
	Muted                      lipgloss.Style
	Accent                     lipgloss.Style
	Assistant                  lipgloss.Style
	User                       lipgloss.Style
	Info                       lipgloss.Style
	Success                    lipgloss.Style
	Warning                    lipgloss.Style
	Error                      lipgloss.Style
	Tool                       lipgloss.Style
	Input                      lipgloss.Style
	Selection                  lipgloss.Style
	ComposerBase               lipgloss.Style
	ComposerBorder             lipgloss.Style
	ComposerFocused            lipgloss.Style
	Footer                     lipgloss.Style
	Divider                    lipgloss.Style
	Modal                      lipgloss.Style
	ApprovalModal              lipgloss.Style
	ApprovalDanger             lipgloss.Style
	Command                    lipgloss.Style
	Debug                      lipgloss.Style
	Status                     lipgloss.Style
	UserGutter                 lipgloss.Style
	AssistantGutter            lipgloss.Style
	PaletteTitleBar            lipgloss.Style
	PaletteTitle               lipgloss.Style
	CommandItem                lipgloss.Style
	CommandDescription         lipgloss.Style
	CommandSelected            lipgloss.Style
	CommandSelectedDescription lipgloss.Style
	PlanModal                  lipgloss.Style
	PlanTitle                  lipgloss.Style
	PlanSubtitle               lipgloss.Style
	PlanOptionKey              lipgloss.Style
	PlanOptionRecommended      lipgloss.Style
	PlanOptionSelected         lipgloss.Style
	PlanOptionIndicator        lipgloss.Style
	PlanTradeoff               lipgloss.Style
	PlanProgress               lipgloss.Style
	PlanResearch               lipgloss.Style
	PlanQuestion               lipgloss.Style
	PlanReady                  lipgloss.Style
	PlanActive                 lipgloss.Style
	PlanComplete               lipgloss.Style
	ToolRunning                lipgloss.Style
	ToolSuccess                lipgloss.Style
	ToolFailure                lipgloss.Style
	ToolRead                   lipgloss.Style
	ToolWrite                  lipgloss.Style
	ToolSearch                 lipgloss.Style
	ToolCommand                lipgloss.Style
	ToolGit                    lipgloss.Style
	ToolAgent                  lipgloss.Style
	ToolDrawer                 lipgloss.Style
	BadgeReady                 lipgloss.Style
	BadgeExecuting             lipgloss.Style
	BadgeWarning               lipgloss.Style
	BadgeError                 lipgloss.Style
}

// NewStyles constructs a new Styles struct from the active theme.
func NewStyles() Styles {
	design := theme.Default()
	base := design.Base
	item := base.Foreground(design.Primary).PaddingLeft(1)
	border := lipgloss.NormalBorder()
	if design.NoColor {
		border = lipgloss.ASCIIBorder()
	}
	selected := base.Foreground(design.Accent).Bold(true).Border(border, false, false, false, true).BorderForeground(design.Accent)

	userGutter := base.Background(design.Surface).Border(border, false, false, false, true).BorderForeground(design.Info).PaddingLeft(1)
	assistantGutter := base.Padding(0, 2)

	composerBorder := base.Background(design.Surface).Border(border).BorderForeground(design.Border).Padding(0, 1)
	composerFocused := composerBorder.BorderForeground(design.Accent)
	drawerBorder := lipgloss.RoundedBorder()
	if design.NoColor {
		drawerBorder = lipgloss.ASCIIBorder()
	}
	var background, foreground color.Color
	if !design.NoColor {
		background, foreground = design.Background, design.Primary
	}

	return Styles{
		Ascii:                      design.NoColor,
		Background:                 background,
		Foreground:                 foreground,
		Header:                     base.Foreground(design.Primary).Border(border, false, false, true, false).BorderForeground(design.Border).Padding(0, 1),
		Welcome:                    base.Border(border).BorderForeground(design.Border).Padding(1, 2),
		Overlay:                    base.Background(design.Surface).Border(border).BorderForeground(design.Accent).Padding(1, 2),
		Palette:                    base.Background(design.Surface).Border(border).BorderForeground(design.Border).Padding(0, 1),
		Title:                      base.Bold(true).Foreground(design.Primary),
		Text:                       base.Foreground(design.Primary),
		Muted:                      base.Foreground(design.Secondary),
		Accent:                     base.Bold(true).Foreground(design.Accent),
		Assistant:                  base.Bold(true).Foreground(design.Assistant),
		User:                       base.Bold(true).Foreground(design.Info),
		Info:                       base.Bold(true).Foreground(design.Info),
		Success:                    base.Bold(true).Foreground(design.Success),
		Warning:                    base.Bold(true).Foreground(design.Warning),
		Error:                      base.Bold(true).Foreground(design.Error),
		Tool:                       base.Foreground(design.Primary),
		Input:                      base.Foreground(design.Primary).PaddingLeft(1),
		Selection:                  base.Foreground(design.Background).Background(design.Primary),
		ComposerBase:               base.Background(design.Surface).Foreground(design.Primary),
		ComposerBorder:             composerBorder,
		ComposerFocused:            composerFocused,
		Footer:                     base.Foreground(design.Secondary).Padding(0, 1),
		Divider:                    base.BorderTop(true).BorderStyle(border).BorderForeground(design.Border),
		Modal:                      base.Background(design.Surface).Border(border).BorderForeground(design.Warning).Padding(1, 2),
		ApprovalModal:              base.Background(design.ElevatedBG).Border(border).BorderForeground(design.BorderStrong).Padding(1, 2),
		ApprovalDanger:             base.Bold(true).Foreground(design.ApprovalDanger),
		Command:                    base.Bold(true).Foreground(design.Accent),
		Debug:                      base.Foreground(design.Secondary),
		Status:                     base.Foreground(design.Secondary),
		UserGutter:                 userGutter,
		AssistantGutter:            assistantGutter,
		PaletteTitleBar:            base.Background(design.Surface).Padding(0, 1),
		PaletteTitle:               base.Background(design.Surface).Bold(true).Foreground(design.Accent).Padding(0, 1),
		CommandItem:                item,
		CommandDescription:         item.Foreground(design.Secondary),
		CommandSelected:            selected,
		CommandSelectedDescription: selected.Foreground(design.Primary),
		PlanModal:                  base.Background(design.Surface).Border(border).BorderForeground(design.Accent).Padding(1, 2),
		PlanTitle:                  base.Bold(true).Foreground(design.Primary),
		PlanSubtitle:               base.Foreground(design.Secondary),
		PlanOptionKey:              base.Bold(true).Foreground(design.Accent),
		PlanOptionRecommended:      base.Bold(true).Foreground(design.Success),
		PlanOptionSelected:         selected,
		PlanOptionIndicator:        base.Bold(true).Foreground(design.Accent),
		PlanTradeoff:               base.Foreground(design.Secondary),
		PlanProgress:               base.Foreground(design.Secondary),
		PlanResearch:               base.Bold(true).Foreground(design.Secondary),
		PlanQuestion:               base.Bold(true).Foreground(design.Warning),
		PlanReady:                  base.Bold(true).Foreground(design.Success),
		PlanActive:                 base.Bold(true).Foreground(design.Accent),
		PlanComplete:               base.Bold(true).Foreground(design.Success),
		ToolRunning:                base.Bold(true).Foreground(design.Accent),
		ToolSuccess:                base.Foreground(design.Success),
		ToolFailure:                base.Bold(true).Foreground(design.Error),
		ToolRead:                   base.Foreground(design.ToolRead),
		ToolWrite:                  base.Foreground(design.ToolWrite),
		ToolSearch:                 base.Foreground(design.ToolSearch),
		ToolCommand:                base.Foreground(design.ToolCommand),
		ToolGit:                    base.Foreground(design.ToolGit),
		ToolAgent:                  base.Foreground(design.ToolAgent),
		ToolDrawer:                 base.Background(design.ElevatedBG).Border(drawerBorder).BorderForeground(design.Border).Padding(0, 1),
		BadgeReady:                 base.Bold(true).Foreground(design.Success),
		BadgeExecuting:             base.Bold(true).Foreground(design.Accent),
		BadgeWarning:               base.Bold(true).Foreground(design.Warning),
		BadgeError:                 base.Bold(true).Foreground(design.Error),
	}
}
