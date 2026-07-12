package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const (
	credentialsFileName = "credentials.json"
)

// Credentials represents the persisted API key structure.
type Credentials struct {
	APIKeys map[string]string `json:"api_keys"`
}

// CredentialStore defines the interface for key managers.
type CredentialStore interface {
	GetAPIKey(ctx context.Context, provider string) (string, error)
	SetAPIKey(ctx context.Context, provider string, apiKey string) error
}

// FileCredentialStore implements CredentialStore using local JSON serialization.
type FileCredentialStore struct {
	mu       sync.Mutex
	filePath string
}

// NewFileCredentialStore constructs a new FileCredentialStore.
func NewFileCredentialStore(dir string) *FileCredentialStore {
	return &FileCredentialStore{
		filePath: filepath.Join(dir, credentialsFileName),
	}
}

// GetAPIKey reads the key for a provider.
func (s *FileCredentialStore) GetAPIKey(ctx context.Context, provider string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	creds, err := s.load()
	if err != nil {
		return "", err
	}

	apiKey, exists := creds.APIKeys[provider]
	if !exists {
		return "", errors.New("API key not found")
	}
	return apiKey, nil
}

// SetAPIKey writes the key for a provider.
func (s *FileCredentialStore) SetAPIKey(ctx context.Context, provider string, apiKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	creds, err := s.load()
	if err != nil {
		creds = &Credentials{APIKeys: make(map[string]string)}
	}

	if creds.APIKeys == nil {
		creds.APIKeys = make(map[string]string)
	}
	creds.APIKeys[provider] = apiKey

	return s.save(creds)
}

func (s *FileCredentialStore) load() (*Credentials, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			defaultCreds := &Credentials{
				APIKeys: map[string]string{
					"gemini": "YOUR_GEMINI_API_KEY",
				},
			}
			// Write the default file to disk
			if err := s.save(defaultCreds); err != nil {
				return nil, err
			}
			return defaultCreds, nil
		}
		return nil, err
	}

	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

func (s *FileCredentialStore) save(creds *Credentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
}
