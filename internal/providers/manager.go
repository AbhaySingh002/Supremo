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
	credStore     CredentialStore
	factory       ProviderFactory
	runtimeConfig *RuntimeConfig
}

// NewManager constructs a new Provider Manager.
func NewManager(configDir string, credStore CredentialStore, factory ProviderFactory) *Manager {
	return &Manager{
		configDir: configDir,
		credStore: credStore,
		factory:   factory,
	}
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

	apiKey, _ := m.credStore.GetAPIKey(ctx, cfg.ProviderName)

	client, err := m.factory.Create(ctx, cfg.ProviderName, cfg.Model, cfg.Endpoint, apiKey)
	if err != nil {
		// Proceed with a nil client if client initialization fails (e.g. missing API key)
		client = nil
	}

	m.runtimeConfig = NewRuntimeConfig(cfg.ProviderName, cfg.Model, cfg.Endpoint, apiKey, client)
	return nil
}

// CurrentProvider returns the active Provider client.
func (m *Manager) CurrentProvider() Provider {
	if m.runtimeConfig == nil {
		return nil
	}
	return m.runtimeConfig.GetClient()
}

// Chat satisfies the agent.Provider interface.
func (m *Manager) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	client := m.CurrentProvider()
	if client == nil {
		return nil, fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	return client.Chat(ctx, prompt)
}

// UpdateModel updates model settings.
func (m *Manager) UpdateModel(ctx context.Context, model string) error {
	if m.runtimeConfig == nil {
		return fmt.Errorf("manager not initialized")
	}

	m.runtimeConfig.mu.Lock()
	defer m.runtimeConfig.mu.Unlock()

	origProvider := m.runtimeConfig.providerName
	origModel := m.runtimeConfig.model
	origEndpoint := m.runtimeConfig.endpoint

	// 1. Update RuntimeConfig
	m.runtimeConfig.model = model

	// 2. Persist Config
	cfg := &Config{
		ProviderName: m.runtimeConfig.providerName,
		Model:        m.runtimeConfig.model,
		Endpoint:     m.runtimeConfig.endpoint,
	}
	if err := SaveConfig(m.configDir, cfg); err != nil {
		m.runtimeConfig.model = origModel
		return err
	}

	// 3. Recreate Gemini Client
	client, err := m.factory.Create(ctx, m.runtimeConfig.providerName, m.runtimeConfig.model, m.runtimeConfig.endpoint, m.runtimeConfig.apiKey)
	if err != nil {
		m.runtimeConfig.model = origModel
		rollbackCfg := &Config{
			ProviderName: origProvider,
			Model:        origModel,
			Endpoint:     origEndpoint,
		}
		_ = SaveConfig(m.configDir, rollbackCfg)
		return fmt.Errorf("failed to rebuild provider client: %w", err)
	}

	// 4. Swap Client Atomically
	m.runtimeConfig.activeClient = client
	return nil
}

// UpdateEndpoint updates endpoints.
func (m *Manager) UpdateEndpoint(ctx context.Context, endpoint string) error {
	if m.runtimeConfig == nil {
		return fmt.Errorf("manager not initialized")
	}

	m.runtimeConfig.mu.Lock()
	defer m.runtimeConfig.mu.Unlock()

	origProvider := m.runtimeConfig.providerName
	origModel := m.runtimeConfig.model
	origEndpoint := m.runtimeConfig.endpoint

	// 1. Update RuntimeConfig
	m.runtimeConfig.endpoint = endpoint

	// 2. Persist Config
	cfg := &Config{
		ProviderName: m.runtimeConfig.providerName,
		Model:        m.runtimeConfig.model,
		Endpoint:     m.runtimeConfig.endpoint,
	}
	if err := SaveConfig(m.configDir, cfg); err != nil {
		m.runtimeConfig.endpoint = origEndpoint
		return err
	}

	// 3. Recreate Gemini Client
	client, err := m.factory.Create(ctx, m.runtimeConfig.providerName, m.runtimeConfig.model, m.runtimeConfig.endpoint, m.runtimeConfig.apiKey)
	if err != nil {
		m.runtimeConfig.endpoint = origEndpoint
		rollbackCfg := &Config{
			ProviderName: origProvider,
			Model:        origModel,
			Endpoint:     origEndpoint,
		}
		_ = SaveConfig(m.configDir, rollbackCfg)
		return fmt.Errorf("failed to rebuild provider client: %w", err)
	}

	// 4. Swap Client Atomically
	m.runtimeConfig.activeClient = client
	return nil
}

// UpdateAPIKey updates provider API key.
func (m *Manager) UpdateAPIKey(ctx context.Context, apiKey string) error {
	if m.runtimeConfig == nil {
		return fmt.Errorf("manager not initialized")
	}

	m.runtimeConfig.mu.Lock()
	defer m.runtimeConfig.mu.Unlock()

	origProvider := m.runtimeConfig.providerName
	origAPIKey := m.runtimeConfig.apiKey

	// 1. Update RuntimeConfig
	m.runtimeConfig.apiKey = apiKey

	// 2. Persist Credentials
	if err := m.credStore.SetAPIKey(ctx, m.runtimeConfig.providerName, apiKey); err != nil {
		m.runtimeConfig.apiKey = origAPIKey
		return err
	}

	// 3. Recreate Gemini Client
	client, err := m.factory.Create(ctx, m.runtimeConfig.providerName, m.runtimeConfig.model, m.runtimeConfig.endpoint, m.runtimeConfig.apiKey)
	if err != nil {
		m.runtimeConfig.apiKey = origAPIKey
		_ = m.credStore.SetAPIKey(ctx, origProvider, origAPIKey)
		return fmt.Errorf("failed to rebuild provider client: %w", err)
	}

	// 4. Swap Client Atomically
	m.runtimeConfig.activeClient = client
	return nil
}

// UpdateProvider switches active provider.
func (m *Manager) UpdateProvider(ctx context.Context, providerName string) error {
	if m.runtimeConfig == nil {
		return fmt.Errorf("manager not initialized")
	}

	m.runtimeConfig.mu.Lock()
	defer m.runtimeConfig.mu.Unlock()

	origProvider := m.runtimeConfig.providerName
	origModel := m.runtimeConfig.model
	origEndpoint := m.runtimeConfig.endpoint
	origAPIKey := m.runtimeConfig.apiKey

	// Load api key for new provider
	apiKey, _ := m.credStore.GetAPIKey(ctx, providerName)

	// 1. Update RuntimeConfig
	m.runtimeConfig.providerName = providerName
	m.runtimeConfig.apiKey = apiKey

	// 2. Persist Config
	cfg := &Config{
		ProviderName: m.runtimeConfig.providerName,
		Model:        m.runtimeConfig.model,
		Endpoint:     m.runtimeConfig.endpoint,
	}
	if err := SaveConfig(m.configDir, cfg); err != nil {
		m.runtimeConfig.providerName = origProvider
		m.runtimeConfig.apiKey = origAPIKey
		return err
	}

	// 3. Recreate Gemini Client
	client, err := m.factory.Create(ctx, m.runtimeConfig.providerName, m.runtimeConfig.model, m.runtimeConfig.endpoint, m.runtimeConfig.apiKey)
	if err != nil {
		m.runtimeConfig.providerName = origProvider
		m.runtimeConfig.apiKey = origAPIKey
		rollbackCfg := &Config{
			ProviderName: origProvider,
			Model:        origModel,
			Endpoint:     origEndpoint,
		}
		_ = SaveConfig(m.configDir, rollbackCfg)
		return fmt.Errorf("failed to rebuild provider client: %w", err)
	}

	// 4. Swap Client Atomically
	m.runtimeConfig.activeClient = client
	return nil
}

// GetRuntimeConfig retrieves the active runtime configurations.
func (m *Manager) GetRuntimeConfig() *RuntimeConfig {
	return m.runtimeConfig
}
