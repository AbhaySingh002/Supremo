package rendering

import (
	"fmt"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"github.com/doug/termtex"
)

type glamourRendererCache struct {
	mu        sync.RWMutex
	renderers map[string]*glamour.TermRenderer
}

var globalGlamourCache = &glamourRendererCache{
	renderers: make(map[string]*glamour.TermRenderer),
}

// CachedGlamourRenderer returns a reusable glamour TermRenderer for the given
// width and word-wrap configuration, avoiding expensive parser/theme allocations.
func CachedGlamourRenderer(width, wordWrap int) (*glamour.TermRenderer, error) {
	key := fmt.Sprintf("%d:%d", width, wordWrap)
	globalGlamourCache.mu.RLock()
	if r, ok := globalGlamourCache.renderers[key]; ok {
		globalGlamourCache.mu.RUnlock()
		return r, nil
	}
	globalGlamourCache.mu.RUnlock()

	globalGlamourCache.mu.Lock()
	defer globalGlamourCache.mu.Unlock()
	if r, ok := globalGlamourCache.renderers[key]; ok {
		return r, nil
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(supremoMarkdownStyle()),
		glamour.WithWordWrap(wordWrap),
	)
	if err != nil {
		return nil, err
	}
	globalGlamourCache.renderers[key] = renderer
	return renderer, nil
}

func supremoMarkdownStyle() ansi.StyleConfig {
	style := glamourstyles.DarkStyleConfig

	text := "#E2E8F0"
	muted := "#64748B"
	h1Color := "#E8B84A"
	h2Color := "#38BDF8"
	h3Color := "#F8FAFC"
	codeColor := "#E29A61"
	codeBg := "#171C24"
	hrColor := "#334155"
	bulletColor := "#38BDF8"
	quoteBarColor := "#E8B84A"
	linkCol := "#38BDF8"
	tableBorder := "#334155"

	bold := true
	italic := true
	margin := uint(0)

	style.Document.Margin = &margin
	style.Document.Color = &text
	style.Paragraph.Color = &text

	// Headings
	style.Heading.Bold = &bold
	style.Heading.Color = &text

	style.H1.Color = &h1Color
	style.H1.Bold = &bold
	style.H1.Prefix = "▌ "
	style.H1.Suffix = ""
	style.H1.BackgroundColor = nil

	style.H2.Color = &h2Color
	style.H2.Bold = &bold
	style.H2.Prefix = "▌ "
	style.H2.Suffix = ""

	style.H3.Color = &h3Color
	style.H3.Bold = &bold
	style.H3.Prefix = "▎ "
	style.H3.Suffix = ""

	style.H4.Color = &text
	style.H4.Bold = &bold
	style.H4.Prefix = "· "

	style.H5.Color = &muted
	style.H5.Prefix = "· "

	style.H6.Color = &muted
	style.H6.Prefix = "· "

	// Inline code: warm orange text with visible surface pill and padding
	style.Code.Color = &codeColor
	style.Code.BackgroundColor = &codeBg
	style.Code.Prefix = " "
	style.Code.Suffix = " "

	// Code blocks with syntax highlighting
	style.CodeBlock.Margin = &margin
	style.CodeBlock.Theme = "nord"

	// Horizontal Rule: subtle smooth line
	style.HorizontalRule.Color = &hrColor
	style.HorizontalRule.Format = "\n────────────────────────────────────────\n"

	// Lists
	style.Item.Color = &bulletColor
	style.Item.BlockPrefix = "• "
	style.Enumeration.Color = &h2Color

	// Blockquote: vertical gold bar
	style.BlockQuote.Color = &quoteBarColor
	style.BlockQuote.Italic = &italic
	style.BlockQuote.IndentToken = func() *string { s := "▌ "; return &s }()

	// Links
	style.Link.Color = &linkCol
	style.LinkText.Color = &linkCol
	style.LinkText.Bold = &bold

	// Table borders
	style.Table.Color = &tableBorder
	style.Table.CenterSeparator = func() *string { s := "┼"; return &s }()
	style.Table.ColumnSeparator = func() *string { s := "│"; return &s }()
	style.Table.RowSeparator = func() *string { s := "─"; return &s }()

	return style
}

// ClearGlamourCache invalidates cached renderers when theme or profile changes.
func ClearGlamourCache() {
	globalGlamourCache.mu.Lock()
	defer globalGlamourCache.mu.Unlock()
	globalGlamourCache.renderers = make(map[string]*glamour.TermRenderer)
}

// RenderMarkdownContent renders markdown through the termtex -> glamour pipeline.
func RenderMarkdownContent(content string, width, wordWrap int) (string, error) {
	if strings.Contains(content, "$") {
		content = safeExpandMath(content)
	}
	renderer, err := CachedGlamourRenderer(width, wordWrap)
	if err != nil {
		return content, err
	}
	rendered, err := renderer.Render(content)
	if err != nil {
		return content, err
	}
	return strings.TrimSpace(rendered), nil
}

func safeExpandMath(content string) (expanded string) {
	defer func() {
		if r := recover(); r != nil {
			expanded = content
		}
	}()
	return termtex.Expand(content, termtex.Style{})
}
