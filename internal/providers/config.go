package providers

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/AbhaySingh002/supremo/internal/storage"
	"gopkg.in/yaml.v3"
)

const (
	configFileName     = "config.yaml"
	defaultGeminiModel = "gemini-3.6-flash"
)

// Config represents the persisted YAML configuration for Supremo.
type Config struct {
	ProviderName string            `yaml:"provider_name"`
	Model        string            `yaml:"model"`
	Endpoint     string            `yaml:"endpoint"`
	Models       map[string]string `yaml:"models,omitempty"`
	Endpoints    map[string]string `yaml:"endpoints,omitempty"`
}

func (c *Config) normalize() {
	if c.Models == nil {
		c.Models = make(map[string]string)
	}
	if c.Endpoints == nil {
		c.Endpoints = make(map[string]string)
	}
	if _, exists := c.Models[c.ProviderName]; !exists {
		c.Models[c.ProviderName] = c.Model
	}
	if _, exists := c.Endpoints[c.ProviderName]; !exists {
		c.Endpoints[c.ProviderName] = c.Endpoint
	}
	c.Model = c.Models[c.ProviderName]
	c.Endpoint = c.Endpoints[c.ProviderName]
}

// EnsureConfigDir creates the config directory path if it does not exist.
func EnsureConfigDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// LoadConfig reads config.yaml from the given directory. Creates default if missing.
func LoadConfig(dir string) (*Config, error) {
	path := filepath.Join(dir, configFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			defaultCfg := &Config{
				ProviderName: "gemini",
				Model:        defaultGeminiModel,
				Endpoint:     "",
			}
			if err := SaveConfig(dir, defaultCfg); err != nil {
				return nil, err
			}
			return defaultCfg, nil
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.normalize()
	return &cfg, nil
}

// SaveConfig writes the active configuration to config.yaml.
func SaveConfig(dir string, cfg *Config) error {
	cfg.normalize()
	path := filepath.Join(dir, configFileName)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return storage.WriteFileAtomic(path, data, 0600)
}
