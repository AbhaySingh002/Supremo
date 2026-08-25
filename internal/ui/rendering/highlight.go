package rendering

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
)

// HighlightDiff formats a unified git diff directly using Chroma's diff lexer,
// avoiding Markdown Goldmark parsing and AST construction overhead.
func HighlightDiff(diff string, ascii bool) string {
	return HighlightSource(diff, "diff", "", ascii)
}

// HighlightSource formats source code directly using Chroma's ANSI terminal formatter.
// Lexer selection follows: explicit language -> filename match -> content analysis -> fallback.
func HighlightSource(source, language, filename string, ascii bool) string {
	if ascii || strings.TrimSpace(source) == "" {
		return source
	}

	var lexer chroma.Lexer
	if language != "" {
		lexer = lexers.Get(language)
	}
	if lexer == nil && filename != "" {
		lexer = lexers.Match(filename)
	}
	if lexer == nil && source != "" {
		lexer = lexers.Analyse(source)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := chromastyles.Get("github-dark")
	if style == nil {
		style = chromastyles.Fallback
	}

	formatter := formatters.TTY256
	if formatter == nil {
		formatter = formatters.Fallback
	}

	iterator, err := lexer.Tokenise(nil, source)
	if err != nil {
		return source
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return source
	}

	return strings.TrimSpace(buf.String())
}
