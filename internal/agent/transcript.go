package agent

import (
	"fmt"
	"strings"
	"unicode"
)

const toolSnippetTokens = 1_000

// truncateObservationBounded formats large outputs with head (60%), tail (40%),
// line/byte omission statistics, and an artifact reference hash so failure summaries
// at the end of compiler/test outputs are preserved in context.
func truncateObservationBounded(text string, budget int, artifactID string) string {
	if budget <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	maxRunes := budget * 4
	if len(runes) <= maxRunes {
		return text
	}

	headRunes := (maxRunes * 6) / 10
	tailRunes := (maxRunes * 4) / 10
	if headRunes+tailRunes > len(runes) {
		return text
	}

	head := string(runes[:headRunes])
	tail := string(runes[len(runes)-tailRunes:])

	totalLines := strings.Count(text, "\n") + 1
	totalBytes := len(text)
	headLines := strings.Count(head, "\n")
	tailLines := strings.Count(tail, "\n")
	omittedLines := totalLines - (headLines + tailLines)
	if omittedLines < 0 {
		omittedLines = 0
	}
	omittedBytes := totalBytes - (len(head) + len(tail))
	if omittedBytes < 0 {
		omittedBytes = 0
	}

	citation := ""
	if artifactID != "" {
		citation = fmt.Sprintf("; full output archived as artifact %s", artifactID)
	}

	return fmt.Sprintf("%s\n\n[... Omitted %d lines (%d bytes)%s ...]\n\n%s", head, omittedLines, omittedBytes, citation, tail)
}

func safeSessionID(id string) string {
	var safe strings.Builder
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('_')
		}
	}
	if safe.Len() == 0 {
		return "session"
	}
	return safe.String()
}
