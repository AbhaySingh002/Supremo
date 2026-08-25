package providers

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func textGenerationModels(models []ModelInfo) []ModelInfo {
	result := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		if isTextGenerationModel(model) {
			result = append(result, model)
		}
	}
	slices.SortStableFunc(result, func(a, b ModelInfo) int { return strings.Compare(a.ID, b.ID) })
	return result
}

func isTextGenerationModel(model ModelInfo) bool {
	if model.ModalitiesKnown && (!model.AcceptsText || !model.ProducesText) {
		return false
	}
	name := strings.ToLower(model.ID + " " + model.Name)
	for _, blocked := range []string{
		"embedding", "embed-", "embed_", "dall-e", "image", "imagen", "audio", "speech",
		"tts", "whisper", "transcri", "moderation", "rerank", "realtime",
	} {
		if strings.Contains(name, blocked) {
			return false
		}
	}
	return strings.TrimSpace(model.ID) != ""
}
