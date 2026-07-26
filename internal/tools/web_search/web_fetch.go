package web_search

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

const webFetchTimeout = 30 * time.Second

var (
	nonTextHTML = regexp.MustCompile(`(?is)<(?:script|style)[^>]*>.*?</(?:script|style)\s*>`)
	htmlTags    = regexp.MustCompile(`(?s)<[^>]*>`)
)

type WebFetchInput struct {
	URL string `json:"url"`
}

type WebFetchOutput struct {
	URL         string `json:"url"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type,omitempty"`
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// WebFetch retrieves an HTTP(S) page for the agent to inspect.
type WebFetch struct{}

func (t *WebFetch) Name() string { return "web_fetch" }

func (t *WebFetch) Description() string {
	return "Fetches an HTTP(S) page and returns up to 1 MiB of readable page text, status, and content type."
}

func (t *WebFetch) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "HTTP or HTTPS URL to fetch",
			},
		},
		"required": []string{"url"},
	}
}

func (t *WebFetch) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var parsed WebFetchInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	urlValue, err := url.ParseRequestURI(parsed.URL)
	if err != nil || urlValue.Host == "" || (urlValue.Scheme != "http" && urlValue.Scheme != "https") {
		return tools.BuildToolResult(false, "URL must be a valid HTTP(S) URL", nil), nil
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, urlValue.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := (&http.Client{Timeout: webFetchTimeout}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, tools.MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read page: %w", err)
	}
	truncated := int64(len(content)) > tools.MaxFileBytes
	if truncated {
		content = content[:tools.MaxFileBytes]
	}
	contentType := response.Header.Get("Content-Type")
	pageContent := string(content)
	if strings.HasPrefix(strings.ToLower(contentType), "text/html") {
		pageContent = htmlText(pageContent)
	}
	output := WebFetchOutput{
		URL:         response.Request.URL.String(),
		StatusCode:  response.StatusCode,
		ContentType: contentType,
		Content:     pageContent,
		Truncated:   truncated,
	}
	data, err := tools.SerializeOutput(output)
	if err != nil {
		return nil, err
	}
	return tools.BuildToolResult(response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices, response.Status, data), nil
}

func htmlText(content string) string {
	// ponytail: regex cleanup handles ordinary pages; use an HTML tokenizer if malformed markup needs exact recovery.
	content = nonTextHTML.ReplaceAllString(content, " ")
	content = htmlTags.ReplaceAllString(content, " ")
	return strings.Join(strings.Fields(html.UnescapeString(content)), " ")
}
