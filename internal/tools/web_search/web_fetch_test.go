package web_search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestWebFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<h1>page &amp; content</h1><script>ignore()</script><style>.hidden {}</style>"))
	}))
	defer server.Close()

	result, err := (&WebFetch{}).Execute(context.Background(), map[string]any{"url": server.URL})
	if err != nil || !result.Success || result.Data["content"] != "page & content" || result.Data["status_code"] != float64(http.StatusOK) {
		t.Fatalf("unexpected fetch result: %#v, %v", result, err)
	}
}

func TestWebFetchRejectsInvalidAndBoundsContent(t *testing.T) {
	result, err := (&WebFetch{}).Execute(context.Background(), map[string]any{"url": "file:///etc/passwd"})
	if err != nil || result.Success {
		t.Fatalf("invalid URL was accepted: %#v, %v", result, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(tools.MaxFileBytes)+1)))
	}))
	defer server.Close()
	result, err = (&WebFetch{}).Execute(context.Background(), map[string]any{"url": server.URL})
	if err != nil || !result.Success || !result.Data["truncated"].(bool) || len(result.Data["content"].(string)) != int(tools.MaxFileBytes) {
		t.Fatalf("content limit was not enforced: %#v, %v", result, err)
	}
}
