package web

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

type webHTTPClientFunc func(*http.Request) (*http.Response, error)

func (f webHTTPClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestWebFetchExtractsReadableMarkdown(t *testing.T) {
	var request *http.Request
	client := webHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
		request = req
		body := `<html><head><title>Readable title</title></head><body>
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
		</body></html>`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})

	result, err := (&WebFetch{client: client}).Execute(context.Background(), map[string]any{"url": "https://example.com/article"})
	if err != nil || !result.Success {
		t.Fatalf("web_fetch failed: %#v, %v", result, err)
	}
	if result.Data["title"] != "Readable title" {
		t.Fatalf("title = %#v", result.Data["title"])
	}
	content := result.Data["content"].(string)
	for _, want := range []string{"**important context**", "[source link](https://example.com/source)"} {
		if !strings.Contains(content, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{"Navigation should disappear", "Advertisement should disappear", "Footer should disappear", "ignore()"} {
		if strings.Contains(content, unwanted) {
			t.Fatalf("Markdown retained clutter %q:\n%s", unwanted, content)
		}
	}
	if !strings.HasPrefix(request.UserAgent(), "Mozilla/5.0") {
		t.Fatalf("unexpected User-Agent %q", request.UserAgent())
	}
	if result.Data["status_code"] != float64(http.StatusOK) {
		t.Fatalf("status code = %#v", result.Data["status_code"])
	}
	if !strings.Contains(result.Message, "untrusted external data") {
		t.Fatalf("missing trust boundary label: %q", result.Message)
	}
}

func TestWebFetchRejectsBadResponsesAndHonorsContext(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		client := webHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Status:     "503 Service Unavailable",
				Body:       io.NopCloser(strings.NewReader("unavailable")),
				Request:    req,
			}, nil
		})
		result, err := (&WebFetch{client: client}).Execute(context.Background(), map[string]any{"url": "https://example.com"})
		if err != nil || result.Success || !strings.Contains(result.Message, "503") {
			t.Fatalf("unexpected status result: %#v, %v", result, err)
		}
	})

	t.Run("download limit", func(t *testing.T) {
		client := webHTTPClientFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				ContentLength: maxWebFetchBytes + 1,
				Body:          io.NopCloser(strings.NewReader("")),
				Request:       req,
			}, nil
		})
		result, err := (&WebFetch{client: client}).Execute(context.Background(), map[string]any{"url": "https://example.com"})
		if err != nil || result.Success || !strings.Contains(result.Message, "5 MiB") {
			t.Fatalf("unexpected limit result: %#v, %v", result, err)
		}
	})

	t.Run("context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client := webHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
			return nil, request.Context().Err()
		})
		_, err := (&WebFetch{client: client}).Execute(ctx, map[string]any{"url": "https://example.com"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	})
}

func TestValidateWebFetchURLAllowsOnlyPublicHTTPDestinations(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://example.com/path", true},
		{"http://8.8.8.8", true},
		{"https://[2606:4700:4700::1111]", true},
		{"ftp://example.com", false},
		{"https://user:password@example.com", false},
		{"http://0.0.0.0", false},
		{"http://10.0.0.1", false},
		{"http://100.64.0.1", false},
		{"http://127.0.0.1", false},
		{"http://169.254.169.254", false},
		{"http://224.0.0.1", false},
		{"http://[::]", false},
		{"http://[::1]", false},
		{"http://[::ffff:127.0.0.1]", false},
		{"http://[fe80::1]", false},
		{"http://[ff02::1]", false},
		{"http://[3fff::1]", false},
		{"http://[5f00::1]", false},
		{"http://[4000::1]", false},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			target, err := url.Parse(test.url)
			if err != nil {
				t.Fatal(err)
			}
			if got := validateWebFetchURL(target) == nil; got != test.want {
				t.Fatalf("valid = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPublicDialContextValidatesAndPinsDNSResult(t *testing.T) {
	public := netip.MustParseAddr("93.184.216.34")
	private := netip.MustParseAddr("127.0.0.1")
	reservedIPv6 := netip.MustParseAddr("3fff::1")
	dialFailure := errors.New("dial stopped by test")

	t.Run("pins public address", func(t *testing.T) {
		lookups := 0
		dialed := ""
		dialer := publicDialContext(
			func(context.Context, string, string) ([]netip.Addr, error) {
				lookups++
				return []netip.Addr{public}, nil
			},
			func(_ context.Context, _ string, address string) (net.Conn, error) {
				dialed = address
				return nil, dialFailure
			},
		)
		_, err := dialer(context.Background(), "tcp", "example.com:443")
		if !errors.Is(err, dialFailure) || lookups != 1 || dialed != "93.184.216.34:443" {
			t.Fatalf("error=%v lookups=%d dialed=%q", err, lookups, dialed)
		}
	})

	for _, ips := range [][]netip.Addr{{private}, {public, private}, {reservedIPv6}} {
		t.Run("rejects non-public DNS answer "+ips[len(ips)-1].String(), func(t *testing.T) {
			dialed := false
			dialer := publicDialContext(
				func(context.Context, string, string) ([]netip.Addr, error) { return ips, nil },
				func(context.Context, string, string) (net.Conn, error) {
					dialed = true
					return nil, dialFailure
				},
			)
			_, err := dialer(context.Background(), "tcp", "example.com:80")
			if err == nil || dialed {
				t.Fatalf("error=%v dialed=%v", err, dialed)
			}
		})
	}
}

func TestWebFetchClientChecksRedirectsWithoutEnvironmentProxy(t *testing.T) {
	client := newWebFetchClient()
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("web fetch transport must not use environment proxies")
	}

	publicRequest := &http.Request{URL: mustURL(t, "https://example.com/next")}
	if err := client.CheckRedirect(publicRequest, []*http.Request{{URL: mustURL(t, "https://example.com")}}); err != nil {
		t.Fatalf("public redirect rejected: %v", err)
	}
	for _, target := range []string{"http://127.0.0.1/admin", "https://user@example.com"} {
		request := &http.Request{URL: mustURL(t, target)}
		if err := client.CheckRedirect(request, []*http.Request{{URL: mustURL(t, "https://example.com")}}); err == nil {
			t.Fatalf("unsafe redirect allowed: %s", target)
		}
	}
}

func TestWebFetchSchemaIncludesTimeoutDefault(t *testing.T) {
	properties := (&WebFetch{}).Schema().(map[string]any)["properties"].(map[string]any)
	timeout := properties["timeout_seconds"].(map[string]any)
	if timeout["default"] != 12 {
		t.Fatalf("timeout default = %#v, want 12", timeout["default"])
	}
}

func mustURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
