package providers

import (
	"context"
	"fmt"
	"sync"

	"github.com/AbhaySingh002/supremo/internal/models"
)

// Manager coordinates file configuration, credentials, client initialization, and state.
type Manager struct {
	mu            sync.RWMutex
	configDir     string
	credStore     *FileCredentialStore
	runtimeConfig *RuntimeConfig
}

// NewManager constructs a new Provider Manager.
func NewManager(configDir string, credStore *FileCredentialStore) *Manager {
	return &Manager{configDir: configDir, credStore: credStore}
}

// Initialize configures files, pulls keys, and sets runtime configs on boot.
func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := EnsureConfigDir(m.configDir); err != nil {
		return fmt.Errorf("failed to configure dir: %w", err)
	}
	cfg, err := LoadConfig(m.configDir)
	if err != nil {
		return fmt.Errorf("failed to load configs: %w", err)
	}
	if cfg.ProviderName != "gemini" {
		return fmt.Errorf("unsupported provider: %s", cfg.ProviderName)
	}
	apiKey, err := m.credStore.GetAPIKey(cfg.ProviderName)
	if err != nil {
		return fmt.Errorf("failed to load API key: %w", err)
	}
	client, err := NewGeminiProvider(ctx, apiKey, cfg.Model, cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("failed to create Gemini provider: %w", err)
	}
	m.runtimeConfig = NewRuntimeConfig(cfg.ProviderName, cfg.Model, cfg.Endpoint, apiKey, client)
	return nil
}

// CurrentProvider returns the active Provider client.
func (m *Manager) CurrentProvider() Provider {
	m.mu.RLock()
	runtime := m.runtimeConfig
	m.mu.RUnlock()
	if runtime == nil {
		return nil
	}
	return runtime.GetClient()
}

// Chat satisfies the agent.Provider interface.
func (m *Manager) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	client := m.CurrentProvider()
	if client == nil {
		return nil, fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	return client.Chat(ctx, prompt)
}

// update applies a setting, persists it, and restores both on client creation failure.
func (m *Manager) update(ctx context.Context, change func(), persist func() error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.runtimeConfig == nil {
		return fmt.Errorf("manager not initialized")
	}

	m.runtimeConfig.mu.Lock()
	defer m.runtimeConfig.mu.Unlock()

	previousProvider := m.runtimeConfig.providerName
	previousModel := m.runtimeConfig.model
	previousEndpoint := m.runtimeConfig.endpoint
	previousAPIKey := m.runtimeConfig.apiKey
	restore := func() {
		m.runtimeConfig.providerName = previousProvider
		m.runtimeConfig.model = previousModel
		m.runtimeConfig.endpoint = previousEndpoint
		m.runtimeConfig.apiKey = previousAPIKey
	}

	change()
	if err := persist(); err != nil {
		restore()
		return err
	}
	client, err := NewGeminiProvider(ctx, m.runtimeConfig.apiKey, m.runtimeConfig.model, m.runtimeConfig.endpoint)
	if err != nil {
		restore()
		if restoreErr := persist(); restoreErr != nil {
			return fmt.Errorf("failed to rebuild provider client: %w; failed to restore previous configuration: %v", err, restoreErr)
		}
		return fmt.Errorf("failed to rebuild provider client: %w", err)
	}
	m.runtimeConfig.activeClient = client
	return nil
}

func (m *Manager) saveConfig() error {
	return SaveConfig(m.configDir, &Config{
		ProviderName: m.runtimeConfig.providerName,
		Model:        m.runtimeConfig.model,
		Endpoint:     m.runtimeConfig.endpoint,
	})
}

// UpdateModel updates model settings.
func (m *Manager) UpdateModel(ctx context.Context, model string) error {
	return m.update(ctx, func() { m.runtimeConfig.model = model }, m.saveConfig)
}

// UpdateEndpoint updates endpoints.
func (m *Manager) UpdateEndpoint(ctx context.Context, endpoint string) error {
	return m.update(ctx, func() { m.runtimeConfig.endpoint = endpoint }, m.saveConfig)
}

// UpdateAPIKey updates provider API key.
func (m *Manager) UpdateAPIKey(ctx context.Context, apiKey string) error {
	return m.update(ctx, func() { m.runtimeConfig.apiKey = apiKey }, func() error {
		return m.credStore.SetAPIKey(m.runtimeConfig.providerName, m.runtimeConfig.apiKey)
	})
}

// UpdateProvider switches active provider.
func (m *Manager) UpdateProvider(ctx context.Context, providerName string) error {
	if providerName != "gemini" {
		return fmt.Errorf("unsupported provider: %s", providerName)
	}
	return m.update(ctx, func() {
		m.runtimeConfig.providerName = providerName
		m.runtimeConfig.apiKey, _ = m.credStore.GetAPIKey(providerName)
	}, m.saveConfig)
}

// GetRuntimeConfig retrieves the active runtime configurations.
func (m *Manager) GetRuntimeConfig() *RuntimeConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtimeConfig
}
