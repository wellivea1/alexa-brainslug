// Package providerauth persists dashboard-managed provider API keys in one
// private local file. It deliberately contains no OAuth logic; account tokens
// remain owned by their provider-specific authentication managers.
package providerauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/wellivea1/alexa-brainslug/internal/securefile"
)

const (
	credentialVersion = 1
	credentialTypeAPI = "api"
)

type credential struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

type credentialFile struct {
	Version     int                   `json:"version"`
	Credentials map[string]credential `json:"credentials"`
}

// Store owns the provider credential file. Keys are returned only to the
// inference manager and are never exposed through a snapshot or HTTP view.
type Store struct {
	mu          sync.RWMutex
	path        string
	credentials map[string]credential
}

// New loads path when it exists. An empty path creates a readable store whose
// mutation methods fail closed because credentials could not be persisted.
func New(path string) (*Store, error) {
	s := &Store{
		path:        strings.TrimSpace(path),
		credentials: make(map[string]credential),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Get returns a managed API key without revealing any other credential.
func (s *Store) Get(provider string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, ok := s.credentials[normalizeProvider(provider)]
	return entry.Key, ok && entry.Type == credentialTypeAPI && strings.TrimSpace(entry.Key) != ""
}

// Set durably stores one provider API key.
func (s *Store) Set(provider, key string) error {
	provider = normalizeProvider(provider)
	key = strings.TrimSpace(key)
	if provider == "" {
		return errors.New("provider is required")
	}
	if key == "" {
		return errors.New("API key is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return errors.New("provider credential storage is unavailable")
	}
	next := cloneCredentials(s.credentials)
	next[provider] = credential{Type: credentialTypeAPI, Key: key}
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.credentials = next
	return nil
}

// Remove deletes only the named provider credential.
func (s *Store) Remove(provider string) error {
	provider = normalizeProvider(provider)
	if provider == "" {
		return errors.New("provider is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return errors.New("provider credential storage is unavailable")
	}
	next := cloneCredentials(s.credentials)
	delete(next, provider)
	if err := s.writeLocked(next); err != nil {
		return err
	}
	s.credentials = next
	return nil
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read provider credential file: %w", err)
	}
	var stored credentialFile
	if err := securefile.DecodeJSON(data, &stored); err != nil {
		return fmt.Errorf("decode provider credential file: %w", err)
	}
	if stored.Version != credentialVersion {
		return fmt.Errorf("provider credential version must be %d", credentialVersion)
	}
	for provider, entry := range stored.Credentials {
		provider = normalizeProvider(provider)
		if provider == "" || entry.Type != credentialTypeAPI || strings.TrimSpace(entry.Key) == "" {
			return errors.New("provider credential file contains an invalid entry")
		}
		s.credentials[provider] = credential{Type: credentialTypeAPI, Key: strings.TrimSpace(entry.Key)}
	}
	return nil
}

func (s *Store) writeLocked(credentials map[string]credential) error {
	stored := credentialFile{Version: credentialVersion, Credentials: credentials}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode provider credentials: %w", err)
	}
	if err := securefile.Write(s.path, append(data, '\n')); err != nil {
		return fmt.Errorf("save provider credential file: %w", err)
	}
	return nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func cloneCredentials(source map[string]credential) map[string]credential {
	cloned := make(map[string]credential, len(source))
	for provider, entry := range source {
		cloned[provider] = entry
	}
	return cloned
}
