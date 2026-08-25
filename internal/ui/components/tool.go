package components

import (
	"strings"
)

// ToolView formats a transcript tool row: header, badge, optional details.
func ToolView(header, badge, details string, expanded bool, groupHint string, _ func(symbol, fallback string) string) string {
	line := header
	if badge != "" {
		line += "  " + badge
	}
	if details == "" {
		return line
	}
	indent := "  "
	if !expanded && groupHint != "" {
		return line + "\n" + indent + groupHint
	}
	return line + "\n" + indent + strings.ReplaceAll(details, "\n", "\n"+indent)
}
