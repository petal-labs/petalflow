package server

import (
	"context"
	"encoding/json"
	"errors"
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
