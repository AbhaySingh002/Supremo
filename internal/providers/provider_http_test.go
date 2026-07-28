package providers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

func TestOpenRouterRefreshAndRuntimeUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"vendor/model","name":"Model","context_length":32768,"pricing":{"prompt":"0.000001","completion":"0.000002"}}]}`)
		case "/credits":
			fmt.Fprint(w, `{"data":{"total_credits":10,"total_usage":2.5}}`)
		case "/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":4,"cost":0.00002}}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	if err := SaveConfig(dir, &Config{ProviderName: "openrouter", Model: "vendor/model", Endpoint: server.URL}); err != nil {
		t.Fatal(err)
	}
	credentials := NewFileCredentialStore(dir)
	if err := credentials.SetAPIKey("openrouter", "test-key"); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(dir, credentials)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.RefreshMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := manager.ContextLimit(); got != 32768 {
		t.Fatalf("context limit = %d, want 32768", got)
	}
	completion, err := manager.Chat(context.Background(), &models.Prompt{Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Raw != "done" || completion.Usage.InputTokens != 12 {
		t.Fatalf("unexpected completion: %#v", completion)
	}
	usage := manager.GetRuntimeConfig().Usage()
	if usage.OutputTokens != 4 || usage.CostUSD == nil || *usage.CostUSD != 0.00002 {
		t.Fatalf("unexpected runtime usage: %#v", usage)
	}
	if account := manager.GetRuntimeConfig().Metadata().Account; account == nil || account.TotalCredits-account.TotalUsage != 7.5 {
		t.Fatalf("unexpected account metadata: %#v", account)
	}

	secondManager := NewManager(dir, credentials)
	if err := secondManager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := secondManager.ContextLimit(); got != 32768 {
		t.Fatalf("cached context limit = %d, want 32768", got)
	}
}

func TestAnthropicProviderUsesMessagesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q", got)
		}
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"claude-test","display_name":"Claude Test"}]}`)
		case "/messages":
			fmt.Fprint(w, `{"content":[{"type":"text","text":"done"},{"type":"tool_use","text":"ignored"},{"type":"text","text":" now"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewAnthropicProvider(context.Background(), "test-key", "claude-test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := provider.FetchMetadata(context.Background())
	if err != nil || len(metadata.Models) != 1 || metadata.Models[0].ID != "claude-test" {
		t.Fatalf("unexpected metadata: %#v, %v", metadata, err)
	}
	completion, err := provider.Chat(context.Background(), &models.Prompt{System: "system", Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Raw != "done now" || completion.Usage.InputTokens != 3 || completion.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected completion: %#v", completion)
	}
}
