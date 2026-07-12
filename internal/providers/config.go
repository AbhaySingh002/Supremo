package providers

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	configFileName = "config.yaml"
)

// Config represents the persisted YAML configuration for Supremo.
type Config struct {
	ProviderName string            `yaml:"provider_name"`
	Model        string            `yaml:"model"`
	Endpoint     string            `yaml:"endpoint"`
	Settings     map[string]string `yaml:"settings,omitempty"`
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
				Model:        "gemini-2.5-flash",
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
	return &cfg, nil
}

// SaveConfig writes the active configuration to config.yaml.
func SaveConfig(dir string, cfg *Config) error {
	path := filepath.Join(dir, configFileName)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
