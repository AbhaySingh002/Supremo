// Package theme contains Supremo's shared terminal color tokens.
package theme

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
)

// Theme keeps reusable Bubble Tea components visually consistent without
// making them depend on Supremo's root model.
type Theme struct {
	Base           lipgloss.Style
	Card           lipgloss.Style
	BaseBG         color.Color
	SurfaceBG      color.Color
	ElevatedBG     color.Color
	Text           color.Color
	TextMuted      color.Color
	TextDim        color.Color
	Border         color.Color
	BorderStrong   color.Color
	Background     color.Color
	Surface        color.Color
	Primary        color.Color
	Secondary      color.Color
	Accent         color.Color
	Assistant      color.Color
	User           color.Color
	Plan           color.Color
	PlanResearch   color.Color
	PlanQuestion   color.Color
	PlanReady      color.Color
	PlanActive     color.Color
	PlanComplete   color.Color
	Success        color.Color
	Warning        color.Color
	Error          color.Color
	Info           color.Color
	ApprovalDanger color.Color
	ToolRunning    color.Color
	ToolSuccess    color.Color
	ToolFailure    color.Color
	ToolRead       color.Color
	ToolWrite      color.Color
	ToolSearch     color.Color
	ToolCommand    color.Color
	ToolGit        color.Color
	ToolAgent      color.Color
	NoColor        bool
}

// Default is Supremo's dark "Ink + Signal" palette. Neutral surfaces carry
// content while restrained colours keep stable semantic roles.
func Default() Theme {
	noColor := os.Getenv("NO_COLOR") != ""
	background := lipgloss.Color("#090B0F")
	surface := lipgloss.Color("#11151B")
	elevated := lipgloss.Color("#171C24")

	primary := lipgloss.Color("#E8EAF0")
	secondary := lipgloss.Color("#A7ADB8")
	dim := lipgloss.Color("#858C98")

	accent := lipgloss.Color("#E8B84A")
	assistant := lipgloss.Color("#E8B84A")
	user := lipgloss.Color("#70ADEC")
	plan := lipgloss.Color("#A99BE0")

	planResearch := lipgloss.Color("#66B8D4")
	planQuestion := lipgloss.Color("#D8AD5D")
	planReady := lipgloss.Color("#70C991")
	planActive := lipgloss.Color("#A99BE0")
	planComplete := lipgloss.Color("#70C991")

	success := lipgloss.Color("#70C991")
	warning := lipgloss.Color("#D8AD5D")
	errorColor := lipgloss.Color("#E27682")
	info := lipgloss.Color("#66B8D4")
	approvalDanger := errorColor

	toolRunning := accent
	toolSuccess := success
	toolFailure := errorColor
	toolRead := info
	toolWrite := lipgloss.Color("#E29A61")
	toolSearch := lipgloss.Color("#A99BE0")
	toolCommand := accent
	toolGit := user
	toolAgent := toolSearch

	border := lipgloss.Color("#29303A")
	borderStrong := lipgloss.Color("#414A57")

	if noColor {
		no := lipgloss.NoColor{}
		background, surface, elevated, primary, secondary, dim = no, no, no, no, no, no
		accent, assistant, user = no, no, no
		plan, planResearch, planQuestion, planReady, planActive, planComplete = no, no, no, no, no, no
		success, warning, errorColor, info, approvalDanger = no, no, no, no, no
		toolRunning, toolSuccess, toolFailure, border, borderStrong = no, no, no, no, no
		toolRead, toolWrite, toolSearch, toolCommand, toolGit, toolAgent = no, no, no, no, no, no
	}
	base := lipgloss.NewStyle().Foreground(primary)
	if !noColor {
		base = base.Background(background)
	}
	borderStyle := lipgloss.NormalBorder()
	if noColor {
		borderStyle = lipgloss.ASCIIBorder()
	}
	return Theme{
		Base:           base,
		Card:           base.Background(surface).Border(borderStyle).BorderForeground(border).Padding(1, 2),
		BaseBG:         background,
		SurfaceBG:      surface,
		ElevatedBG:     elevated,
		Text:           primary,
		TextMuted:      secondary,
		TextDim:        dim,
		Border:         border,
		BorderStrong:   borderStrong,
		Background:     background,
		Surface:        surface,
		Primary:        primary,
		Secondary:      secondary,
		Accent:         accent,
		Assistant:      assistant,
		User:           user,
		Plan:           plan,
		PlanResearch:   planResearch,
		PlanQuestion:   planQuestion,
		PlanReady:      planReady,
		PlanActive:     planActive,
		PlanComplete:   planComplete,
		Success:        success,
		Warning:        warning,
		Error:          errorColor,
		Info:           info,
		ApprovalDanger: approvalDanger,
		ToolRunning:    toolRunning,
		ToolSuccess:    toolSuccess,
		ToolFailure:    toolFailure,
		ToolRead:       toolRead,
		ToolWrite:      toolWrite,
		ToolSearch:     toolSearch,
		ToolCommand:    toolCommand,
		ToolGit:        toolGit,
		ToolAgent:      toolAgent,
		NoColor:        noColor,
	}
}
