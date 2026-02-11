package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	stateStoreVersionV1 = "1"
	defaultStateDir     = ".petalflow"
	defaultStateDB      = "server_state.json"
)

var errEmptyStateStorePath = errors.New("server: file state store path is empty")

type serverState struct {
	AuthUser *authAccount `json:"auth_user,omitempty"`
	Settings AppSettings  `json:"settings"`
}

type stateStoreDocument struct {
	Version string      `json:"version"`
	State   serverState `json:"state"`
}

// FileStateStore persists auth and application settings in a local JSON file.
type FileStateStore struct {
	path string
	mu   sync.RWMutex
}

// NewFileStateStore creates a state store at the given path.
func NewFileStateStore(path string) *FileStateStore {
	return &FileStateStore{path: path}
}

// NewDefaultFileStateStore creates a state store at ~/.petalflow/server_state.json.
func NewDefaultFileStateStore() (*FileStateStore, error) {
	path, err := DefaultStateStorePath()
	if err != nil {
		return nil, err
	}
	return NewFileStateStore(path), nil
}

// DefaultStateStorePath returns the default state file path.
func DefaultStateStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("server: resolve user home: %w", err)
	}
	return filepath.Join(home, defaultStateDir, defaultStateDB), nil
}

// Path returns the backing file path.
func (s *FileStateStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Load returns persisted state. Missing files return an empty state.
func (s *FileStateStore) Load() (serverState, error) {
	if s == nil {
		return serverState{}, errors.New("server: file state store is nil")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.load()
}

// Save writes state atomically.
func (s *FileStateStore) Save(state serverState) error {
	if s == nil {
		return errors.New("server: file state store is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.save(state)
}

func (s *FileStateStore) load() (serverState, error) {
	if strings.TrimSpace(s.path) == "" {
		return serverState{}, errEmptyStateStorePath
	}

	// #nosec G304 -- path is configured by caller and constrained to local filesystem usage.
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return serverState{}, nil
		}
		return serverState{}, fmt.Errorf("server: read state: %w", err)
	}
	if len(data) == 0 {
		return serverState{}, nil
	}

	var doc stateStoreDocument
	if err := json.Unmarshal(data, &doc); err == nil {
		if strings.TrimSpace(doc.Version) != "" || doc.State.AuthUser != nil || doc.State.Settings != (AppSettings{}) {
			doc.State.AuthUser = cloneAuthAccount(doc.State.AuthUser)
			doc.State.Settings = normalizeAppSettings(doc.State.Settings)
			return doc.State, nil
		}
	}

	// Backward compatibility: allow plain serverState payloads.
	var state serverState
	if err := json.Unmarshal(data, &state); err != nil {
		return serverState{}, fmt.Errorf("server: decode state: %w", err)
	}
	state.AuthUser = cloneAuthAccount(state.AuthUser)
	state.Settings = normalizeAppSettings(state.Settings)
	return state, nil
}

func (s *FileStateStore) save(state serverState) error {
	if strings.TrimSpace(s.path) == "" {
		return errEmptyStateStorePath
	}

	state.AuthUser = cloneAuthAccount(state.AuthUser)
	state.Settings = normalizeAppSettings(state.Settings)

	doc := stateStoreDocument{
		Version: stateStoreVersionV1,
		State:   state,
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("server: encode state: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("server: create state store dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("server: write temp state file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("server: replace state file: %w", err)
	}
	return nil
}
