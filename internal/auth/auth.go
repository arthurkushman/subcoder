package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/arthurkushman/subcoder/internal/config"
)

type Entry struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

type Store struct {
	path string
}

func NewStore(dataDir string) *Store {
	if dataDir == "" {
		dataDir = config.DataDir()
	}
	return &Store{path: filepath.Join(dataDir, "auth.json")}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) load() (map[string]Entry, error) {
	entries := map[string]Entry{}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return entries, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return entries, nil
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) save(entries map[string]Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Set(provider string, entry Entry) error {
	entries, err := s.load()
	if err != nil {
		return err
	}
	if entry.Type == "" {
		entry.Type = "api"
	}
	entries[provider] = entry
	return s.save(entries)
}

func (s *Store) Get(provider string) (Entry, bool) {
	entries, err := s.load()
	if err != nil {
		return Entry{}, false
	}
	entry, ok := entries[provider]
	return entry, ok
}

func (s *Store) Delete(provider string) error {
	entries, err := s.load()
	if err != nil {
		return err
	}
	delete(entries, provider)
	return s.save(entries)
}

func (s *Store) List() (map[string]Entry, error) {
	return s.load()
}

func (s *Store) Providers() []string {
	entries, err := s.load()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Key resolves an API key from the auth store, the provider config, or the
// SUBCODER_<PROVIDER>_API_KEY environment variable.
func (s *Store) Key(provider string, cfg config.ProviderConfig) string {
	if cfg.APIKey != "" {
		return cfg.APIKey
	}
	if entry, ok := s.Get(provider); ok && entry.Key != "" {
		return entry.Key
	}
	if v := os.Getenv(config.EnvKey(provider)); v != "" {
		return v
	}
	switch provider {
	case "openai":
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			return v
		}
	case "anthropic":
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
			return v
		}
	}
	return ""
}
