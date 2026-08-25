package logging

import "regexp"

var redactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_\-\.]+`),
	regexp.MustCompile(`\bsk-[a-zA-Z0-9_\-]{16,}\b`),
	regexp.MustCompile(`(?i)(api[_-]?key|password|secret|token|authorization)\s*[:=]\s*["']?([^"'\s,;]+)["']?`),
}

// Redact strips sensitive credentials, tokens, and authorization headers from log text.
func Redact(text string) string {
	for _, re := range redactions {
		text = re.ReplaceAllStringFunc(text, func(match string) string {
			if len(match) > 7 && (match[:7] == "Bearer " || match[:7] == "bearer ") {
				return "Bearer [REDACTED]"
			}
			if len(match) > 3 && match[:3] == "sk-" {
				return "sk-[REDACTED]"
			}
			submatches := re.FindStringSubmatch(match)
			if len(submatches) >= 3 {
				return submatches[1] + "=[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return text
}
