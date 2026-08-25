package rendering

import (
	"strings"
	"testing"
)

func TestHighlightDiffDirect(t *testing.T) {
	diff := `--- a/main.go
+++ b/main.go
@@ -1,3 +1,3 @@
 package main
-func old() {}
+func new() {}
`
	rendered := HighlightDiff(diff, false)
	if rendered == "" {
		t.Fatal("expected non-empty highlighted diff output")
	}
	if strings.Contains(rendered, "```diff") || strings.Contains(rendered, "```") {
		t.Fatalf("expected direct Chroma highlighting without markdown code fences, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "func new()") {
		t.Fatalf("expected diff content to be preserved, got:\n%s", rendered)
	}

	// ASCII / NO_COLOR mode should return plain diff without ANSI escapes
	plain := HighlightDiff(diff, true)
	if plain != diff {
		t.Fatalf("expected plain diff in ascii mode, got:\n%s", plain)
	}
}

func TestHighlightSourceLanguages(t *testing.T) {
	goCode := `package main

import "fmt"

func main() {
	fmt.Println("hello world")
}
`
	renderedGo := HighlightSource(goCode, "go", "main.go", false)
	if renderedGo == "" || strings.Contains(renderedGo, "```") {
		t.Fatalf("expected clean highlighted Go source, got:\n%s", renderedGo)
	}

	jsonCode := `{"key": "value", "count": 42}`
	renderedJSON := HighlightSource(jsonCode, "json", "data.json", false)
	if renderedJSON == "" || strings.Contains(renderedJSON, "```") {
		t.Fatalf("expected clean highlighted JSON, got:\n%s", renderedJSON)
	}

	// Unknown extension / plain text fallback
	plainText := "arbitrary plain text with no lexer"
	renderedPlain := HighlightSource(plainText, "", "notes.txt", false)
	if !strings.Contains(renderedPlain, "arbitrary plain text") {
		t.Fatalf("expected fallback plain text, got:\n%s", renderedPlain)
	}

	// Empty source
	if empty := HighlightSource("", "", "", false); empty != "" {
		t.Fatalf("expected empty result for empty source, got %q", empty)
	}
}
