package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/loader"
	"github.com/petal-labs/petalflow/tool"
)

// Sentinel errors for store operations.
var (
	ErrWorkflowExists   = errors.New("workflow already exists")
	ErrWorkflowNotFound = errors.New("workflow not found")

	// ErrTenantPolicyNotFound is returned when a tenant has no stored
	// tool policy override.
	ErrTenantPolicyNotFound = errors.New("tenant policy not found")
)

// WorkflowRecord represents a stored workflow.
type WorkflowRecord struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id,omitempty"`
	SchemaKind loader.SchemaKind      `json:"kind"`
	Name       string                 `json:"name,omitempty"`
	Source     json.RawMessage        `json:"source"`
	Compiled   *graph.GraphDefinition `json:"compiled,omitempty"`
	// Policy is the workflow-scoped tool policy override. nil means the
	// workflow follows the tenant default policy.
	Policy    *tool.ToolPolicy `json:"tool_policy,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// TenantPolicyReference scopes a stored tool policy to a tenant.
type TenantPolicyReference struct {
	TenantID string `json:"tenant_id"`
}

// TenantPolicyRecord is the stored tool policy for one tenant. The policy
// stored here overrides any statically configured default policy for that
// tenant.
type TenantPolicyRecord struct {
	TenantID  string          `json:"tenant_id"`
	Policy    tool.ToolPolicy `json:"policy"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// TenantPolicyStore provides tenant-scoped tool policy persistence.
type TenantPolicyStore interface {
	// GetTenantPolicy returns the stored policy for the tenant, if any.
	GetTenantPolicy(ctx context.Context, tenantID string) (TenantPolicyRecord, bool, error)
	// UpsertTenantPolicy stores the policy for the tenant. The stored
	// version is server-managed: equal content keeps the previous
	// version, changed content increments it. Callers should leave
	// Policy.Version unset.
	UpsertTenantPolicy(ctx context.Context, rec TenantPolicyRecord) (TenantPolicyRecord, error)
	// DeleteTenantPolicy removes the tenant override, restoring whatever
	// statically configured default applies.
	DeleteTenantPolicy(ctx context.Context, tenantID string) error
}

// WorkflowStore provides CRUD operations for workflow records.
type WorkflowStore interface {
	List(ctx context.Context) ([]WorkflowRecord, error)
	Get(ctx context.Context, id string) (WorkflowRecord, bool, error)
	Create(ctx context.Context, rec WorkflowRecord) error
	Update(ctx context.Context, rec WorkflowRecord) error
	Delete(ctx context.Context, id string) error
}
