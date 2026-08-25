package providers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// Manager coordinates persisted provider settings, credentials, cached metadata, and the active client.
type Manager struct {
	mu            sync.RWMutex
	configDir     string
	credStore     *FileCredentialStore
	registry      *Registry
	config        *Config
	runtimeConfig *RuntimeConfig
	metadataCache *metadataCache
}

// RuntimeOverrides apply only to this process. They never write global
// provider configuration or credentials.
type RuntimeOverrides struct {
	Provider string
	Model    string
	Endpoint string
	APIKey   string
}

// CatalogProvider is a credential-safe snapshot used by frontends to choose a model.
type CatalogProvider struct {
	ID               string
	Name             string
	Endpoint         string
	RequiresEndpoint bool
	State            string
	Warning          string
	Metadata         Metadata
}

// ConfigurationUpdate stages one provider runtime change before it is persisted.
type ConfigurationUpdate struct {
	Provider *string
	Model    *string
	Endpoint *string
	APIKey   *string
	Verify   bool
}

// EmbeddingSettings keeps the global endpoint/model/credential source out of
// workspace state; only each workspace's opt-in flag is stored locally.
type EmbeddingSettings struct {
	CredentialProvider string
	Endpoint           string
	Model              string
	APIKey             string
}

func NewManager(configDir string, credStore *FileCredentialStore, registry *Registry) (*Manager, error) {
	if registry == nil {
		return nil, fmt.Errorf("provider registry is required")
	}
	return &Manager{configDir: configDir, credStore: credStore, registry: registry}, nil
}

func providerType(providerName string) string {
	return strings.SplitN(providerName, ":", 2)[0]
}

func (m *Manager) createClient(ctx context.Context, providerName, apiKey, model, endpoint string) (Provider, error) {
	if m == nil || m.registry == nil {
		return nil, fmt.Errorf("provider registry is not initialized")
	}
	return m.registry.Create(ctx, providerName, apiKey, model, endpoint)
}

func (m *Manager) isKnownProvider(providerName string) bool {
	if m == nil || m.registry == nil {
		return false
	}
	return m.registry.Has(providerName)
}

// Providers returns the registered CLI-facing provider choices.
func (m *Manager) Providers() []ProviderRegistration {
	if m == nil || m.registry == nil {
		return nil
	}
	return m.registry.Registrations()
}

func (m *Manager) providerRegistration(providerName string) (ProviderRegistration, error) {
	if m == nil || m.registry == nil {
		return ProviderRegistration{}, fmt.Errorf("provider registry is not initialized")
	}
	registration, ok := m.registry.Registration(providerName)
	if !ok {
		return ProviderRegistration{}, fmt.Errorf("unsupported provider: %s", providerName)
	}
	return registration, nil
}

// ModelInfo returns the current active provider and model name.
func (m *Manager) ModelInfo() (string, string) {
	if m == nil {
		return "", ""
	}
	m.mu.RLock()
	runtime := m.runtimeConfig
	m.mu.RUnlock()
	if runtime == nil {
		return "", ""
	}
	pName, mName, _, _, _ := runtime.Get()
	return pName, mName
}

// ProviderConfigured reports credential presence without exposing the value.
func (m *Manager) ProviderConfigured(providerName string) bool {
	if m == nil || m.credStore == nil {
		return false
	}
	key, err := m.credStore.GetAPIKey(providerName)
	return err == nil && credentialConfigured(key)
}

// ProviderEndpoint returns a configured endpoint without exposing credentials.
func (m *Manager) ProviderEndpoint(providerName string) string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.config == nil {
		return ""
	}
	registration, ok := m.registry.Registration(providerName)
	if !ok {
		return ""
	}
	return configuredEndpoint(m.config, providerName, registration.DefaultEndpoint)
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
	if !m.isKnownProvider(cfg.ProviderName) {
		return fmt.Errorf("unsupported provider: %s", cfg.ProviderName)
	}
	apiKey, err := m.credStore.GetAPIKey(cfg.ProviderName)
	if err != nil {
		return fmt.Errorf("failed to load API key: %w", err)
	}
	client, err := m.createClient(ctx, cfg.ProviderName, apiKey, cfg.Model, cfg.Endpoint)
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

// Stream forwards normalized events when possible. A non-streaming adapter is
// converted to the same event representation without assembling a second response.
func (m *Manager) Stream(ctx context.Context, prompt *models.Prompt, receive func(StreamEvent) error) error {
	m.mu.RLock()
	runtime := m.runtimeConfig
	m.mu.RUnlock()
	if runtime == nil {
		return fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	client := runtime.GetClient()
	if client == nil {
		return fmt.Errorf("active provider client not initialized (API key may be missing)")
	}
	streamer, ok := client.(StreamProvider)
	if !ok {
		completion, err := m.Chat(ctx, prompt)
		if err != nil {
			return err
		}
		return EmitCompletion(completion, receive)
	}
	return streamer.Stream(ctx, prompt, func(event StreamEvent) error {
		if event.Type == StreamEventUsage && event.Usage != nil {
			runtime.addUsage(*event.Usage)
		}
		if receive == nil {
			return nil
		}
		return receive(event)
	})
}

func (m *Manager) update(ctx context.Context, change func() error, persist func() error) error {
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
	if err := change(); err != nil {
		restore()
		return err
	}
	client, err := m.createClient(ctx, runtime.providerName, runtime.apiKey, runtime.model, runtime.endpoint)
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
	return m.update(ctx, func() error { m.runtimeConfig.model = model; return nil }, m.saveConfig)
}

func (m *Manager) UpdateEndpoint(ctx context.Context, endpoint string) error {
	return m.update(ctx, func() error { m.runtimeConfig.endpoint = endpoint; return nil }, m.saveConfig)
}

func (m *Manager) UpdateAPIKey(ctx context.Context, apiKey string) error {
	return m.update(ctx, func() error { m.runtimeConfig.apiKey = apiKey; return nil }, func() error {
		return m.credStore.SetAPIKey(m.runtimeConfig.providerName, m.runtimeConfig.apiKey)
	})
}

func (m *Manager) UpdateProvider(ctx context.Context, providerName string) error {
	registration, err := m.providerRegistration(providerName)
	if err != nil {
		return err
	}
	apiKey, err := m.credStore.GetAPIKey(providerName)
	if err != nil {
		return err
	}
	return m.update(ctx, func() error {
		model := configuredModel(m.config, providerName, registration.DefaultModel)
		endpoint := configuredEndpoint(m.config, providerName, registration.DefaultEndpoint)
		if err := validateProviderEndpoint(registration, endpoint); err != nil {
			return err
		}
		m.runtimeConfig.providerName = providerName
		m.runtimeConfig.model = model
		m.runtimeConfig.endpoint = endpoint
		m.runtimeConfig.apiKey = apiKey
		return nil
	}, m.saveConfig)
}

// Configure applies provider, model, endpoint, and credential changes as one
// staged runtime update. Verification happens before any durable setting changes.
func (m *Manager) Configure(ctx context.Context, update ConfigurationUpdate) error {
	if m == nil {
		return fmt.Errorf("manager is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeConfig == nil || m.config == nil || m.metadataCache == nil {
		return fmt.Errorf("manager not initialized")
	}

	runtime := m.runtimeConfig
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	providerName, model, endpoint, apiKey := runtime.providerName, runtime.model, runtime.endpoint, runtime.apiKey
	if update.Provider != nil {
		providerName = strings.TrimSpace(*update.Provider)
		registration, err := m.providerRegistration(providerName)
		if err != nil {
			return err
		}
		model = configuredModel(m.config, providerName, registration.DefaultModel)
		endpoint = configuredEndpoint(m.config, providerName, registration.DefaultEndpoint)
		stored, err := m.credStore.GetAPIKey(providerName)
		if err != nil {
			return err
		}
		apiKey = stored
	}
	registration, err := m.providerRegistration(providerName)
	if err != nil {
		return err
	}
	if update.Model != nil {
		model = strings.TrimSpace(*update.Model)
	}
	if update.Endpoint != nil {
		endpoint = strings.TrimSpace(*update.Endpoint)
	}
	if update.APIKey != nil {
		apiKey = *update.APIKey
	}
	if err := validateProviderEndpoint(registration, endpoint); err != nil {
		return err
	}
	if update.Verify && !credentialConfigured(apiKey) {
		return fmt.Errorf("API key is required for %s", providerName)
	}
	client, err := m.createClient(ctx, providerName, apiKey, model, endpoint)
	if err != nil {
		return errors.New(safeMetadataError(err, apiKey))
	}

	metadata := m.metadataCache.Providers[cacheKey(providerName, endpoint)]
	if update.Verify {
		fetcher, ok := client.(MetadataProvider)
		if !ok {
			return fmt.Errorf("%s does not support credential verification", providerName)
		}
		metadata, err = fetcher.FetchMetadata(ctx)
		if err != nil {
			return errors.New(safeMetadataError(err, apiKey))
		}
		metadata.Models = textGenerationModels(metadata.Models)
	}

	nextConfig := *m.config
	nextConfig.Models = maps.Clone(m.config.Models)
	nextConfig.Endpoints = maps.Clone(m.config.Endpoints)
	nextConfig.ProviderName, nextConfig.Model, nextConfig.Endpoint = providerName, model, endpoint
	nextConfig.Models[providerName], nextConfig.Endpoints[providerName] = model, endpoint

	nextCache := &metadataCache{Providers: maps.Clone(m.metadataCache.Providers)}
	if update.Verify {
		nextCache.Providers[cacheKey(providerName, endpoint)] = metadata
		if err := saveMetadataCache(m.configDir, nextCache); err != nil {
			return err
		}
	}
	oldKey := ""
	if update.APIKey != nil {
		oldKey, err = m.credStore.GetAPIKey(providerName)
		if err != nil {
			return err
		}
		if err := m.credStore.SetAPIKey(providerName, apiKey); err != nil {
			return err
		}
	}
	if err := SaveConfig(m.configDir, &nextConfig); err != nil {
		if update.APIKey != nil {
			return errors.Join(err, m.credStore.SetAPIKey(providerName, oldKey))
		}
		return err
	}

	m.config = &nextConfig
	if update.Verify {
		m.metadataCache = nextCache
	}
	runtime.providerName, runtime.model, runtime.endpoint, runtime.apiKey = providerName, model, endpoint, apiKey
	runtime.activeClient, runtime.metadata = client, metadata
	return nil
}

// ModelCatalog returns deterministic model metadata for configured providers.
// Refreshes are isolated from the active runtime and partial failures retain cache.
func (m *Manager) ModelCatalog(ctx context.Context, refresh bool) ([]CatalogProvider, error) {
	if m == nil {
		return nil, fmt.Errorf("manager is required")
	}
	m.mu.RLock()
	if m.config == nil || m.metadataCache == nil {
		m.mu.RUnlock()
		return nil, fmt.Errorf("manager not initialized")
	}
	config := *m.config
	config.Models = maps.Clone(m.config.Models)
	config.Endpoints = maps.Clone(m.config.Endpoints)
	cached := maps.Clone(m.metadataCache.Providers)
	registrations := m.registry.Registrations()
	m.mu.RUnlock()

	names := make(map[string]ProviderRegistration, len(registrations))
	for _, registration := range registrations {
		names[registration.Type] = registration
	}
	for name := range config.Models {
		if registration, ok := m.registry.Registration(name); ok {
			names[name] = registration
		}
	}
	for name := range config.Endpoints {
		if registration, ok := m.registry.Registration(name); ok {
			names[name] = registration
		}
	}

	type catalogWork struct {
		result CatalogProvider
		model  string
		key    string
	}
	work := make([]catalogWork, 0, len(names))
	for name, registration := range names {
		key, err := m.credStore.GetAPIKey(name)
		if err != nil {
			return nil, err
		}
		if !credentialConfigured(key) {
			continue
		}
		endpoint := configuredEndpoint(&config, name, registration.DefaultEndpoint)
		metadata := cached[cacheKey(name, endpoint)]
		metadata.Models = textGenerationModels(metadata.Models)
		state := "unavailable"
		if len(metadata.Models) > 0 {
			state = "cached"
		}
		label := registration.DisplayName
		if route := strings.TrimPrefix(name, registration.Type+":"); route != name {
			label += " (" + route + ")"
		}
		work = append(work, catalogWork{
			result: CatalogProvider{ID: name, Name: label, Endpoint: endpoint, RequiresEndpoint: registration.RequiresEndpoint, State: state, Metadata: metadata},
			model:  configuredModel(&config, name, registration.DefaultModel), key: key,
		})
	}
	slices.SortStableFunc(work, func(a, b catalogWork) int { return strings.Compare(a.result.ID, b.result.ID) })
	if refresh {
		semaphore := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for index := range work {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					work[index].result.Warning = ctx.Err().Error()
					return
				}
				item := &work[index]
				if item.result.RequiresEndpoint && strings.TrimSpace(item.result.Endpoint) == "" {
					item.result.Warning = "endpoint is not configured"
					return
				}
				client, err := m.createClient(ctx, item.result.ID, item.key, item.model, item.result.Endpoint)
				if err == nil {
					fetcher, ok := client.(MetadataProvider)
					if !ok {
						err = fmt.Errorf("model listing is not supported")
					} else {
						var metadata Metadata
						metadata, err = fetcher.FetchMetadata(ctx)
						if err == nil {
							item.result.Metadata = metadata
						}
					}
				}
				if err != nil {
					item.result.Warning = safeMetadataError(err, item.key)
					return
				}
				item.result.Metadata.Models = textGenerationModels(item.result.Metadata.Models)
				item.result.State = "fresh"
			}(index)
		}
		wg.Wait()

		next := &metadataCache{Providers: maps.Clone(cached)}
		changed := false
		for _, item := range work {
			if item.result.State == "fresh" {
				next.Providers[cacheKey(item.result.ID, item.result.Endpoint)] = item.result.Metadata
				changed = true
			}
		}
		if changed {
			m.mu.Lock()
			if err := saveMetadataCache(m.configDir, next); err != nil {
				m.mu.Unlock()
				return nil, err
			}
			m.metadataCache = next
			providerName, _, endpoint, _, _ := m.runtimeConfig.Get()
			for _, item := range work {
				if item.result.State == "fresh" && item.result.ID == providerName && item.result.Endpoint == endpoint {
					m.runtimeConfig.mu.Lock()
					m.runtimeConfig.metadata = item.result.Metadata
					m.runtimeConfig.mu.Unlock()
					break
				}
			}
			m.mu.Unlock()
		}
	}

	result := make([]CatalogProvider, 0, len(work))
	for _, item := range work {
		if item.result.Warning != "" && len(item.result.Metadata.Models) > 0 {
			item.result.State = "cached"
		}
		item.result.Metadata.Models = textGenerationModels(item.result.Metadata.Models)
		result = append(result, item.result)
	}
	return result, nil
}

func safeMetadataError(err error, apiKey string) string {
	message := strings.TrimSpace(err.Error())
	if apiKey != "" {
		message = strings.ReplaceAll(message, apiKey, "[redacted]")
	}
	fields := strings.Fields(message)
	for index, field := range fields {
		if strings.Contains(field, "://") {
			fields[index] = "[endpoint]"
		}
	}
	message = strings.Join(fields, " ")
	if len(message) > 240 {
		message = message[:239] + "…"
	}
	return message
}

// UpdateProviderEndpoint switches provider and endpoint atomically, for custom compatible servers.
func (m *Manager) UpdateProviderEndpoint(ctx context.Context, providerName, endpoint string) error {
	registration, err := m.providerRegistration(providerName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("provider endpoint is required")
	}
	apiKey, err := m.credStore.GetAPIKey(providerName)
	if err != nil {
		return err
	}
	return m.update(ctx, func() error {
		m.runtimeConfig.providerName, m.runtimeConfig.model, m.runtimeConfig.endpoint = providerName, configuredModel(m.config, providerName, registration.DefaultModel), endpoint
		m.runtimeConfig.apiKey = apiKey
		return nil
	}, m.saveConfig)
}

func validateProviderEndpoint(registration ProviderRegistration, endpoint string) error {
	if !registration.RequiresEndpoint || strings.TrimSpace(endpoint) != "" {
		return nil
	}
	name := registration.Type
	if registration.AllowsNamedRoutes {
		name += ":<name>"
	}
	return fmt.Errorf("provider %q requires an endpoint; use /provider %s <url>", registration.Type, name)
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
		return errors.New(safeMetadataError(err, apiKey))
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

func (m *Manager) EmbeddingSettings() (EmbeddingSettings, error) {
	m.mu.RLock()
	if m.config == nil {
		m.mu.RUnlock()
		return EmbeddingSettings{}, fmt.Errorf("manager not initialized")
	}
	settings := EmbeddingSettings{CredentialProvider: m.config.EmbeddingCredentialProvider, Endpoint: m.config.EmbeddingEndpoint, Model: m.config.EmbeddingModel}
	m.mu.RUnlock()
	if settings.CredentialProvider == "" || settings.Endpoint == "" || settings.Model == "" {
		return settings, nil
	}
	key, err := m.credStore.GetAPIKey(settings.CredentialProvider)
	if err != nil {
		return EmbeddingSettings{}, err
	}
	settings.APIKey = key
	return settings, nil
}

func (m *Manager) UpdateEmbeddingSettings(credentialProvider, endpoint, model string) error {
	if strings.TrimSpace(credentialProvider) == "" || strings.TrimSpace(endpoint) == "" || strings.TrimSpace(model) == "" {
		return fmt.Errorf("embedding credential provider, endpoint, and model are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config == nil {
		return fmt.Errorf("manager not initialized")
	}
	previous := *m.config
	m.config.EmbeddingCredentialProvider, m.config.EmbeddingEndpoint, m.config.EmbeddingModel = credentialProvider, endpoint, model
	if err := SaveConfig(m.configDir, m.config); err != nil {
		*m.config = previous
		return err
	}
	return nil
}

// ApplyRuntimeOverrides applies CLI or environment settings without changing
// the persisted configuration. Empty fields retain the current runtime value.
func (m *Manager) ApplyRuntimeOverrides(ctx context.Context, overrides RuntimeOverrides) error {
	if overrides == (RuntimeOverrides{}) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeConfig == nil || m.config == nil {
		return fmt.Errorf("manager not initialized")
	}
	runtime := m.runtimeConfig
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	providerName, model, endpoint, apiKey := runtime.providerName, runtime.model, runtime.endpoint, runtime.apiKey
	if overrides.Provider != "" {
		registration, err := m.providerRegistration(overrides.Provider)
		if err != nil {
			return err
		}
		providerName = overrides.Provider
		model = configuredModel(m.config, providerName, registration.DefaultModel)
		endpoint = configuredEndpoint(m.config, providerName, registration.DefaultEndpoint)
		storedKey, err := m.credStore.GetAPIKey(providerName)
		if err != nil {
			return err
		}
		apiKey = storedKey
	}
	if overrides.Model != "" {
		model = overrides.Model
	}
	if overrides.Endpoint != "" {
		endpoint = overrides.Endpoint
	}
	if overrides.APIKey != "" {
		apiKey = overrides.APIKey
	}
	registration, err := m.providerRegistration(providerName)
	if err != nil {
		return err
	}
	if err := validateProviderEndpoint(registration, endpoint); err != nil {
		return err
	}
	client, err := m.createClient(ctx, providerName, apiKey, model, endpoint)
	if err != nil {
		return err
	}
	runtime.providerName, runtime.model, runtime.endpoint, runtime.apiKey, runtime.activeClient = providerName, model, endpoint, apiKey, client
	if m.metadataCache != nil {
		runtime.metadata = m.metadataCache.Providers[cacheKey(providerName, endpoint)]
	}
	return nil
}
