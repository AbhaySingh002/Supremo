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
