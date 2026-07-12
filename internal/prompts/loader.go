package prompts

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Loader handles loading and caching of markdown prompt templates.
type Loader struct {
	mu          sync.RWMutex
	templateDir string
	cache       map[string]string
}

// NewLoader creates a new Loader instance with the specified template directory.
func NewLoader(templateDir string) *Loader {
	return &Loader{
		templateDir: templateDir,
		cache:       make(map[string]string),
	}
}

// Load retrieves a template by name from cache, or reads it from disk if not cached.
func (l *Loader) Load(name string) (string, error) {
	l.mu.RLock()
	content, cached := l.cache[name]
	l.mu.RUnlock()
	if cached {
		return content, nil
	}

	// Double check lock / load
	l.mu.Lock()
	defer l.mu.Unlock()

	// Recheck cache
	if content, cached = l.cache[name]; cached {
		return content, nil
	}

	path := filepath.Join(l.templateDir, name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read template %q at %s: %w", name, path, err)
	}

	content = string(data)
	l.cache[name] = content
	return content, nil
}

// Reload clears the cache and reloads all currently cached files (or optionally just refreshes everything in the directory).
func (l *Loader) Reload() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Clear cache
	l.cache = make(map[string]string)

	// Pre-populate cache with templates found in templateDir
	files, err := os.ReadDir(l.templateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read template directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".md" {
			continue
		}
		name := file.Name()[:len(file.Name())-len(".md")]
		path := filepath.Join(l.templateDir, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to reload template %q: %w", name, err)
		}
		l.cache[name] = string(data)
	}

	return nil
}

// Names returns a list of loaded/available template names.
func (l *Loader) Names() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	names := make([]string, 0, len(l.cache))
	for name := range l.cache {
		names = append(names, name)
	}
	return names
}
