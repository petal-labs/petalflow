package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/core"
)

// RunStatus is the durable lifecycle state of a workflow run.
type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusPaused    RunStatus = "paused"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCanceled  RunStatus = "canceled"
)

var (
	ErrRunNotFound       = errors.New("run not found")
	ErrRunExists         = errors.New("run already exists")
	ErrRunAlreadySettled = errors.New("run is already settled")
	ErrPendingNotFound   = errors.New("pending action not found")
	ErrPendingCompleted  = errors.New("pending action already completed")
	ErrHumanPending      = errors.New("run is waiting for human input")
)

// PendingError marks an error that pauses a run instead of failing it. Human
// interaction adapters implement this small interface without coupling nodes
// to the runtime's persistence implementation.
type PendingError interface {
	error
	Pending() bool
}

// Checkpoint is the smallest safe recovery point for a sequential run. Queue
// contains the node to execute next and is persisted before a node starts, so
// a worker crash never causes the runtime to skip an uncommitted node.
type Checkpoint struct {
	ID        string            `json:"id"`
	RunID     string            `json:"run_id"`
	CreatedAt time.Time         `json:"created_at"`
	Envelope  *core.Envelope    `json:"envelope,omitempty"`
	Queue     []string          `json:"queue"`
	Visited   map[string]bool   `json:"visited,omitempty"`
	HopCount  map[string]int    `json:"hop_count,omitempty"`
	NodeKeys  map[string]string `json:"node_keys,omitempty"`
}

// PendingAction is a durable human interaction. Data and Response are kept as
// JSON-compatible values by persistence implementations; callers should avoid
// putting credentials or full sensitive prompts in them.
type PendingAction struct {
	ID          string         `json:"id"`
	RunID       string         `json:"run_id"`
	NodeID      string         `json:"node_id"`
	Type        string         `json:"type"`
	Prompt      string         `json:"prompt,omitempty"`
	Data        any            `json:"data,omitempty"`
	Options     any            `json:"options,omitempty"`
	Schema      map[string]any `json:"schema,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	Response    any            `json:"response,omitempty"`
	RespondedAt time.Time      `json:"responded_at,omitempty"`
}

// RunRecord is the durable state of a run. Records are updated with
// compare-and-preserve semantics by stores: cancellation cannot be replaced
// by a late worker result.
type RunRecord struct {
	ID              string         `json:"id"`
	WorkflowID      string         `json:"workflow_id,omitempty"`
	IdempotencyKey  string         `json:"idempotency_key,omitempty"`
	GraphName       string         `json:"graph_name,omitempty"`
	Status          RunStatus      `json:"status"`
	StartedAt       time.Time      `json:"started_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	CompletedAt     time.Time      `json:"completed_at,omitempty"`
	Error           string         `json:"error,omitempty"`
	CancelRequested bool           `json:"cancel_requested,omitempty"`
	LastSeq         uint64         `json:"last_seq,omitempty"`
	Checkpoint      *Checkpoint    `json:"checkpoint,omitempty"`
	PendingAction   *PendingAction `json:"pending_action,omitempty"`
}

// RunStore persists run records and checkpoints.
type RunStore interface {
	Create(ctx context.Context, record *RunRecord) error
	Get(ctx context.Context, runID string) (*RunRecord, error)
	Update(ctx context.Context, record *RunRecord) error
	Cancel(ctx context.Context, runID string) error
}

// PendingActionCompleter provides an atomic exactly-once completion operation.
// It is optional so embedded applications can implement RunStore minimally.
type PendingActionCompleter interface {
	CompletePendingAction(ctx context.Context, runID, actionID string, response any) (*RunRecord, error)
}

// MemoryRunStore is a concurrency-safe store useful for embedded runtimes and
// tests. A process restart requires a durable implementation such as a
// database-backed store.
type MemoryRunStore struct {
	mu      sync.RWMutex
	records map[string]*RunRecord
}

func NewMemoryRunStore() *MemoryRunStore {
	return &MemoryRunStore{records: make(map[string]*RunRecord)}
}

func (s *MemoryRunStore) Create(_ context.Context, record *RunRecord) error {
	if record == nil || record.ID == "" {
		return errors.New("run record ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[record.ID]; exists {
		return ErrRunExists
	}
	s.records[record.ID] = cloneRunRecord(record)
	return nil
}

func (s *MemoryRunStore) Get(_ context.Context, runID string) (*RunRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[runID]
	if !ok {
		return nil, ErrRunNotFound
	}
	return cloneRunRecord(record), nil
}

func (s *MemoryRunStore) Update(_ context.Context, record *RunRecord) error {
	if record == nil || record.ID == "" {
		return errors.New("run record ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.records[record.ID]
	if !exists {
		return ErrRunNotFound
	}
	if current.CancelRequested {
		preserved := cloneRunRecord(current)
		preserved.UpdatedAt = record.UpdatedAt
		s.records[record.ID] = preserved
		return nil
	}
	s.records[record.ID] = cloneRunRecord(record)
	return nil
}

func (s *MemoryRunStore) Cancel(_ context.Context, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[runID]
	if !ok {
		return ErrRunNotFound
	}
	if isTerminal(record.Status) {
		return ErrRunAlreadySettled
	}
	record.CancelRequested = true
	record.Status = RunStatusCanceled
	record.UpdatedAt = time.Now().UTC()
	record.CompletedAt = record.UpdatedAt
	return nil
}

func (s *MemoryRunStore) CompletePendingAction(_ context.Context, runID, actionID string, response any) (*RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[runID]
	if !ok {
		return nil, ErrRunNotFound
	}
	if isTerminal(record.Status) {
		return nil, ErrRunAlreadySettled
	}
	if record.PendingAction == nil || record.PendingAction.ID != actionID {
		return nil, ErrPendingNotFound
	}
	if record.PendingAction.Response != nil {
		return nil, ErrPendingCompleted
	}
	pending := *record.PendingAction
	pending.Response = response
	pending.RespondedAt = time.Now().UTC()
	record.PendingAction = &pending
	record.UpdatedAt = pending.RespondedAt
	return cloneRunRecord(record), nil
}

// IDs returns the stored run IDs in no guaranteed order.
func (s *MemoryRunStore) IDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.records))
	for id := range s.records {
		ids = append(ids, id)
	}
	return ids
}

func isTerminal(status RunStatus) bool {
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusCanceled
}

func cloneRunRecord(record *RunRecord) *RunRecord {
	if record == nil {
		return nil
	}
	clone := *record
	if record.Checkpoint != nil {
		checkpoint := *record.Checkpoint
		checkpoint.Queue = append([]string(nil), record.Checkpoint.Queue...)
		checkpoint.Visited = cloneBoolMap(record.Checkpoint.Visited)
		checkpoint.HopCount = cloneIntMap(record.Checkpoint.HopCount)
		checkpoint.NodeKeys = cloneStringMap(record.Checkpoint.NodeKeys)
		checkpoint.Envelope = record.Checkpoint.Envelope.Clone()
		clone.Checkpoint = &checkpoint
	}
	if record.PendingAction != nil {
		pending := *record.PendingAction
		pending.Schema = cloneAnyMap(record.PendingAction.Schema)
		clone.PendingAction = &pending
	}
	return &clone
}

func cloneBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneIntMap(source map[string]int) map[string]int {
	if source == nil {
		return nil
	}
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneAnyMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
