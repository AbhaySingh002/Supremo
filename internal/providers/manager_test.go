package providers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newBuiltinManager(t testing.TB, configDir string, credStore *FileCredentialStore) *Manager {
	t.Helper()
	registry := NewRegistry()
	if err := RegisterBuiltins(registry); err != nil {
		t.Fatalf("register built-in providers: %v", err)
	}
	manager, err := NewManager(configDir, credStore, registry)
	if err != nil {
		t.Fatalf("new provider manager: %v", err)
	}
	return manager
}

func TestModelCatalogRefreshesConfiguredProvidersWithCachedFallback(t *testing.T) {
	dir := t.TempDir()
	credentials := NewFileCredentialStore(dir)
	for _, provider := range []string{"initial", "fallback"} {
		if err := credentials.SetAPIKey(provider, provider+"-key"); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveConfig(dir, &Config{
		ProviderName: "initial", Model: "chat-model",
		Models:    map[string]string{"initial": "chat-model", "fallback": "cached-model"},
		Endpoints: map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	fetchedAt := time.Now().UTC()
	initial := testRegistration("initial", func(context.Context, string, string, string) (Provider, error) {
		return &fakeTestProvider{metadata: Metadata{FetchedAt: fetchedAt, Models: []ModelInfo{
			{ID: "chat-model", Name: "Chat Model"}, {ID: "image-model", Name: "Image generator"}, {ID: "text-embedding-3", Name: "Embedding"},
		}}}, nil
	})
	fallback := testRegistration("fallback", func(context.Context, string, string, string) (Provider, error) {
		return &fakeTestProvider{metaErr: errors.New("Get https://user:pass@example.test/models?api_key=fallback-key: provider offline")}, nil
	})
	if err := registry.Register(initial); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fallback); err != nil {
		t.Fatal(err)
	}
	if err := saveMetadataCache(dir, &metadataCache{Providers: map[string]Metadata{
		cacheKey("fallback", ""): {FetchedAt: fetchedAt.Add(-time.Hour), Models: []ModelInfo{{ID: "cached-model", Name: "Cached Model"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(dir, credentials, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.ModelCatalog(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 2 || catalog[0].ID != "fallback" || catalog[0].State != "cached" || catalog[0].Warning == "" || len(catalog[0].Metadata.Models) != 1 {
		t.Fatalf("fallback catalog = %#v", catalog)
	}
	if strings.Contains(catalog[0].Warning, "fallback-key") || strings.Contains(catalog[0].Warning, "user:pass") || strings.Contains(catalog[0].Warning, "api_key") {
		t.Fatalf("catalog warning exposed endpoint credentials: %q", catalog[0].Warning)
	}
	if catalog[1].ID != "initial" || catalog[1].State != "fresh" || len(catalog[1].Metadata.Models) != 1 || catalog[1].Metadata.Models[0].ID != "chat-model" {
		t.Fatalf("fresh catalog = %#v", catalog)
	}
	provider, model := manager.ModelInfo()
	if provider != "initial" || model != "chat-model" {
		t.Fatalf("catalog refresh changed active runtime to %s/%s", provider, model)
	}

	providerName, replacement := "fallback", "replacement-key"
	if err := manager.Configure(context.Background(), ConfigurationUpdate{Provider: &providerName, APIKey: &replacement, Verify: true}); err == nil {
		t.Fatal("expected verification failure")
	}
	provider, model = manager.ModelInfo()
	stored, _ := credentials.GetAPIKey("fallback")
	if provider != "initial" || model != "chat-model" || stored != "fallback-key" {
		t.Fatalf("failed verification leaked state: active=%s/%s key=%q", provider, model, stored)
	}
}

func TestTextGenerationModelFilterUsesCapabilitiesAndPracticalExclusions(t *testing.T) {
	models := textGenerationModels([]ModelInfo{
		{ID: "chat"},
		{ID: "vision-chat", ModalitiesKnown: true, AcceptsText: true, ProducesText: true},
		{ID: "image-only", ModalitiesKnown: true, AcceptsText: true},
		{ID: "text-embedding-3-small"},
		{ID: "whisper-large"},
	})
	if len(models) != 2 || models[0].ID != "chat" || models[1].ID != "vision-chat" {
		t.Fatalf("filtered models = %#v", models)
	}
}

func TestManagerLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	credStore := NewFileCredentialStore(tmpDir)
	if err := credStore.SetAPIKey("gemini", "initial-key"); err != nil {
		t.Fatalf("seed API key: %v", err)
	}

	manager := newBuiltinManager(t, tmpDir, credStore)
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	for _, name := range []string{"config.yaml", "credentials.json"} {
		if _, err := os.Stat(filepath.Join(tmpDir, name)); err != nil {
			t.Errorf("expected %s to be created: %v", name, err)
		}
	}

	if err := manager.UpdateModel(ctx, "gemini-2.5-pro"); err != nil {
		t.Fatalf("update model: %v", err)
	}
	if err := manager.UpdateEndpoint(ctx, "https://custom-endpoint.com"); err != nil {
		t.Fatalf("update endpoint: %v", err)
	}
	if err := manager.UpdateAPIKey(ctx, "new-key"); err != nil {
		t.Fatalf("update API key: %v", err)
	}

	provider, model, endpoint, apiKey, client := manager.GetRuntimeConfig().Get()
	if provider != "gemini" || model != "gemini-2.5-pro" || endpoint != "https://custom-endpoint.com" || apiKey != "new-key" || client == nil {
		t.Errorf("unexpected runtime config: provider=%q model=%q endpoint=%q apiKey=%q client=%T", provider, model, endpoint, apiKey, client)
	}
}

func TestManagerRollbackOnProviderFailure(t *testing.T) {
	tmpDir := t.TempDir()
	credStore := NewFileCredentialStore(tmpDir)
	if err := credStore.SetAPIKey("gemini", "initial-key"); err != nil {
		t.Fatalf("seed API key: %v", err)
	}

	manager := newBuiltinManager(t, tmpDir, credStore)
	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := manager.UpdateProvider(ctx, "unsupported"); err == nil {
		t.Fatal("expected unsupported provider update to fail")
	}

	provider, _, _, _, _ := manager.GetRuntimeConfig().Get()
	if provider != "gemini" {
		t.Errorf("expected provider rollback to gemini, got %q", provider)
	}
	cfg, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ProviderName != "gemini" {
		t.Errorf("expected persisted provider rollback to gemini, got %q", cfg.ProviderName)
	}
}

func TestManagerInitializeRejectsUnsupportedProvider(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, &Config{ProviderName: "other", Model: "model"}); err != nil {
		t.Fatal(err)
	}
	err := newBuiltinManager(t, dir, NewFileCredentialStore(dir)).Initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("unexpected initialization error: %v", err)
	}
}

func TestManagerKeepsEndpointAndModelPerProvider(t *testing.T) {
	dir := t.TempDir()
	credentials := NewFileCredentialStore(dir)
	if err := credentials.SetAPIKey("gemini", "gemini-key"); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SetAPIKey("openai", "openai-key"); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SetAPIKey("groq", "groq-key"); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SetAPIKey("nvidia", "nvidia-key"); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SetAPIKey("mistral", "mistral-key"); err != nil {
		t.Fatal(err)
	}
	manager := newBuiltinManager(t, dir, credentials)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateEndpoint(context.Background(), "https://gemini.example/"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateProviderEndpoint(context.Background(), "openai", "https://openai.example/v1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateModel(context.Background(), "openai-model"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateProvider(context.Background(), "gemini"); err != nil {
		t.Fatal(err)
	}
	provider, model, endpoint, _, _ := manager.GetRuntimeConfig().Get()
	if provider != "gemini" || model != defaultGeminiModel || endpoint != "https://gemini.example/" {
		t.Fatalf("gemini config was not restored: provider=%q model=%q endpoint=%q", provider, model, endpoint)
	}
	if err := manager.UpdateProviderEndpoint(context.Background(), "openai-compatible:local", "http://localhost:11434/v1"); err != nil {
		t.Fatal(err)
	}
	provider, _, endpoint, _, _ = manager.GetRuntimeConfig().Get()
	if provider != "openai-compatible:local" || endpoint != "http://localhost:11434/v1" {
		t.Fatalf("named compatible provider was not stored: provider=%q endpoint=%q", provider, endpoint)
	}
	if err := manager.UpdateProvider(context.Background(), "groq"); err != nil {
		t.Fatal(err)
	}
	provider, model, endpoint, _, _ = manager.GetRuntimeConfig().Get()
	if provider != "groq" || model != defaultGroqModel || endpoint != groqEndpoint {
		t.Fatalf("Groq defaults were not selected: provider=%q model=%q endpoint=%q", provider, model, endpoint)
	}
	if err := manager.UpdateProvider(context.Background(), "nvidia"); err != nil {
		t.Fatal(err)
	}
	provider, model, endpoint, _, _ = manager.GetRuntimeConfig().Get()
	if provider != "nvidia" || model != defaultNVIDIAModel || endpoint != nvidiaEndpoint {
		t.Fatalf("NVIDIA defaults were not selected: provider=%q model=%q endpoint=%q", provider, model, endpoint)
	}
	if err := manager.UpdateProvider(context.Background(), "mistral"); err != nil {
		t.Fatal(err)
	}
	provider, model, endpoint, _, _ = manager.GetRuntimeConfig().Get()
	if provider != "mistral" || model != defaultMistralModel || endpoint != mistralEndpoint {
		t.Fatalf("Mistral defaults were not selected: provider=%q model=%q endpoint=%q", provider, model, endpoint)
	}
}

func TestManagerHandlesMissingAndCorruptCredentials(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, &Config{ProviderName: "openai", Model: "gpt-test"}); err != nil {
		t.Fatal(err)
	}
	store := NewFileCredentialStore(dir)
	manager := newBuiltinManager(t, dir, store)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize without stored key: %v", err)
	}
	if manager.GetRuntimeConfig().CredentialConfigured() {
		t.Fatal("missing key reported as configured")
	}

	path := filepath.Join(dir, credentialsFileName)
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAPIKey("openai", "replacement"); err == nil {
		t.Fatal("corrupt credentials should not be overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{" {
		t.Fatalf("corrupt credentials changed: %q, %v", data, err)
	}
}

func TestManagerRollsBackConfigWhenPersistenceFails(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCredentialStore(dir)
	if err := store.SetAPIKey("gemini", "key"); err != nil {
		t.Fatal(err)
	}
	manager := newBuiltinManager(t, dir, store)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	manager.configDir = filepath.Join(dir, "missing")
	if err := manager.UpdateModel(context.Background(), "replacement"); err == nil {
		t.Fatal("expected persistence failure")
	}
	_, model, _, _, _ := manager.GetRuntimeConfig().Get()
	if model != defaultGeminiModel || manager.config.Model != model || manager.config.Models["gemini"] != model {
		t.Fatalf("failed update leaked into memory: runtime=%q config=%#v", model, manager.config)
	}
}

func TestRuntimeOverridesDoNotPersist(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCredentialStore(dir)
	if err := store.SetAPIKey("gemini", "stored-key"); err != nil {
		t.Fatal(err)
	}
	manager := newBuiltinManager(t, dir, store)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyRuntimeOverrides(context.Background(), RuntimeOverrides{Model: "runtime-model", APIKey: "runtime-key"}); err != nil {
		t.Fatal(err)
	}
	_, model, _, apiKey, _ := manager.GetRuntimeConfig().Get()
	if model != "runtime-model" || apiKey != "runtime-key" {
		t.Fatalf("runtime override was not applied: model=%q key=%q", model, apiKey)
	}
	persisted, err := LoadConfig(dir)
	if err != nil || persisted.Model != defaultGeminiModel {
		t.Fatalf("runtime override changed config: %#v, %v", persisted, err)
	}
	storedKey, err := store.GetAPIKey("gemini")
	if err != nil || storedKey != "stored-key" {
		t.Fatalf("runtime override changed credentials: key=%q err=%v", storedKey, err)
	}
}
