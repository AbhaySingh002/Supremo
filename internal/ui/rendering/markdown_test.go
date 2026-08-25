package rendering_test

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

func TestGlamourRendererCacheReuse(t *testing.T) {
	rendering.ClearGlamourCache()

	r1, err := rendering.CachedGlamourRenderer(80, 76)
	if err != nil {
		t.Fatalf("failed to create glamour renderer: %v", err)
	}

	r2, err := rendering.CachedGlamourRenderer(80, 76)
	if err != nil {
		t.Fatalf("failed to retrieve glamour renderer: %v", err)
	}

	// Pointers should be identical (cached instance)
	if r1 != r2 {
		t.Fatal("expected CachedGlamourRenderer to return the same instance for identical dimensions")
	}

	// Different width creates new entry
	r3, err := rendering.CachedGlamourRenderer(100, 96)
	if err != nil {
		t.Fatalf("failed to create second glamour renderer: %v", err)
	}
	if r1 == r3 {
		t.Fatal("expected different renderer instance for different width")
	}
}

func TestRenderMarkdownContentWithTermtexMath(t *testing.T) {
	input := "The time complexity is $O(n \\log n)$ and $N \\le 500$."
	rendered, err := rendering.RenderMarkdownContent(input, 80, 80)
	if err != nil {
		t.Fatalf("RenderMarkdownContent failed: %v", err)
	}
	plain := ansi.Strip(rendered)
	// termtex should expand LaTeX math and glamour formats it
	if !strings.Contains(plain, "O(n") || !strings.Contains(plain, "500") {
		t.Fatalf("expected rendered math output, got:\n%s", plain)
	}
	// Raw delimiters $...$ should have been processed by termtex
	if strings.Contains(plain, "$O(n \\log n)$") {
		t.Fatalf("expected $...$ to be rendered by termtex, got raw text:\n%s", plain)
	}
}

func TestRenderMarkdownGlamourV2(t *testing.T) {
	rendered, err := rendering.RenderMarkdownContent("# Heading\n\n`code`", 80, 80)
	if err != nil {
		t.Fatalf("RenderMarkdownContent: %v", err)
	}
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "Heading") || !strings.Contains(plain, "code") {
		t.Fatalf("expected glamour v2 markdown output, got:\n%s", plain)
	}
}

func TestRenderMarkdownMalformedMath(t *testing.T) {
	// Incomplete streaming math or broken LaTeX delimiters
	malformed := "Streaming chunk with unclosed math $O(n \\log and more text"
	rendered, err := rendering.RenderMarkdownContent(malformed, 80, 80)
	if err != nil {
		t.Fatalf("expected graceful rendering on malformed math, got error: %v", err)
	}
	if !strings.Contains(rendered, "Streaming chunk") {
		t.Fatalf("expected preserved content, got:\n%s", rendered)
	}
}
