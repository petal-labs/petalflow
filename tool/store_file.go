package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	fileStoreVersionV1 = "1"
	defaultCLIStoreDir = ".petalflow"
	defaultCLIStoreDB  = "tools.json"
)

var errEmptyStorePath = errors.New("tool: file store path is empty")

type fileStoreDocument struct {
	Version string             `json:"version"`
	Tools   []ToolRegistration `json:"tools"`
}

// FileStore persists tool registrations in a local JSON file.
// This store is intended for CLI mode.
type FileStore struct {
	path string
	mu   sync.RWMutex
}

// NewFileStore creates a file-backed registration store at the given path.
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// NewDefaultFileStore creates a CLI store at ~/.petalflow/tools.json.
func NewDefaultFileStore() (*FileStore, error) {
	path, err := DefaultCLIStorePath()
	if err != nil {
		return nil, err
	}
	return NewFileStore(path), nil
}

// DefaultCLIStorePath returns the default registration file path for CLI mode.
func DefaultCLIStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("tool: resolve user home: %w", err)
	}
	return filepath.Join(home, defaultCLIStoreDir, defaultCLIStoreDB), nil
}

// Path returns the backing file path.
func (s *FileStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// List returns all registrations in deterministic (name-sorted) order.
func (s *FileStore) List(ctx context.Context) ([]ToolRegistration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("tool: file store is nil")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	regs, err := s.load()
	if err != nil {
		return nil, err
	}
	return cloneRegistrations(regs), nil
}

// Get returns a registration by name.
func (s *FileStore) Get(ctx context.Context, name string) (ToolRegistration, bool, error) {
	if err := ctx.Err(); err != nil {
		return ToolRegistration{}, false, err
	}

	regs, err := s.List(ctx)
	if err != nil {
		return ToolRegistration{}, false, err
	}

	for _, reg := range regs {
		if reg.Name == name {
			return reg, true, nil
		}
	}
	return ToolRegistration{}, false, nil
}

// Upsert inserts or updates a registration by name.
func (s *FileStore) Upsert(ctx context.Context, reg ToolRegistration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return errors.New("tool: file store is nil")
	}
	if strings.TrimSpace(reg.Name) == "" {
		return errors.New("tool: registration name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	regs, err := s.load()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	index := -1
	for i := range regs {
		if regs[i].Name == reg.Name {
			index = i
			break
		}
	}

	if reg.Status == "" {
		reg.Status = StatusUnverified
	}
	if reg.RegisteredAt.IsZero() {
		if index >= 0 && !regs[index].RegisteredAt.IsZero() {
			reg.RegisteredAt = regs[index].RegisteredAt
		} else {
			reg.RegisteredAt = now
		}
	}
	if reg.LastHealthCheck.IsZero() && index >= 0 {
		reg.LastHealthCheck = regs[index].LastHealthCheck
	}

	if index >= 0 {
		regs[index] = reg
	} else {
		regs = append(regs, reg)
	}

	return s.save(regs)
}

// Delete removes a registration by name. Deleting a missing name is a no-op.
func (s *FileStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return errors.New("tool: file store is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	regs, err := s.load()
	if err != nil {
		return err
	}

	filtered := make([]ToolRegistration, 0, len(regs))
	for _, reg := range regs {
		if reg.Name != name {
			filtered = append(filtered, reg)
		}
	}
	return s.save(filtered)
}

func (s *FileStore) load() ([]ToolRegistration, error) {
	if strings.TrimSpace(s.path) == "" {
		return nil, errEmptyStorePath
	}

	// #nosec G304 -- path is configured by caller and constrained to local filesystem usage.
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ToolRegistration{}, nil
		}
		return nil, fmt.Errorf("tool: read registrations: %w", err)
	}
	if len(data) == 0 {
		return []ToolRegistration{}, nil
	}

	var doc fileStoreDocument
	if err := json.Unmarshal(data, &doc); err == nil && doc.Tools != nil {
		sortRegistrations(doc.Tools)
		return doc.Tools, nil
	}

	// Backward-compatibility: permit a plain array payload.
	var regs []ToolRegistration
	if err := json.Unmarshal(data, &regs); err != nil {
		return nil, fmt.Errorf("tool: decode registrations: %w", err)
	}
	sortRegistrations(regs)
	return regs, nil
}

func (s *FileStore) save(regs []ToolRegistration) error {
	if strings.TrimSpace(s.path) == "" {
		return errEmptyStorePath
	}

	regs = cloneRegistrations(regs)
	sortRegistrations(regs)

	doc := fileStoreDocument{
		Version: fileStoreVersionV1,
		Tools:   regs,
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("tool: encode registrations: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return fmt.Errorf("tool: create store dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("tool: write temp store file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("tool: replace store file: %w", err)
	}
	return nil
}

func sortRegistrations(regs []ToolRegistration) {
	slices.SortFunc(regs, func(a, b ToolRegistration) int {
		return strings.Compare(a.Name, b.Name)
	})
}

func cloneRegistrations(in []ToolRegistration) []ToolRegistration {
	out := make([]ToolRegistration, len(in))
	for i := range in {
		out[i] = cloneRegistration(in[i])
	}
	return out
}

func cloneRegistration(in ToolRegistration) ToolRegistration {
	out := in
	if in.Config != nil {
		out.Config = make(map[string]string, len(in.Config))
		for k, v := range in.Config {
			out.Config[k] = v
		}
	}
	if in.Overlay != nil {
		overlay := *in.Overlay
		out.Overlay = &overlay
	}
	return out
}

var _ Store = (*FileStore)(nil)
