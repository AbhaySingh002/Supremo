// Package composer handles workspace context attachments, mention suggestions,
// and prompt layout projection for Supremo's interactive composer.
package composer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	MaxContextAttachments = 12
	MaxAttachmentBytes    = 32 << 10
	MaxContextBytes       = 128 << 10
	MentionMarkerCells    = 3
)

// ContextAttachment holds file content referenced via @mentions in user prompts.
type ContextAttachment struct {
	Path      string
	Content   string
	Truncated bool
}

// MentionItem represents a file or folder suggestion for @mention autocompletion.
type MentionItem struct {
	Path  string
	IsDir bool
	Label string
}

func (i MentionItem) Title() string {
	if i.IsDir {
		return i.Label + "/"
	}
	return i.Label
}

func (i MentionItem) Description() string {
	if i.IsDir {
		return "folder reference"
	}
	return "file reference"
}

func (i MentionItem) FilterValue() string { return i.Title() }

// MentionDelegate renders file/folder autocomplete options with custom styling.
type MentionDelegate struct {
	list.DefaultDelegate
	MarkerStyle   lipgloss.Style
	NormalStyle   lipgloss.Style
	SelectedStyle lipgloss.Style
	FilterStyle   lipgloss.Style
}

// NewMentionDelegate creates a themed delegate for the mention suggestions menu.
func NewMentionDelegate(normal, selected, marker, filter lipgloss.Style) MentionDelegate {
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.ShowDescription = false
	delegate.Styles.FilterMatch = filter

	return MentionDelegate{
		DefaultDelegate: delegate,
		MarkerStyle:     marker,
		NormalStyle:     normal.Inline(true),
		SelectedStyle:   selected.Inline(true),
		FilterStyle:     filter,
	}
}

func (d MentionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	mention, ok := item.(MentionItem)
	if !ok {
		d.DefaultDelegate.Render(w, m, index, item)
		return
	}

	title := mention.Title()
	emptyFilter := m.FilterState() == list.Filtering && m.FilterValue() == ""
	isFiltered := m.FilterState() == list.Filtering || m.FilterState() == list.FilterApplied
	selected := index == m.Index() && m.FilterState() != list.Filtering
	titleStyle := d.NormalStyle
	marker := "  "
	if emptyFilter {
		titleStyle = d.Styles.DimmedTitle.Inline(true)
	} else if selected {
		titleStyle = d.SelectedStyle
		marker = d.MarkerStyle.Render("> ")
	}
	icon := MentionMarker(mention.IsDir)
	textWidth := max(0, m.Width()-2-MentionMarkerCells-1)
	title = ansi.TruncateWc(title, textWidth, "…")
	if isFiltered && !emptyFilter {
		unmatched := titleStyle.Inline(true)
		matched := unmatched.Inherit(d.FilterStyle)
		title = lipgloss.StyleRunes(title, m.MatchesForItem(index), matched, unmatched)
	}
	fmt.Fprintf(w, "%s%s %s", marker, icon, titleStyle.Render(title)) //nolint: errcheck
}

// MentionMarker returns a compact type indicator for files and directories.
func MentionMarker(folder bool) string {
	if folder {
		return "[D]"
	}
	return "[F]"
}

// MentionCatalog walks the workspace root and builds a sorted index of eligible files and directories.
func MentionCatalog(root string) []MentionItem {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	var items []MentionItem
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || SkipAttachmentPath(rel, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() && !entry.IsDir() {
			return nil
		}
		items = append(items, MentionItem{Path: filepath.ToSlash(rel), IsDir: entry.IsDir()})
		return nil
	})
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir
		}
		return items[i].Path < items[j].Path
	})
	return items
}

// MentionSuggestions filters the catalog for a given query prefix.
func MentionSuggestions(catalog []MentionItem, query string) ([]list.Item, string) {
	scope, filter := MentionScope(catalog, query)
	items := make([]list.Item, 0, len(catalog))
	for _, item := range catalog {
		label := item.Path
		if scope == "" {
			if query == "" && strings.Contains(item.Path, "/") {
				continue
			}
		} else {
			parent := filepath.ToSlash(filepath.Dir(item.Path))
			if parent != scope {
				continue
			}
			label = strings.TrimPrefix(item.Path, scope+"/")
		}
		item.Label = label
		items = append(items, item)
	}
	return items, filter
}

func MentionScope(catalog []MentionItem, query string) (string, string) {
	query = filepath.ToSlash(strings.TrimPrefix(query, "@"))
	if query == "" {
		return "", ""
	}
	best := ""
	for _, item := range catalog {
		if !item.IsDir {
			continue
		}
		if query == item.Path || strings.HasPrefix(query, item.Path+"/") {
			if len(item.Path) > len(best) {
				best = item.Path
			}
		}
	}
	if best != "" {
		if query == best {
			return best, ""
		}
		return best, strings.TrimPrefix(query, best+"/")
	}
	if strings.Contains(query, "/") {
		dir := filepath.ToSlash(filepath.Dir(query))
		if dir == "." {
			dir = ""
		}
		return dir, filepath.Base(query)
	}
	return "", query
}

// MentionPaths extracts @-prefixed paths from user input.
func MentionPaths(value string) []string {
	seen := make(map[string]bool)
	paths := make([]string, 0)
	for _, token := range MentionTokens(value) {
		if token.Path == "" || seen[token.Path] {
			continue
		}
		seen[token.Path] = true
		paths = append(paths, token.Path)
	}
	return paths
}

// LoadMentionAttachments reads and bounds file contents referenced by paths.
func LoadMentionAttachments(root string, paths []string) ([]ContextAttachment, []string) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, []string{"cannot resolve workspace path: " + err.Error()}
	}

	var attachments []ContextAttachment
	var warnings []string
	totalBytes := 0
	seen := make(map[string]bool)

	loadFile := func(relPath, fullPath string) {
		clean := filepath.ToSlash(relPath)
		if seen[clean] {
			return
		}
		seen[clean] = true
		if len(attachments) >= MaxContextAttachments {
			warnings = append(warnings, fmt.Sprintf("omitted %s: maximum attachment count reached (%d)", relPath, MaxContextAttachments))
			return
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot read %s: %s", relPath, err.Error()))
			return
		}
		if !utf8.Valid(data) {
			warnings = append(warnings, fmt.Sprintf("omitted %s: binary file", relPath))
			return
		}

		truncated := false
		if len(data) > MaxAttachmentBytes {
			data = data[:MaxAttachmentBytes]
			truncated = true
		}
		if totalBytes+len(data) > MaxContextBytes {
			remaining := MaxContextBytes - totalBytes
			if remaining <= 0 {
				warnings = append(warnings, "attachment limit reached")
				return
			}
			data = data[:remaining]
			truncated = true
		}
		totalBytes += len(data)
		attachments = append(attachments, ContextAttachment{
			Path:      filepath.ToSlash(relPath),
			Content:   string(data),
			Truncated: truncated,
		})
	}

	for _, rel := range paths {
		cleanRel := filepath.Clean(filepath.FromSlash(rel))
		if strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
			warnings = append(warnings, fmt.Sprintf("omitted %s: outside workspace", rel))
			continue
		}
		full := filepath.Join(root, cleanRel)
		info, err := os.Stat(full)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot read %s: %s", rel, err.Error()))
			continue
		}
		if info.IsDir() {
			var dirFiles []string
			_ = filepath.WalkDir(full, func(p string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				childRel, relErr := filepath.Rel(root, p)
				if relErr != nil || SkipAttachmentPath(childRel, entry.IsDir()) {
					if entry.IsDir() && p != full {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.Type().IsRegular() {
					dirFiles = append(dirFiles, childRel)
				}
				return nil
			})
			sort.Strings(dirFiles)
			for _, childRel := range dirFiles {
				loadFile(childRel, filepath.Join(root, childRel))
			}
			continue
		}
		loadFile(rel, full)
	}
	return attachments, warnings
}

// PromptWithAttachments bundles the attachments into the prompt payload.
func PromptWithAttachments(prompt string, attachments []ContextAttachment) string {
	if len(attachments) == 0 {
		return prompt
	}
	var out strings.Builder
	out.WriteString(prompt)
	out.WriteString("\n\n---\n### Attached Context Files\n")
	for _, att := range attachments {
		out.WriteString(fmt.Sprintf("\n#### `%s`", att.Path))
		if att.Truncated {
			out.WriteString(" (truncated)")
		}
		out.WriteString("\n```\n")
		out.WriteString(att.Content)
		if !strings.HasSuffix(att.Content, "\n") {
			out.WriteString("\n")
		}
		out.WriteString("```\n")
	}
	return out.String()
}

// CleanUserPrompt separates the user's actual typed prompt from attached context files.
func CleanUserPrompt(content string) (string, []string) {
	delimiter := "\n\n---\n### Attached Context Files\n"
	parts := strings.Split(content, delimiter)
	if len(parts) <= 1 {
		return content, nil
	}
	userText := strings.TrimSpace(parts[0])
	var files []string
	lines := strings.Split(parts[1], "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#### `") {
			fileName := strings.TrimPrefix(line, "#### `")
			if idx := strings.Index(fileName, "`"); idx >= 0 {
				fileName = fileName[:idx]
			}
			if fileName != "" {
				files = append(files, fileName)
			}
		}
	}
	return userText, files
}

// SkipAttachmentPath determines if a file or directory should be ignored from indexing.
func SkipAttachmentPath(path string, isDir bool) bool {
	clean := filepath.ToSlash(path)
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
		switch part {
		case "node_modules", "vendor", "dist", "build", "target", "out", "coverage":
			return true
		}
	}
	base := filepath.Base(clean)
	if strings.Contains(strings.ToLower(base), "credentials") || strings.Contains(strings.ToLower(base), "secret") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".pem") {
		return true
	}
	return false
}

// IsTextContent checks whether a string contains only valid UTF-8 text and printable/whitespace runes.
func IsTextContent(content string) bool {
	if !utf8.ValidString(content) {
		return false
	}
	for _, r := range content {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}
