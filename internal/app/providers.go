package app

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/providers"
)

// buildProviderRegistry creates and populates the Provider Factory Registry with all standard adapters.
func buildProviderRegistry() (*providers.Registry, error) {
	reg := providers.NewRegistry()
	if err := providers.RegisterBuiltins(reg); err != nil {
		return nil, err
	}
	return reg, nil
}

// buildProviderManager initializes the Provider Manager with the explicit registry and credential store.
func buildProviderManager(ctx context.Context, configDir string, credStore *providers.FileCredentialStore, overrides providers.RuntimeOverrides) (*providers.Manager, error) {
	registry, err := buildProviderRegistry()
	if err != nil {
		return nil, err
	}
	manager, err := providers.NewManager(configDir, credStore, registry)
	if err != nil {
		return nil, err
	}
	if err := manager.Initialize(ctx); err != nil {
		return nil, err
	}
	if err := manager.ApplyRuntimeOverrides(ctx, overrides); err != nil {
		return nil, err
	}
	return manager, nil
}
