package web_search

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestWebFetchExtractsReadableMarkdown(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.UserAgent()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Readable title</title></head><body>
			<nav>Navigation should disappear</nav>
			<article>
				<h1>Readable title</h1>
				<p>This is the primary article content with enough detail to be recognized as useful prose for a reader.</p>
				<p>It contains <strong>important context</strong> and a <a href="/source">source link</a> for the language model.</p>
				<p>Another substantial paragraph keeps the focus on this article instead of nearby menus, advertisements, or footers.</p>
			</article>
			<aside>Advertisement should disappear</aside>
			<footer>Footer should disappear</footer>
			<script>ignore()</script>
		</body></html>`))
	}))
	defer server.Close()

	result, err := (&WebFetch{}).Execute(context.Background(), map[string]any{"url": server.URL})
	if err != nil || !result.Success {
		t.Fatalf("web_fetch failed: %#v, %v", result, err)
	}
	if result.Data["title"] != "Readable title" {
		t.Fatalf("title = %#v", result.Data["title"])
	}
	content := result.Data["content"].(string)
	for _, want := range []string{"**important context**", "[source link](" + server.URL + "/source)"} {
		if !strings.Contains(content, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"Navigation should disappear", "Advertisement should disappear", "Footer should disappear", "ignore()"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("Markdown retained clutter %q:\n%s", unwanted, content)
		}
	}
	if !strings.HasPrefix(userAgent, "Mozilla/5.0") {
		t.Fatalf("unexpected User-Agent %q", userAgent)
	}
	if result.Data["status_code"] != float64(http.StatusOK) {
		t.Fatalf("status code = %#v", result.Data["status_code"])
	}
}

func TestWebFetchRejectsBadResponsesAndHonorsContext(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}))
		defer server.Close()
		result, err := (&WebFetch{}).Execute(context.Background(), map[string]any{"url": server.URL})
		if err != nil || result.Success || !strings.Contains(result.Message, "503") {
			t.Fatalf("unexpected status result: %#v, %v", result, err)
		}
	})

	t.Run("download limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(maxWebFetchBytes+1))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		result, err := (&WebFetch{}).Execute(context.Background(), map[string]any{"url": server.URL})
		if err != nil || result.Success || !strings.Contains(result.Message, "5 MiB") {
			t.Fatalf("unexpected limit result: %#v, %v", result, err)
		}
	})

	t.Run("context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := (&WebFetch{}).Execute(ctx, map[string]any{"url": "https://example.com"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	})
}

func TestWebFetchSchemaIncludesTimeoutDefault(t *testing.T) {
	properties := (&WebFetch{}).Schema().(map[string]any)["properties"].(map[string]any)
	timeout := properties["timeout_seconds"].(map[string]any)
	if timeout["default"] != 12 {
		t.Fatalf("timeout default = %#v, want 12", timeout["default"])
	}
}
