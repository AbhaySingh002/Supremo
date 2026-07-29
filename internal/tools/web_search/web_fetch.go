package web_search

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

const (
	defaultWebFetchTimeout = 12 * time.Second
	maxWebFetchBytes       = 5 << 20
	webFetchUserAgent      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"
)

type WebFetchInput struct {
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type WebFetchOutput struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type,omitempty"`
	Content     string `json:"content"`
}

// WebFetch retrieves an HTTP(S) page and returns its primary content as Markdown.
type WebFetch struct{}

func (t *WebFetch) Name() string { return "web_fetch" }

func (t *WebFetch) Description() string {
	return "Fetches content from a specified URL, extracts the primary readable content, and converts it into clean Markdown suitable for LLM processing."
}

func (t *WebFetch) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The HTTP or HTTPS URL to fetch",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Request timeout in seconds (default: 12)",
				"default":     12,
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
	target, err := url.ParseRequestURI(strings.TrimSpace(parsed.URL))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return tools.BuildToolResult(false, "URL must be a valid HTTP(S) URL", nil), nil
	}
	if parsed.TimeoutSeconds < 0 || int64(parsed.TimeoutSeconds) > (1<<63-1)/int64(time.Second) {
		return tools.BuildToolResult(false, "timeout_seconds must be a positive integer", nil), nil
	}
	timeout := defaultWebFetchTimeout
	if parsed.TimeoutSeconds > 0 {
		timeout = time.Duration(parsed.TimeoutSeconds) * time.Second
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create web request: %w", err)
	}
	request.Header.Set("User-Agent", webFetchUserAgent)
	response, err := (&http.Client{Timeout: timeout}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", target.Redacted(), err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return tools.BuildToolResult(false, "Fetch failed: expected 200 OK, got "+response.Status, nil), nil
	}
	if response.ContentLength > maxWebFetchBytes {
		return tools.BuildToolResult(false, "Response exceeds 5 MiB download limit", nil), nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxWebFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > maxWebFetchBytes {
		return tools.BuildToolResult(false, "Response exceeds 5 MiB download limit", nil), nil
	}

	article, err := readability.FromReader(bytes.NewReader(body), response.Request.URL)
	if err != nil {
		return nil, fmt.Errorf("extract readable content: %w", err)
	}
	if article.Node == nil {
		return tools.BuildToolResult(false, "No readable content found", nil), nil
	}
	markdown, err := htmltomarkdown.ConvertNode(article.Node)
	if err != nil {
		return nil, fmt.Errorf("convert readable content to Markdown: %w", err)
	}
	content := strings.TrimSpace(string(markdown))
	if content == "" {
		return tools.BuildToolResult(false, "No readable content found", nil), nil
	}

	data, err := tools.SerializeOutput(WebFetchOutput{
		URL:         response.Request.URL.String(),
		Title:       article.Title(),
		StatusCode:  response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Content:     content,
	})
	if err != nil {
		return nil, fmt.Errorf("serialize web_fetch output: %w", err)
	}
	return tools.BuildToolResult(true, "Web content fetched successfully", data), nil
}
