package providers

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// Manager coordinates persisted provider settings, credentials, cached metadata, and the active client.
type Manager struct {
	mu            sync.RWMutex
	configDir     string
	credStore     *FileCredentialStore
	config        *Config
	runtimeConfig *RuntimeConfig
	metadataCache *metadataCache
}

func NewManager(configDir string, credStore *FileCredentialStore) *Manager {
	return &Manager{configDir: configDir, credStore: credStore}
}

func providerClient(ctx context.Context, providerName, apiKey, model, endpoint string) (Provider, error) {
	switch providerType(providerName) {
	case "gemini":
		return NewGeminiProvider(ctx, apiKey, model, endpoint)
	case "openai":
		return NewOpenAIProvider(ctx, apiKey, model, endpoint)
	case "openrouter":
		return NewOpenRouterProvider(ctx, apiKey, model, endpoint)
	case "anthropic":
		return NewAnthropicProvider(ctx, apiKey, model, endpoint)
	case "openai-compatible":
		return NewOpenAICompatibleProvider(ctx, apiKey, model, endpoint)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}

func providerType(providerName string) string {
	return strings.SplitN(providerName, ":", 2)[0]
}

func knownProvider(providerName string) bool {
	switch providerType(providerName) {
	case "gemini", "openai", "openrouter", "anthropic", "openai-compatible":
		return true
	default:
		return false
	}
}

// Initialize loads the selected provider without making a metadata request.
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
	if !knownProvider(cfg.ProviderName) {
		return fmt.Errorf("unsupported provider: %s", cfg.ProviderName)
	}
	apiKey, err := m.credStore.GetAPIKey(cfg.ProviderName)
	if err != nil {
		return fmt.Errorf("failed to load API key: %w", err)
	}
	client, err := providerClient(ctx, cfg.ProviderName, apiKey, cfg.Model, cfg.Endpoint)
	if err != nil {
		return err
	}
	cache, err := loadMetadataCache(m.configDir)
	if err != nil {
		return fmt.Errorf("load provider metadata cache: %w", err)
	}
	runtime := NewRuntimeConfig(cfg.ProviderName, cfg.Model, cfg.Endpoint, apiKey, client)
	runtime.setMetadata(cache.Providers[cacheKey(cfg.ProviderName, cfg.Endpoint)])
	m.config, m.runtimeConfig, m.metadataCache = cfg, runtime, cache
	return nil
}

func (m *Manager) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	m.mu.RLock()
	runtime := m.runtimeConfig
	m.mu.RUnlock()
	if runtime == nil {
		return nil, fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	client := runtime.GetClient()
	if client == nil {
		return nil, fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	completion, err := client.Chat(ctx, prompt)
	if err != nil {
		return nil, err
	}
	if completion != nil {
		runtime.addUsage(completion.Usage)
	}
	return completion, nil
}

// Stream forwards incremental text when the active provider supports it, otherwise Chat.
func (m *Manager) Stream(ctx context.Context, prompt *models.Prompt, receive func(string)) (*Completion, error) {
	m.mu.RLock()
	runtime := m.runtimeConfig
	m.mu.RUnlock()
	if runtime == nil {
		return nil, fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	client := runtime.GetClient()
	if client == nil {
		return nil, fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	streamer, ok := client.(StreamProvider)
	if !ok {
		return m.Chat(ctx, prompt)
	}
	completion, err := streamer.Stream(ctx, prompt, receive)
	if err != nil {
		return nil, err
	}
	if completion != nil {
		runtime.addUsage(completion.Usage)
	}
	return completion, nil
}

func (m *Manager) update(ctx context.Context, change func(), persist func() error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.runtimeConfig == nil {
		return fmt.Errorf("manager not initialized")
	}
	runtime := m.runtimeConfig
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	previousProvider, previousModel, previousEndpoint, previousAPIKey := runtime.providerName, runtime.model, runtime.endpoint, runtime.apiKey
	previousConfig := *m.config
	previousConfig.Models = maps.Clone(m.config.Models)
	previousConfig.Endpoints = maps.Clone(m.config.Endpoints)
	restore := func() {
		runtime.providerName, runtime.model, runtime.endpoint, runtime.apiKey = previousProvider, previousModel, previousEndpoint, previousAPIKey
		*m.config = previousConfig
	}
	change()
	client, err := providerClient(ctx, runtime.providerName, runtime.apiKey, runtime.model, runtime.endpoint)
	if err != nil {
		restore()
		return err
	}
	if err := persist(); err != nil {
		restore()
		return err
	}
	runtime.activeClient = client
	if m.metadataCache != nil {
		runtime.metadata = m.metadataCache.Providers[cacheKey(runtime.providerName, runtime.endpoint)]
	}
	return nil
}

func (m *Manager) saveConfig() error {
	m.config.ProviderName = m.runtimeConfig.providerName
	m.config.Model = m.runtimeConfig.model
	m.config.Endpoint = m.runtimeConfig.endpoint
	m.config.Models[m.runtimeConfig.providerName] = m.runtimeConfig.model
	m.config.Endpoints[m.runtimeConfig.providerName] = m.runtimeConfig.endpoint
	return SaveConfig(m.configDir, m.config)
}

func (m *Manager) UpdateModel(ctx context.Context, model string) error {
	return m.update(ctx, func() { m.runtimeConfig.model = model }, m.saveConfig)
}

func (m *Manager) UpdateEndpoint(ctx context.Context, endpoint string) error {
	return m.update(ctx, func() { m.runtimeConfig.endpoint = endpoint }, m.saveConfig)
}

func (m *Manager) UpdateAPIKey(ctx context.Context, apiKey string) error {
	return m.update(ctx, func() { m.runtimeConfig.apiKey = apiKey }, func() error {
		return m.credStore.SetAPIKey(m.runtimeConfig.providerName, m.runtimeConfig.apiKey)
	})
}

func (m *Manager) UpdateProvider(ctx context.Context, providerName string) error {
	apiKey, err := m.credStore.GetAPIKey(providerName)
	if err != nil {
		return err
	}
	return m.update(ctx, func() {
		m.runtimeConfig.providerName = providerName
		m.runtimeConfig.model = m.config.Models[providerName]
		m.runtimeConfig.endpoint = m.config.Endpoints[providerName]
		m.runtimeConfig.apiKey = apiKey
	}, m.saveConfig)
}

// UpdateProviderEndpoint switches provider and endpoint atomically, for custom compatible servers.
func (m *Manager) UpdateProviderEndpoint(ctx context.Context, providerName, endpoint string) error {
	apiKey, err := m.credStore.GetAPIKey(providerName)
	if err != nil {
		return err
	}
	return m.update(ctx, func() {
		m.runtimeConfig.providerName, m.runtimeConfig.model, m.runtimeConfig.endpoint = providerName, m.config.Models[providerName], endpoint
		m.runtimeConfig.apiKey = apiKey
	}, m.saveConfig)
}

// RefreshMetadata fetches models and optional account data once, then persists them locally.
func (m *Manager) RefreshMetadata(ctx context.Context) error {
	m.mu.RLock()
	runtime, cache := m.runtimeConfig, m.metadataCache
	m.mu.RUnlock()
	if runtime == nil || cache == nil {
		return fmt.Errorf("manager not initialized")
	}
	providerName, _, endpoint, apiKey, client := runtime.Get()
	metadataProvider, ok := client.(MetadataProvider)
	if !ok {
		return fmt.Errorf("%s does not support model metadata refresh", providerName)
	}
	metadata, err := metadataProvider.FetchMetadata(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.providerName != providerName || runtime.endpoint != endpoint || runtime.apiKey != apiKey {
		return fmt.Errorf("provider changed while refreshing metadata")
	}
	key := cacheKey(providerName, endpoint)
	m.metadataCache.Providers[key] = metadata
	if err := saveMetadataCache(m.configDir, m.metadataCache); err != nil {
		return err
	}
	runtime.metadata = metadata
	return nil
}

func (m *Manager) ContextLimit() int {
	m.mu.RLock()
	runtime := m.runtimeConfig
	m.mu.RUnlock()
	if runtime == nil {
		return 0
	}
	return runtime.ContextLimit()
}

func (m *Manager) GetRuntimeConfig() *RuntimeConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtimeConfig
}
