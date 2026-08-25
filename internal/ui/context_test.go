package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/composer"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

func TestMentionMenuFiltersTraversesAndSelects(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"README.md", "src/components/Button.ts", "src/controllers/UserController.ts", "src/main.go"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	model := newMentionTestModel(t, root)
	openMentionMenu(&model, "Explain @")
	if !model.mentionOpen {
		t.Fatal("@ did not open mention completion")
	}
	if selected, ok := model.mentionMenu.SelectedItem().(composer.MentionItem); !ok || strings.Contains(selected.Path, "/") {
		t.Fatalf("initial result = %#v, want top-level item", selected)
	}

	openMentionMenu(&model, "Explain @userctrl")
	selected, ok := model.mentionMenu.SelectedItem().(composer.MentionItem)
	if !ok || selected.Path != "src/controllers/UserController.ts" {
		t.Fatalf("fuzzy selection = %#v", selected)
	}
	if !model.selectMention() || model.mentionOpen || model.input.Value() != "Explain @src/controllers/UserController.ts " {
		t.Fatalf("selection value=%q open=%t", model.input.Value(), model.mentionOpen)
	}

	openMentionMenu(&model, "Review @src/")
	selected, ok = model.mentionMenu.SelectedItem().(composer.MentionItem)
	if !ok || selected.Path != "src/components" || selected.Label != "components" {
		t.Fatalf("folder traversal selection = %#v", selected)
	}
	if !model.selectMention() || !model.mentionOpen || model.input.Value() != "Review @src/components/" {
		t.Fatalf("folder drilldown value=%q open=%t", model.input.Value(), model.mentionOpen)
	}
	if selected, ok = model.mentionMenu.SelectedItem().(composer.MentionItem); !ok || selected.Path != "src/components/Button.ts" {
		t.Fatalf("nested item selection = %#v", selected)
	}

	openMentionMenu(&model, `Review @"src/controllers/`)
	selected, ok = model.mentionMenu.SelectedItem().(composer.MentionItem)
	if !ok || selected.Path != "src/controllers/UserController.ts" {
		t.Fatalf("quoted directory query selection = %#v", selected)
	}
	if !model.selectMention() || model.mentionOpen || model.input.Value() != `Review @src/controllers/UserController.ts ` {
		t.Fatalf("quoted selection value=%q open=%t", model.input.Value(), model.mentionOpen)
	}
}

func TestMentionMenuRenderingAndShortcuts(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"README.md", "src/main.go", "docs/index.md"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	model := newMentionTestModel(t, root)
	openMentionMenu(&model, "check @")
	view := model.mentionMenu.View()
	if !strings.Contains(view, "README.md") || !strings.Contains(view, "src/") {
		t.Fatalf("rendered mention menu:\n%s", view)
	}
	openMentionMenu(&model, "check @src/")
	if selected, ok := model.mentionMenu.SelectedItem().(composer.MentionItem); !ok || selected.Path != "src/main.go" {
		t.Fatalf("prefix filter item = %#v", selected)
	}

	noColor := model
	noColor.styles.Ascii = true
	noColor.mentionMenu.SetDelegate(composer.NewMentionDelegate(noColor.styles.CommandItem, noColor.styles.Accent, noColor.styles.Accent, noColor.styles.Accent))
	openMentionMenu(&noColor, "check @")
	if view := noColor.mentionMenu.View(); strings.Contains(view, "\x1b]1337;") || !strings.Contains(ansi.Strip(view), "[D] src/") {
		t.Fatalf("plain mention menu should use ASCII markers without graphics: %q", view)
	}
}

func TestMentionKeyNavigationAndDismissal(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"README.md", "docs/architecture.md", "src/main.go"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	model := newMentionTestModel(t, root)
	updated, _ := model.Update(tea.KeyPressMsg{Code: '@', Text: "@"})
	model = updated.(Model)
	if !model.mentionOpen || model.input.Value() != "@" {
		t.Fatalf("typing @ should open completion: value=%q open=%t", model.input.Value(), model.mentionOpen)
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	model = updated.(Model)
	if selected, ok := model.mentionMenu.SelectedItem().(composer.MentionItem); !ok || selected.Path != "src" {
		t.Fatalf("down selection = %#v", selected)
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	model = updated.(Model)
	if selected, ok := model.mentionMenu.SelectedItem().(composer.MentionItem); !ok || selected.Path != "docs" {
		t.Fatalf("up selection = %#v", selected)
	}

	model.input.SetValue("check @d")
	model.updateMentionMenu()
	if selected, ok := model.mentionMenu.SelectedItem().(composer.MentionItem); !ok || selected.Path != "docs" {
		t.Fatalf("filtered selection = %#v", selected)
	}

	model.input.SetValue("@R")
	model.updateMentionMenu()
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	model = updated.(Model)
	if model.mentionOpen || model.input.Value() != "@README.md " {
		t.Fatalf("tab completion = %q open=%t", model.input.Value(), model.mentionOpen)
	}

	model.input.SetValue("@")
	model.updateMentionMenu()
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	model = updated.(Model)
	if model.mentionOpen || model.input.Value() != "@ " {
		t.Fatalf("space should leave plain @: value=%q open=%t", model.input.Value(), model.mentionOpen)
	}

	model.input.SetValue("@")
	model.updateMentionMenu()
	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	model = updated.(Model)
	if model.mentionOpen || model.input.Value() != "@" {
		t.Fatalf("escape should leave @ untouched: value=%q open=%t", model.input.Value(), model.mentionOpen)
	}
}

func TestMentionResolutionReadsFilesRecursivelyAndDropsStaleReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "my note.txt"), []byte("note"), 0600); err != nil {
		t.Fatal(err)
	}
	prompt := `Review @src/main.go, then @src/ and @"docs/my note.txt".`
	paths := composer.MentionPaths(prompt)
	if got, want := strings.Join(paths, ","), "src/main.go,src/,docs/my note.txt"; got != want {
		t.Fatalf("mention paths = %q, want %q", got, want)
	}
	attachments, warnings := composer.LoadMentionAttachments(root, paths)
	if len(warnings) != 0 || len(attachments) != 2 || attachments[0].Path != "src/main.go" || attachments[1].Path != "docs/my note.txt" {
		t.Fatalf("resolved attachments=%#v warnings=%#v", attachments, warnings)
	}
	if attachments, warnings = composer.LoadMentionAttachments(root, composer.MentionPaths("Review only @src/main.go")); len(warnings) != 0 || len(attachments) != 1 || attachments[0].Path != "src/main.go" {
		t.Fatalf("deleted mention leaked into context: attachments=%#v warnings=%#v", attachments, warnings)
	}
}

func TestMentionFolderResolutionHonorsTheSharedContextCap(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
		path := filepath.Join(root, "docs", name)
		if err := os.WriteFile(path, []byte(strings.Repeat("x", composer.MaxAttachmentBytes)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	attachments, warnings := composer.LoadMentionAttachments(root, []string{"docs/"})
	if len(attachments) != composer.MaxContextBytes/composer.MaxAttachmentBytes || len(warnings) != 1 || warnings[0] != "attachment limit reached" {
		t.Fatalf("capped folder attachments=%d warnings=%#v", len(attachments), warnings)
	}
}

func TestMentionRendererStylesTokensAndKeepsMultilineCursorSafe(t *testing.T) {
	styled := Model{styles: rendering.Styles{
		Text:   lipgloss.NewStyle().Foreground(lipgloss.Color("7")),
		Accent: lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true),
	}}
	if got := styled.renderMentionText("@src/main.go"); !strings.Contains(got, "\x1b[") || ansi.Strip(got) != "[F] src/main.go" {
		t.Fatalf("styled mention has no ANSI styling: %q", got)
	}
	styled.styles.Ascii = true
	if got := styled.renderMentionText(`@"docs/my note.txt" and @src/`); got != "[F] docs/my note.txt and [D] src/" {
		t.Fatalf("ASCII mention = %q", got)
	}
	active := newMentionTestModel(t, t.TempDir())
	active.width, active.height = 80, 24
	active.layout()
	active.input.SetValue("review @main")
	active.input.CursorEnd()
	if view := ansi.Strip(active.mentionComposerView()); !strings.Contains(view, "@main") || strings.Contains(view, "[F] main") {
		t.Fatalf("active mention should stay editable: %q", view)
	}

	model := newMentionTestModel(t, t.TempDir())
	model.input.SetWidth(24)
	model.input.SetHeight(3)
	model.input.SetValue("see @src/main.go done\nnext @ui/ done")
	model.input.CursorEnd()
	if view := model.mentionComposerView(); !strings.Contains(ansi.Strip(view), "[F] src/main.go") || !strings.Contains(ansi.Strip(view), "[D] ui/") {
		t.Fatalf("multiline mention view = %q", view)
	}
	if model.input.Value() != "see @src/main.go done\nnext @ui/ done" {
		t.Fatalf("renderer changed raw input: %q", model.input.Value())
	}
	for _, row := range composerLayout(model.input).rows {
		if width := lipgloss.Width(string(composerLayout(model.input).projection.Runes[row.Start:row.End])); width > model.input.Width() {
			t.Fatalf("wrapped mention row is %d cells wide: %#v", width, row)
		}
	}
	model.width, model.height = 30, 20
	model.layout()
	model.input.SetValue("see @src/main.go and enough text to wrap")
	setComposerCursorOffset(&model.input, 0)
	_, before := composerMetrics(model.input)
	if !model.moveComposerVisualCursor(1) {
		t.Fatal("down did not move through a wrapped marker")
	}
	if _, after := composerMetrics(model.input); after != before+1 {
		t.Fatalf("visual cursor row=%d, want %d", after, before+1)
	}
	entry := model.renderEntry(0, transcriptEntry{kind: entryUser, content: "Review @src/main.go and @ui/"})
	if got := ansi.Strip(entry); !strings.Contains(got, "[F] src/main.go") || !strings.Contains(got, "[D] ui/") {
		t.Fatalf("user entry markers = %q", entry)
	}
}

func TestComposerMultilineAndCursorVisibility(t *testing.T) {
	model := newMentionTestModel(t, t.TempDir())
	model.width, model.height = 80, 24
	model.layout()

	// 1. Single line: cursor visible at end
	model.input.SetValue("hello")
	model.input.CursorEnd()
	view := model.composerView()
	if !strings.Contains(ansi.Strip(view), "hello") {
		t.Fatalf("expected 'hello' in composerView, got %q", view)
	}

	// 2. Multiline with newline: previous line and current line must both be visible
	model.input.SetValue("first line\nsecond line")
	model.input.CursorEnd()
	model.resizeComposer()
	view = model.composerView()
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "first line") || !strings.Contains(stripped, "second line") {
		t.Fatalf("expected both lines visible in multiline input, got %q", stripped)
	}

	// 3. Mention added: cursor remains present in the view
	model.input.SetValue("review @src/main.go done")
	model.input.CursorEnd()
	model.resizeComposer()
	view = model.composerView()
	stripped = ansi.Strip(view)
	if !strings.Contains(stripped, "[F] src/main.go") || !strings.Contains(stripped, "done") {
		t.Fatalf("expected mention badge and text, got %q", stripped)
	}
}

func TestComposerMarkdownRendering(t *testing.T) {
	model := newMentionTestModel(t, t.TempDir())
	model.width, model.height = 80, 24
	model.layout()

	model.input.SetValue("check `code` and **bold** and *italic*")
	view := model.composerView()
	stripped := ansi.Strip(view)
	if !strings.Contains(stripped, "`code`") || !strings.Contains(stripped, "**bold**") || !strings.Contains(stripped, "*italic*") {
		t.Fatalf("expected markdown text in composer, got %q", stripped)
	}
}

func newMentionTestModel(t *testing.T, root string) Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	model := newTestModel(api.Session{ID: "mentions", Name: "Mentions"}, ctx, cancel)
	model.workspace = root
	model.input.Focus()
	return model
}

func openMentionMenu(model *Model, value string) {
	model.input.SetValue(value)
	model.input.CursorEnd()
	_, _ = model.input.Update(nil)
	_ = model.updateMentionMenu()
}
