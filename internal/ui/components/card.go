package components

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Card wraps title + body in an existing Lip Gloss style (overlay, modal, plan).
func Card(style lipgloss.Style, width int, title, body string) string {
	if width > 0 {
		style = style.Width(width)
	}
	parts := make([]string, 0, 2)
	if title != "" {
		parts = append(parts, title)
	}
	if body != "" {
		parts = append(parts, body)
	}
	return style.Render(strings.Join(parts, "\n\n"))
}
