package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// userError contains human-readable error diagnosis and recommended remedy.
type userError struct {
	Title       string
	Explanation string
	Action      string
}

// parseUserError extracts structured, actionable guidance from raw runtime or agent error strings.
func parseUserError(raw string) userError {
	text := strings.TrimSpace(raw)
	text = strings.TrimPrefix(text, "✕ ")
	text = strings.TrimPrefix(text, "ERROR: ")
	text = strings.TrimPrefix(text, "error: ")
	lower := strings.ToLower(text)

	switch {
	case strings.Contains(lower, "model made no progress after requesting context:") || strings.Contains(lower, "could not fetch external url") || strings.Contains(lower, "context:http"):
		url := ""
		if idx := strings.Index(text, "context:"); idx >= 0 {
			url = strings.TrimSpace(text[idx+len("context:"):])
		}
		target := "an external URL"
		if url != "" {
			target = url
		}
		return userError{
			Title:       "Could not load external URL context (" + target + ")",
			Explanation: "Supremo operates directly on your local workspace repository files.",
			Action:      "Copy and paste the webpage content or README text directly into the prompt.",
		}

	case strings.HasPrefix(lower, "usage: /"):
		cmd := strings.TrimPrefix(text, "usage: ")
		fields := strings.Fields(cmd)
		cmdName := "/help"
		if len(fields) > 0 {
			cmdName = fields[0]
		}
		return userError{
			Title:       "Invalid command syntax: " + cmd,
			Explanation: "Command was provided with unexpected or incomplete arguments.",
			Action:      "Run " + cmdName + " or type /help to view valid parameters.",
		}

	case strings.Contains(lower, "needs an api key") || strings.Contains(lower, "key needed") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "401") || strings.Contains(lower, "invalid_api_key"):
		return userError{
			Title:       "API key is missing or unauthorized",
			Explanation: "The active model provider requires a verified API key to process requests.",
			Action:      "Run /auth to enter a credential securely, or /provider to select another provider.",
		}

	case strings.Contains(lower, "no such host") || strings.Contains(lower, "dial tcp") || strings.Contains(lower, "connection refused") || strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "could not resolve host") || strings.Contains(lower, "error sending request"):
		return userError{
			Title:       "Network connection failed — could not reach model provider",
			Explanation: "Unable to connect to the remote AI provider endpoint (DNS lookup failed or network offline).",
			Action:      "Check your internet connection, DNS configuration, or VPN.",
		}

	case strings.Contains(lower, "429") || strings.Contains(lower, "quota") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "resource_exhausted") || strings.Contains(lower, "insufficient_quota"):
		return userError{
			Title:       "Provider rate limit or quota exceeded",
			Explanation: "Your account has reached its provider rate limit or run out of credits.",
			Action:      "Check account balance with /usage, wait a moment before retrying, or switch models with /model.",
		}

	case strings.Contains(lower, "context length exceeded") || strings.Contains(lower, "maximum context length") || strings.Contains(lower, "token limit"):
		return userError{
			Title:       "Conversation exceeded model token context window",
			Explanation: "The current session has accumulated too much conversation history for the model.",
			Action:      "Start a fresh session with /session new, or switch to a high-context model.",
		}

	case strings.Contains(lower, "tool execution failed") || strings.Contains(lower, "permission denied") || strings.Contains(lower, "executable file not found"):
		return userError{
			Title:       "Tool execution error in workspace",
			Explanation: text,
			Action:      "Verify required CLI tools are installed and file permissions are correct.",
		}

	default:
		return userError{
			Title:       text,
			Explanation: "An unexpected error occurred during execution.",
			Action:      "Review the error output above or inspect logs with /debug.",
		}
	}
}

func (m Model) formatUserError(raw string, width int) string {
	info := parseUserError(raw)
	header := m.styles.Error.Render(m.glyph("×", "!") + " " + info.Title)
	if info.Explanation == "" && info.Action == "" {
		return header
	}
	var lines []string
	if info.Explanation != "" {
		lines = append(lines, m.styles.Muted.Render(info.Explanation))
	}
	if info.Action != "" {
		lines = append(lines, m.styles.Assistant.Render("next  ")+m.styles.Muted.Render(info.Action))
	}
	border := lipgloss.NormalBorder()
	if m.styles.Ascii {
		border = lipgloss.ASCIIBorder()
	}
	content := strings.Join(append([]string{header}, lines...), "\n")
	return m.styles.Text.Border(border, false, false, false, true).BorderForeground(m.styles.Error.GetForeground()).PaddingLeft(1).Width(max(24, min(width-2, 88))).Render(content)
}
