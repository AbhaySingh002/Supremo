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
	text, muted, focus, link, code, surface := "#E8EAF0", "#A7ADB8", "#E8B84A", "#66B8D4", "#E29A61", "#171C24"
	bold := true
	margin := uint(0)
	style.Document.Margin = &margin
	style.Document.Color = &text
	style.Paragraph.Color = &text
	style.Heading.Color, style.Heading.Bold = &text, &bold
	style.H1 = style.Heading
	style.H1.Prefix, style.H1.Suffix, style.H1.BackgroundColor = "", "", nil
	style.H2.Prefix, style.H2.Color = "", &focus
	style.H3.Prefix, style.H3.Color = "", &text
	style.H4.Prefix, style.H4.Color = "", &text
	style.H5.Prefix, style.H5.Color = "", &text
	style.H6.Prefix, style.H6.Color = "", &muted
	style.Item.Color, style.Enumeration.Color = &text, &text
	style.Link.Color, style.LinkText.Color = &link, &link
	style.Code.Color, style.Code.BackgroundColor = &code, &surface
	style.CodeBlock.Color = &text
	style.CodeBlock.Margin = &margin
	style.BlockQuote.Color = &muted
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
