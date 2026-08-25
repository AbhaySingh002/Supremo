package composer

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// MentionToken represents a parsed @mention token span in a text string.
type MentionToken struct {
	Start int
	End   int
	Path  string
}

// ActiveMention determines if the cursor is currently inside or immediately after an @mention query.
func ActiveMention(value string, cursor int) (MentionToken, bool) {
	runes := []rune(value)
	cursor = min(max(0, cursor), len(runes))
	start := cursor
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	if start == cursor || runes[start] != '@' || (start > 0 && !unicode.IsSpace(runes[start-1])) {
		return MentionToken{}, false
	}
	path := strings.TrimPrefix(string(runes[start+1:cursor]), "\"")
	return MentionToken{Start: start, End: cursor, Path: path}, true
}

// MentionTokens parses all mention spans from a string.
func MentionTokens(value string) []MentionToken {
	runes := []rune(value)
	tokens := make([]MentionToken, 0)
	for index := 0; index < len(runes); index++ {
		if runes[index] != '@' || index > 0 && !unicode.IsSpace(runes[index-1]) {
			continue
		}
		start := index
		index++
		if index < len(runes) && runes[index] == '"' {
			index++
			pathStart := index
			for index < len(runes) && runes[index] != '"' {
				index++
			}
			if index < len(runes) && pathStart < index {
				tokens = append(tokens, MentionToken{Start: start, End: index + 1, Path: string(runes[pathStart:index])})
			}
			continue
		}
		pathStart := index
		for index < len(runes) && !unicode.IsSpace(runes[index]) {
			index++
		}
		pathEnd := index
		for pathEnd > pathStart && strings.ContainsRune(",.;:!?)]}", runes[pathEnd-1]) {
			pathEnd--
		}
		if pathStart < pathEnd {
			tokens = append(tokens, MentionToken{Start: start, End: pathEnd, Path: string(runes[pathStart:pathEnd])})
		}
		index--
	}
	return tokens
}

// MentionReference formats a path as a mention string.
func MentionReference(path string, isDir bool) string {
	if isDir && !strings.HasSuffix(path, "/") {
		path += "/"
	}
	if strings.ContainsAny(path, " \t\n") {
		return `@"` + path + `"`
	}
	return "@" + path
}

// MentionProjection expands stored references only for display.
type MentionProjection struct {
	Runes        []rune
	Mention      []bool
	RawToDisplay []int
	DisplayToRaw []int
}

// ProjectMentions computes the visual display runes for mention tags.
func ProjectMentions(value string, spans []MentionToken) MentionProjection {
	raw := []rune(value)
	projection := MentionProjection{
		RawToDisplay: make([]int, len(raw)+1),
		DisplayToRaw: []int{0},
	}
	position := 0
	appendRaw := func(index int) {
		projection.RawToDisplay[index] = len(projection.Runes)
		projection.Runes = append(projection.Runes, raw[index])
		projection.Mention = append(projection.Mention, false)
		projection.RawToDisplay[index+1] = len(projection.Runes)
		projection.DisplayToRaw = append(projection.DisplayToRaw, index+1)
	}
	for _, span := range spans {
		if span.Start < position || span.Start < 0 || span.End > len(raw) || span.End <= span.Start {
			continue
		}
		for index := position; index < span.Start; index++ {
			appendRaw(index)
		}

		projection.RawToDisplay[span.Start] = len(projection.Runes)
		for _, char := range MentionMarker(strings.HasSuffix(span.Path, "/")) + " " {
			projection.Runes = append(projection.Runes, char)
			projection.Mention = append(projection.Mention, true)
			projection.DisplayToRaw = append(projection.DisplayToRaw, span.Start)
		}
		pathStart := span.Start + 1
		if pathStart < len(raw) && raw[pathStart] == '"' {
			pathStart++
		}
		projection.DisplayToRaw[len(projection.DisplayToRaw)-1] = pathStart
		for index := span.Start + 1; index <= pathStart; index++ {
			projection.RawToDisplay[index] = len(projection.Runes)
		}
		pathEnd := min(span.End, pathStart+len([]rune(span.Path)))
		for index := pathStart; index < pathEnd; index++ {
			projection.RawToDisplay[index] = len(projection.Runes)
			projection.Runes = append(projection.Runes, raw[index])
			projection.Mention = append(projection.Mention, true)
			projection.RawToDisplay[index+1] = len(projection.Runes)
			projection.DisplayToRaw = append(projection.DisplayToRaw, index+1)
		}
		for index := pathEnd + 1; index <= span.End; index++ {
			projection.RawToDisplay[index] = len(projection.Runes)
		}
		position = span.End
	}
	for index := position; index < len(raw); index++ {
		appendRaw(index)
	}
	projection.RawToDisplay[len(raw)] = len(projection.Runes)
	return projection
}

// VisualRow represents the start and end rune indices of a visual display row.
type VisualRow struct {
	Start int
	End   int
}

// ComputeVisualRows breaks projected runes into soft-wrapped visual rows within maxWidth columns.
// It wraps at word/space boundaries where possible and runs in linear O(N) time with zero
// throwaway textarea allocations.
func ComputeVisualRows(runes []rune, maxWidth int) []VisualRow {
	if maxWidth <= 0 {
		maxWidth = 80
	}
	if len(runes) == 0 {
		return []VisualRow{{Start: 0, End: 0}}
	}
	var rows []VisualRow
	lineStart := 0
	for lineStart < len(runes) {
		lineEnd := lineStart
		for lineEnd < len(runes) && runes[lineEnd] != '\n' {
			lineEnd++
		}

		if lineStart == lineEnd {
			rows = append(rows, VisualRow{Start: lineStart, End: lineEnd})
			lineStart = lineEnd + 1
			continue
		}

		subStart := lineStart
		for subStart < lineEnd {
			subEnd := subStart
			currentWidth := 0
			lastBreak := -1
			for subEnd < lineEnd {
				w := ansi.StringWidth(string(runes[subEnd]))
				if currentWidth+w > maxWidth && subEnd > subStart {
					if lastBreak > subStart {
						subEnd = lastBreak
					}
					break
				}
				if unicode.IsSpace(runes[subEnd]) {
					lastBreak = subEnd + 1
				}
				currentWidth += w
				subEnd++
			}
			rows = append(rows, VisualRow{Start: subStart, End: subEnd})
			subStart = subEnd
		}

		lineStart = lineEnd + 1
	}
	if len(runes) > 0 && runes[len(runes)-1] == '\n' {
		rows = append(rows, VisualRow{Start: len(runes), End: len(runes)})
	}
	if len(rows) == 0 {
		rows = append(rows, VisualRow{Start: 0, End: 0})
	}
	return rows
}
