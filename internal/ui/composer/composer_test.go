package composer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/ui/composer"
)

func TestMentionCatalogAndContextSafety(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("small"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "credentials.json"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{0xff, 0x00}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "large.txt"), []byte(strings.Repeat("x", composer.MaxAttachmentBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, item := range composer.MentionCatalog(root) {
		if strings.Contains(item.Path, ".env") || strings.Contains(item.Path, "credentials") {
			t.Fatalf("sensitive file appeared in catalog: %q", item.Path)
		}
	}
	attachments, warnings := composer.LoadMentionAttachments(root, []string{"src/large.txt"})
	if len(warnings) != 0 || len(attachments) != 1 || !attachments[0].Truncated || len(attachments[0].Content) != composer.MaxAttachmentBytes {
		t.Fatalf("attachments=%#v warnings=%#v", attachments, warnings)
	}
	if _, warnings = composer.LoadMentionAttachments(root, []string{"../outside"}); len(warnings) == 0 {
		t.Fatal("outside workspace path was accepted")
	}
	if binaryAttachments, binaryWarnings := composer.LoadMentionAttachments(root, []string{"binary.dat"}); len(binaryAttachments) != 0 || len(binaryWarnings) == 0 {
		t.Fatalf("binary file was accepted: attachments=%#v warnings=%#v", binaryAttachments, binaryWarnings)
	}
	if got := composer.PromptWithAttachments("inspect", attachments); !strings.Contains(got, "Attached Context Files") || !strings.Contains(got, "src/large.txt") {
		t.Fatalf("attached prompt missing context: %q", got)
	}
}

func TestMentionTokensAndPaths(t *testing.T) {
	input := "Check @main.go and @\"pkg/sub folder/file.txt\" please"
	paths := composer.MentionPaths(input)
	if len(paths) != 2 {
		t.Fatalf("expected 2 mention paths, got %#v", paths)
	}
	if paths[0] != "main.go" || paths[1] != "pkg/sub folder/file.txt" {
		t.Fatalf("unexpected extracted paths: %#v", paths)
	}
}

func TestActiveMentionDetection(t *testing.T) {
	input := "Explain @src/comp"
	token, active := composer.ActiveMention(input, len([]rune(input)))
	if !active || token.Path != "src/comp" {
		t.Fatalf("expected active mention for 'src/comp', got active=%t token=%#v", active, token)
	}

	inactiveInput := "user@example.com"
	_, active = composer.ActiveMention(inactiveInput, len([]rune(inactiveInput)))
	if active {
		t.Fatal("email address was unexpectedly detected as active mention")
	}
}

func TestProjectMentions(t *testing.T) {
	input := "Review @main.go file"
	tokens := composer.MentionTokens(input)
	projection := composer.ProjectMentions(input, tokens)
	if len(projection.Runes) <= len([]rune(input)) {
		t.Fatalf("expected projected runes to be longer due to indicator badge, got %d runes", len(projection.Runes))
	}
	if !strings.Contains(string(projection.Runes), "[F] main.go") {
		t.Fatalf("expected projected mention tag in display string, got: %q", string(projection.Runes))
	}
}

func TestComputeVisualRowsMultilineAndTrailingNewline(t *testing.T) {
	// Single line
	rows := composer.ComputeVisualRows([]rune("hello"), 80)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for 'hello', got %d", len(rows))
	}

	// Line with trailing newline
	rows = composer.ComputeVisualRows([]rune("hello\n"), 80)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows for 'hello\\n', got %d: %#v", len(rows), rows)
	}
	if rows[1].Start != 6 || rows[1].End != 6 {
		t.Fatalf("expected empty second row at index 6, got %#v", rows[1])
	}

	// Two lines
	rows = composer.ComputeVisualRows([]rune("hello\nworld"), 80)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows for 'hello\\nworld', got %d: %#v", len(rows), rows)
	}

	// Consecutive newlines
	rows = composer.ComputeVisualRows([]rune("hello\n\nworld"), 80)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows for 'hello\\n\\nworld', got %d: %#v", len(rows), rows)
	}
}
