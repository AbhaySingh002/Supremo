package providers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	credStore := NewFileCredentialStore(tmpDir)
	if err := credStore.SetAPIKey("gemini", "initial-key"); err != nil {
		t.Fatalf("seed API key: %v", err)
	}

	manager := NewManager(tmpDir, credStore)
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

	manager := NewManager(tmpDir, credStore)
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

func TestManagerKeepsEndpointAndModelPerProvider(t *testing.T) {
	dir := t.TempDir()
	credentials := NewFileCredentialStore(dir)
	if err := credentials.SetAPIKey("gemini", "gemini-key"); err != nil {
		t.Fatal(err)
	}
	if err := credentials.SetAPIKey("openai", "openai-key"); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(dir, credentials)
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
}

func TestManagerHandlesMissingAndCorruptCredentials(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, &Config{ProviderName: "openai", Model: "gpt-test"}); err != nil {
		t.Fatal(err)
	}
	store := NewFileCredentialStore(dir)
	manager := NewManager(dir, store)
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
	manager := NewManager(dir, store)
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
