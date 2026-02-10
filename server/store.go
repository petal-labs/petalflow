package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/loader"
)

// Sentinel errors for store operations.
var (
	ErrWorkflowExists   = errors.New("workflow already exists")
	ErrWorkflowNotFound = errors.New("workflow not found")
)

// WorkflowRecord represents a stored workflow.
type WorkflowRecord struct {
	ID         string                 `json:"id"`
	SchemaKind loader.SchemaKind      `json:"kind"`
	Name       string                 `json:"name,omitempty"`
	Source     json.RawMessage        `json:"source"`
	Compiled   *graph.GraphDefinition `json:"compiled,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// WorkflowStore provides CRUD operations for workflow records.
type WorkflowStore interface {
	List(ctx context.Context) ([]WorkflowRecord, error)
	Get(ctx context.Context, id string) (WorkflowRecord, bool, error)
	Create(ctx context.Context, rec WorkflowRecord) error
	Update(ctx context.Context, rec WorkflowRecord) error
	Delete(ctx context.Context, id string) error
}

// MemoryStore is a thread-safe in-memory WorkflowStore.
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]WorkflowRecord
	order []string // insertion order for List
}

// NewMemoryStore creates a new in-memory workflow store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items: make(map[string]WorkflowRecord),
	}
}

func (s *MemoryStore) List(_ context.Context) ([]WorkflowRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]WorkflowRecord, 0, len(s.order))
	for _, id := range s.order {
		result = append(result, s.items[id])
	}
	return result, nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (WorkflowRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.items[id]
	return rec, ok, nil
}

func (s *MemoryStore) Create(_ context.Context, rec WorkflowRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.items[rec.ID]; exists {
		return ErrWorkflowExists
	}
	s.items[rec.ID] = rec
	s.order = append(s.order, rec.ID)
	return nil
}

func (s *MemoryStore) Update(_ context.Context, rec WorkflowRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.items[rec.ID]; !exists {
		return ErrWorkflowNotFound
	}
	s.items[rec.ID] = rec
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.items[id]; !exists {
		return ErrWorkflowNotFound
	}
	delete(s.items, id)
	// Remove from order slice
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}

// Compile-time interface check.
var _ WorkflowStore = (*MemoryStore)(nil)
