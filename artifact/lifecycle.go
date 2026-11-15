package artifact

import (
	"context"
	"fmt"
	"time"
)

// RetentionPolicy resolves artifact lifetime with deterministic precedence:
// workflow override, tenant default, then the global default. A zero duration
// means no automatic expiry at that level.
type RetentionPolicy struct {
	Default   time.Duration
	Tenants   map[string]time.Duration
	Workflows map[string]time.Duration
}

// Resolve returns the effective retention for one artifact write.
func (p RetentionPolicy) Resolve(tenantID, workflowID string) time.Duration {
	if retention, ok := p.Workflows[workflowID]; ok {
		return retention
	}
	if retention, ok := p.Tenants[tenantID]; ok {
		return retention
	}
	return p.Default
}

// Clone returns a policy whose override maps cannot be mutated through the
// original configuration value.
func (p RetentionPolicy) Clone() RetentionPolicy {
	return RetentionPolicy{
		Default: p.Default, Tenants: cloneRetentionMap(p.Tenants),
		Workflows: cloneRetentionMap(p.Workflows),
	}
}

func cloneRetentionMap(values map[string]time.Duration) map[string]time.Duration {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]time.Duration, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// Validate rejects unsafe identifiers and negative durations.
func (p RetentionPolicy) Validate() error {
	if p.Default < 0 {
		return fmt.Errorf("artifact retention: default must not be negative")
	}
	if err := validateRetentionOverrides("tenant", p.Tenants); err != nil {
		return err
	}
	return validateRetentionOverrides("workflow", p.Workflows)
}

func validateRetentionOverrides(kind string, overrides map[string]time.Duration) error {
	for id, retention := range overrides {
		if err := validateIdentifier(kind+"_id", id); err != nil {
			return fmt.Errorf("artifact retention: %w", err)
		}
		if retention < 0 {
			return fmt.Errorf("artifact retention: %s %q must not be negative", kind, id)
		}
	}
	return nil
}

// LifecycleStore exposes bounded, tenant-scoped garbage-collection
// primitives. Callers must determine that a candidate is unreferenced before
// DeleteExpired. The conditional delete makes a concurrent expiry extension
// safe: it returns false instead of deleting the artifact.
type LifecycleStore interface {
	Store
	ListExpired(ctx context.Context, before time.Time, tenantID string, offset, limit int) ([]Metadata, error)
	DeleteExpired(ctx context.Context, metadata Metadata, before time.Time) (bool, error)
}

// GCObservation is the bounded aggregate result of one collection pass.
type GCObservation struct {
	Candidates   int64
	Deleted      int64
	Retained     int64
	BytesDeleted int64
	Errors       int64
}

// GCObserver exports garbage-collection counts and bytes.
type GCObserver interface {
	ObserveArtifactGC(context.Context, GCObservation)
}
