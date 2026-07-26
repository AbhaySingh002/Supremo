package providers

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/storage"
	"gopkg.in/yaml.v3"
)

const metadataFileName = "provider-metadata.yaml"

type metadataCache struct {
	Providers map[string]Metadata `yaml:"providers"`
}

func loadMetadataCache(dir string) (*metadataCache, error) {
	data, err := os.ReadFile(filepath.Join(dir, metadataFileName))
	if errors.Is(err, os.ErrNotExist) {
		return &metadataCache{Providers: make(map[string]Metadata)}, nil
	}
	if err != nil {
		return nil, err
	}
	var cache metadataCache
	if err := yaml.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if cache.Providers == nil {
		cache.Providers = make(map[string]Metadata)
	}
	return &cache, nil
}

func saveMetadataCache(dir string, cache *metadataCache) error {
	data, err := yaml.Marshal(cache)
	if err != nil {
		return err
	}
	return storage.WriteFileAtomic(filepath.Join(dir, metadataFileName), data, 0600)
}

func cacheKey(providerName, endpoint string) string {
	return providerName + "@" + strings.TrimRight(endpoint, "/")
}

func findModel(models []ModelInfo, id string) (ModelInfo, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return ModelInfo{}, false
}

func parsePrice(value string) (float64, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.ParseFloat(value, 64)
}
