package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/models"
)

// MockProvider implements Provider for testing.
type MockProvider struct {
	chatFunc func(ctx context.Context, prompt *models.Prompt) (*Completion, error)
}

func (m *MockProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, prompt)
	}
	return &Completion{Raw: "mock response", FinishReason: "stop"}, nil
}

// MockFactory extends ProviderFactory to create MockProvider during tests.
type MockFactory struct {
	lastProviderName string
	lastModel        string
	lastEndpoint     string
	lastAPIKey       string
}

func (f *MockFactory) Create(ctx context.Context, providerName, model, endpoint, apiKey string) (Provider, error) {
	f.lastProviderName = providerName
	f.lastModel = model
	f.lastEndpoint = endpoint
	f.lastAPIKey = apiKey
	return &MockProvider{}, nil
}

func TestManager_Lifecycle(t *testing.T) {
	// Create a temp config dir
	tmpDir, err := os.MkdirTemp("", "supremo-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	credStore := NewFileCredentialStore(tmpDir)
	// Seed API key for gemini
	err = credStore.SetAPIKey(context.Background(), "gemini", "initial-key")
	if err != nil {
		t.Fatalf("failed to seed api key: %v", err)
	}

	mockFactory := &MockFactory{}
	manager := NewManager(tmpDir, credStore, mockFactory)

	ctx := context.Background()
	// Initialize
	if err := manager.Initialize(ctx); err != nil {
		t.Fatalf("failed to initialize manager: %v", err)
	}

	// Verify defaults written
	configPath := filepath.Join(tmpDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("expected config.yaml to be created, but it doesn't exist")
	}

	credsPath := filepath.Join(tmpDir, "credentials.json")
	if _, err := os.Stat(credsPath); os.IsNotExist(err) {
		t.Errorf("expected credentials.json to be created, but it doesn't exist")
	}

	// Verify initial client state
	providerName, model, endpoint, apiKey, client := manager.GetRuntimeConfig().Get()
	if providerName != "gemini" {
		t.Errorf("expected provider 'gemini', got '%s'", providerName)
	}
	if model != "gemini-2.5-flash" {
		t.Errorf("expected model 'gemini-2.5-flash', got '%s'", model)
	}
	if endpoint != "" {
		t.Errorf("expected empty endpoint, got '%s'", endpoint)
	}
	if apiKey != "initial-key" {
		t.Errorf("expected api key 'initial-key', got '%s'", apiKey)
	}
	if client == nil {
		t.Errorf("expected client to be initialized")
	}

	// Update active model
	err = manager.UpdateModel(ctx, "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("failed to update model: %v", err)
	}

	// Verify client got recreated with new model
	if mockFactory.lastModel != "gemini-2.5-pro" {
		t.Errorf("expected factory to get new model 'gemini-2.5-pro', got '%s'", mockFactory.lastModel)
	}
	_, model, _, _, _ = manager.GetRuntimeConfig().Get()
	if model != "gemini-2.5-pro" {
		t.Errorf("expected runtime model update to 'gemini-2.5-pro', got '%s'", model)
	}

	// Update active endpoint
	err = manager.UpdateEndpoint(ctx, "https://custom-endpoint.com")
	if err != nil {
		t.Fatalf("failed to update endpoint: %v", err)
	}

	// Verify client got recreated with new endpoint
	if mockFactory.lastEndpoint != "https://custom-endpoint.com" {
		t.Errorf("expected factory to get new endpoint 'https://custom-endpoint.com', got '%s'", mockFactory.lastEndpoint)
	}
	_, _, endpoint, _, _ = manager.GetRuntimeConfig().Get()
	if endpoint != "https://custom-endpoint.com" {
		t.Errorf("expected runtime endpoint update to 'https://custom-endpoint.com', got '%s'", endpoint)
	}

	// Update active API key
	err = manager.UpdateAPIKey(ctx, "new-key")
	if err != nil {
		t.Fatalf("failed to update api key: %v", err)
	}

	// Verify client got recreated with new API key
	if mockFactory.lastAPIKey != "new-key" {
		t.Errorf("expected factory to get new api key 'new-key', got '%s'", mockFactory.lastAPIKey)
	}
	_, _, _, apiKey, _ = manager.GetRuntimeConfig().Get()
	if apiKey != "new-key" {
		t.Errorf("expected runtime api key update to 'new-key', got '%s'", apiKey)
	}
}

type MockErrorFactory struct{}

func (f *MockErrorFactory) Create(ctx context.Context, providerName, model, endpoint, apiKey string) (Provider, error) {
	importFmt := "injected creation failure"
	return nil, fmt.Errorf("%s", importFmt)
}

func TestManager_RollbackOnFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "supremo-rollback-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	credStore := NewFileCredentialStore(tmpDir)
	_ = credStore.SetAPIKey(context.Background(), "gemini", "initial-key")

	mockFactory := &MockFactory{}
	manager := NewManager(tmpDir, credStore, mockFactory)

	ctx := context.Background()
	if err := manager.Initialize(ctx); err != nil {
		t.Fatalf("failed to initialize manager: %v", err)
	}

	// Swap factory to one that returns errors
	manager.factory = &MockErrorFactory{}

	// Attempt to update model (which will fail recreation)
	err = manager.UpdateModel(ctx, "gemini-2.5-pro")
	if err == nil {
		t.Errorf("expected error during update, got nil")
	}

	// Verify runtime config rolled back to original
	_, model, _, _, _ := manager.GetRuntimeConfig().Get()
	if model != "gemini-2.5-flash" {
		t.Errorf("expected model to remain 'gemini-2.5-flash' on rollback, got '%s'", model)
	}

	// Verify persisted config has also rolled back
	cfg, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.Model != "gemini-2.5-flash" {
		t.Errorf("expected persisted model to remain 'gemini-2.5-flash' on rollback, got '%s'", cfg.Model)
	}
}
