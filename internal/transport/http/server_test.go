package httptransport

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/backend"
)

type testBackend struct {
	backend.API
	subscribe chan api.SubscribeRequest
}

func (b *testBackend) Initialize(context.Context) (api.InitializeResult, error) {
	return api.InitializeResult{APIVersion: api.Version, ServerVersion: "test"}, nil
}

func (b *testBackend) ListModels(_ context.Context, request api.ListModelsRequest) (api.ModelCatalog, error) {
	state := "cached"
	if request.Refresh {
		state = "fresh"
	}
	return api.ModelCatalog{Providers: []api.Provider{{ID: "openai", Configured: true, MetadataState: state, Models: []api.Model{{ID: "gpt-test"}}}}}, nil
}

func (b *testBackend) Subscribe(_ context.Context, request api.SubscribeRequest) (api.EventStream, error) {
	if b.subscribe != nil {
		b.subscribe <- request
	}
	events := make(chan api.Event, 1)
	events <- api.Event{V: api.Version, Cursor: 8, EventID: "event-8", Type: "run/end", Durable: true}
	close(events)
	return backend.NewSubscription(events), nil
}

func startHTTPTestServer(t *testing.T) (*Server, *testBackend) {
	t.Helper()
	service := &testBackend{subscribe: make(chan api.SubscribeRequest, 1)}
	server, err := Listen("127.0.0.1:0", "test-token", "test", service)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	return server, service
}

func TestServerRequiresTokenHostAndSameOrigin(t *testing.T) {
	server, _ := startHTTPTestServer(t)
	client := http.Client{Timeout: time.Second}
	body := `{"v":1,"id":"1","method":"initialize","params":{}}`
	request, _ := http.NewRequest(http.MethodPost, server.URL()+"/api/v1/rpc", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL()+"/api/v1/rpc", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-token")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("authorized response = %d %s", response.StatusCode, data)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL()+"/api/v1/rpc", strings.NewReader(body))
	request.Host = "evil.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-token")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("invalid host status = %d", response.StatusCode)
	}
}

func TestSSEUsesDurableCursorAndAuthorizationHeader(t *testing.T) {
	server, service := startHTTPTestServer(t)
	request, _ := http.NewRequest(http.MethodGet, server.URL()+"/api/v1/events?session_id=chat", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Last-Event-ID", "7")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(data), "id: 8") || !strings.Contains(string(data), "event: run/end") {
		t.Fatalf("SSE response = %d %s", response.StatusCode, data)
	}
	select {
	case got := <-service.subscribe:
		if got.AfterCursor != 7 || got.SessionID != "chat" {
			t.Fatalf("subscribe request = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("subscribe was not called")
	}
}

func TestHTTPClientMatchesRPCAndEventContracts(t *testing.T) {
	server, _ := startHTTPTestServer(t)
	client, err := NewClient(server.URL(), "test-token", nil)
	if err != nil {
		t.Fatal(err)
	}
	initialized, err := client.Initialize(context.Background())
	if err != nil || initialized.APIVersion != api.Version || initialized.ServerVersion != "test" {
		t.Fatalf("initialize = %#v, %v", initialized, err)
	}
	catalog, err := client.ListModels(context.Background(), api.ListModelsRequest{Refresh: true})
	if err != nil || len(catalog.Providers) != 1 || catalog.Providers[0].MetadataState != "fresh" || catalog.Providers[0].Models[0].ID != "gpt-test" {
		t.Fatalf("model catalog = %#v, %v", catalog, err)
	}
	stream, err := client.Subscribe(context.Background(), api.SubscribeRequest{SessionID: "chat", AfterCursor: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-stream.Events():
		if event.Cursor != 8 || event.Type != "run/end" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP event stream did not deliver")
	}
}

func TestListenRejectsNonLoopbackAddress(t *testing.T) {
	if server, err := Listen("0.0.0.0:0", "token", "test", &testBackend{}); err == nil {
		server.Close()
		t.Fatal("expected non-loopback address rejection")
	}
}
