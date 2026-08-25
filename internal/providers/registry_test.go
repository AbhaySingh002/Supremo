package providers

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

type fakeTestProvider struct {
	name     string
	apiKey   string
	model    string
	endpoint string
	metadata Metadata
	metaErr  error
}

func (f *fakeTestProvider) FetchMetadata(context.Context) (Metadata, error) {
	return f.metadata, f.metaErr
}

func testRegistration(name string, factory Factory) ProviderRegistration {
	return ProviderRegistration{Type: name, DisplayName: name + " display", Description: name + " description", Factory: factory}
}

func (f *fakeTestProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	return &Completion{Text: fmt.Sprintf("response from %s (%s)", f.name, f.model)}, nil
}

func TestProviderRegistryRegistrationAndResolution(t *testing.T) {
	reg := NewRegistry()

	if err := reg.Register(testRegistration("custom-mock", func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
		return &fakeTestProvider{name: "custom-mock", apiKey: apiKey, model: model, endpoint: endpoint}, nil
	})); err != nil {
		t.Fatal(err)
	}

	if !reg.Has("custom-mock") {
		t.Fatal("expected registry to have custom-mock")
	}

	p, err := reg.Create(context.Background(), "custom-mock", "test-key", "gpt-test", "http://localhost")
	if err != nil {
		t.Fatalf("unexpected create error: %v", err)
	}

	fake, ok := p.(*fakeTestProvider)
	if !ok || fake.name != "custom-mock" || fake.apiKey != "test-key" {
		t.Fatalf("unexpected provider instance: %#v", p)
	}
}

func TestProviderRegistryRejectsInvalidOrDuplicateTypes(t *testing.T) {
	registry := NewRegistry()
	factory := func(context.Context, string, string, string) (Provider, error) { return &fakeTestProvider{}, nil }
	for _, name := range []string{"", "  ", "openai:local"} {
		if err := registry.Register(testRegistration(name, factory)); err == nil {
			t.Fatalf("Register(%q) succeeded", name)
		}
	}
	if err := registry.Register(testRegistration("custom", nil)); err == nil {
		t.Fatal("Register accepted a nil factory")
	}
	if err := registry.Register(testRegistration("Custom", factory)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(testRegistration("custom", factory)); err == nil {
		t.Fatal("Register accepted a duplicate type")
	}
	if err := registry.Register(ProviderRegistration{Type: "missing-copy", Factory: factory}); err == nil {
		t.Fatal("Register accepted missing presentation metadata")
	}
}

func TestProviderRegistryBuiltinsAreDeterministic(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterBuiltins(registry); err != nil {
		t.Fatal(err)
	}
	want := []string{"anthropic", "gemini", "groq", "mistral", "nvidia", "openai", "openai-compatible", "openrouter"}
	if got := registry.RegisteredTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RegisteredTypes() = %v, want %v", got, want)
	}
	if err := RegisterBuiltins(registry); err == nil {
		t.Fatal("RegisterBuiltins accepted duplicate registrations")
	}
	compatible, ok := registry.Registration("openai-compatible:local")
	if !ok || !compatible.RequiresEndpoint || !compatible.AllowsNamedRoutes {
		t.Fatalf("compatible registration = %#v, found=%t", compatible, ok)
	}
}

func TestManagerRequiresExplicitRegistry(t *testing.T) {
	if _, err := NewManager(t.TempDir(), NewFileCredentialStore(t.TempDir()), nil); err == nil {
		t.Fatal("NewManager accepted a nil registry")
	}
}

func TestProviderRegistryUnknownProviderError(t *testing.T) {
	reg := NewRegistry()

	if reg.Has("non-existent-provider") {
		t.Fatal("expected non-existent-provider to not be present")
	}

	_, err := reg.Create(context.Background(), "non-existent-provider", "key", "model", "")
	if err == nil {
		t.Fatal("expected error creating unknown provider")
	}
}

func TestProviderManagerWithCustomRegistry(t *testing.T) {
	dir := t.TempDir()
	credStore := NewFileCredentialStore(dir)
	_ = credStore.SetAPIKey("mock-ai", "secret-mock-key")

	cfg := &Config{
		ProviderName: "mock-ai",
		Model:        "mock-large",
		Models:       map[string]string{"mock-ai": "mock-large"},
		Endpoints:    map[string]string{},
	}
	_ = SaveConfig(dir, cfg)

	reg := NewRegistry()
	registration := testRegistration("mock-ai", func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
		return &fakeTestProvider{name: "mock-ai", apiKey: apiKey, model: model, endpoint: endpoint}, nil
	})
	registration.DefaultModel = "mock-default"
	if err := reg.Register(registration); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(dir, credStore, reg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := mgr.Initialize(ctx); err != nil {
		t.Fatalf("manager Initialize failed: %v", err)
	}

	pName, mName := mgr.ModelInfo()
	if pName != "mock-ai" || mName != "mock-large" {
		t.Fatalf("expected mock-ai / mock-large, got %s / %s", pName, mName)
	}

	completion, err := mgr.Chat(ctx, &models.Prompt{})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if completion.Text != "response from mock-ai (mock-large)" {
		t.Fatalf("unexpected completion text: %s", completion.Text)
	}

	// Runtime override to another model
	if err := mgr.ApplyRuntimeOverrides(ctx, RuntimeOverrides{Model: "mock-small"}); err != nil {
		t.Fatalf("ApplyRuntimeOverrides failed: %v", err)
	}

	_, mName2 := mgr.ModelInfo()
	if mName2 != "mock-small" {
		t.Fatalf("expected model mock-small after override, got %s", mName2)
	}
}

func TestManagerUsesRegistrationDefaultsAndChoices(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCredentialStore(dir)
	for _, name := range []string{"initial", "custom"} {
		if err := store.SetAPIKey(name, "key"); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveConfig(dir, &Config{ProviderName: "initial", Model: "initial-model"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	factory := func(ctx context.Context, apiKey, model, endpoint string) (Provider, error) {
		return &fakeTestProvider{name: "custom", apiKey: apiKey, model: model, endpoint: endpoint}, nil
	}
	if err := registry.Register(testRegistration("initial", factory)); err != nil {
		t.Fatal(err)
	}
	custom := testRegistration("custom", factory)
	custom.DisplayName, custom.Description = "Custom Provider", "Registered test provider"
	custom.DefaultModel, custom.DefaultEndpoint = "custom-model", "https://custom.example/v1"
	if err := registry.Register(custom); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dir, store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateProvider(context.Background(), "custom"); err != nil {
		t.Fatal(err)
	}
	provider, model, endpoint, _, _ := manager.GetRuntimeConfig().Get()
	if provider != "custom" || model != custom.DefaultModel || endpoint != custom.DefaultEndpoint {
		t.Fatalf("registered defaults not applied: provider=%q model=%q endpoint=%q", provider, model, endpoint)
	}
	choices := manager.Providers()
	if len(choices) != 2 || choices[1].Type != "initial" || choices[0].DisplayName != custom.DisplayName {
		t.Fatalf("registered choices = %#v", choices)
	}
}

func TestManagerRequiresEndpointForRegisteredProvider(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCredentialStore(dir)
	if err := store.SetAPIKey("initial", "key"); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(dir, &Config{ProviderName: "initial", Model: "initial-model"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	factory := func(context.Context, string, string, string) (Provider, error) { return &fakeTestProvider{}, nil }
	if err := registry.Register(testRegistration("initial", factory)); err != nil {
		t.Fatal(err)
	}
	required := testRegistration("custom", factory)
	required.RequiresEndpoint, required.AllowsNamedRoutes = true, true
	if err := registry.Register(required); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dir, store, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	err = manager.UpdateProvider(context.Background(), "custom")
	if err == nil || !strings.Contains(err.Error(), "/provider custom:<name> <url>") {
		t.Fatalf("missing endpoint error = %v", err)
	}
}
