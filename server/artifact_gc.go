package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/petal-labs/petalflow/artifact"
	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/tool"
)

const (
	defaultArtifactGCBatchSize = 100
	defaultArtifactGCInterval  = time.Hour
	// ArtifactAuditDeleted is the stable audit decision for a GC deletion.
	ArtifactAuditDeleted = "deleted"
	artifactAuditError   = "error"
)

// ArtifactGCObservation is the bounded result of one garbage-collection pass.
type ArtifactGCObservation = artifact.GCObservation

// ArtifactGCObserver exports garbage-collection counts and bytes.
type ArtifactGCObserver = artifact.GCObserver

// ArtifactCollectorConfig configures reference-aware artifact collection.
type ArtifactCollectorConfig struct {
	Store      artifact.LifecycleStore
	RunStore   runtime.RunStore
	EventStore bus.EventStore
	AuditStore tool.ToolAuditStore
	Observer   ArtifactGCObserver
	TenantID   string
	BatchSize  int
	Interval   time.Duration
	Now        func() time.Time
	Logger     *slog.Logger
}

// ArtifactCollector prunes expired artifacts only after proving they are not
// referenced by a protected run record, checkpoint, pending action, or event.
type ArtifactCollector struct {
	config ArtifactCollectorConfig
	mu     sync.Mutex
	runMu  sync.Mutex
	offset int
	cancel context.CancelFunc
	done   chan struct{}
}

// NewArtifactCollector validates and normalizes collector configuration.
func NewArtifactCollector(config ArtifactCollectorConfig) (*ArtifactCollector, error) {
	if config.Store == nil || config.RunStore == nil || config.EventStore == nil {
		return nil, errors.New("artifact collector: artifact, run, and event stores are required")
	}
	if config.BatchSize < 0 || config.BatchSize > 10_000 || config.Interval < 0 {
		return nil, errors.New("artifact collector: batch size must not exceed 10000 and interval must not be negative")
	}
	if config.BatchSize == 0 {
		config.BatchSize = defaultArtifactGCBatchSize
	}
	if config.Interval == 0 {
		config.Interval = defaultArtifactGCInterval
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &ArtifactCollector{config: config}, nil
}

// Start launches the background collection loop. The first pass runs
// immediately so expired data does not wait a full interval after startup.
func (c *ArtifactCollector) Start(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.done = make(chan struct{})
	go c.loop(loopCtx, c.done)
}

// Stop stops the background loop and waits for its current pass.
func (c *ArtifactCollector) Stop(ctx context.Context) error {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.cancel, c.done = nil, nil
	c.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *ArtifactCollector) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	c.runAndLog(ctx)
	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runAndLog(ctx)
		}
	}
}

func (c *ArtifactCollector) runAndLog(ctx context.Context) {
	stats, err := c.RunOnce(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		c.config.Logger.Error("artifact garbage collection failed", "errors", stats.Errors, "error", err)
	}
	if stats.Deleted > 0 {
		c.config.Logger.Info("artifact garbage collection completed", "deleted", stats.Deleted, "bytes", stats.BytesDeleted)
	}
}

// RunOnce performs one bounded garbage-collection pass.
func (c *ArtifactCollector) RunOnce(ctx context.Context) (ArtifactGCObservation, error) {
	c.runMu.Lock()
	defer c.runMu.Unlock()
	cutoff := c.config.Now().UTC()
	candidates, err := c.config.Store.ListExpired(ctx, cutoff, c.config.TenantID, c.offset, c.config.BatchSize)
	stats := ArtifactGCObservation{Candidates: int64(len(candidates))}
	if err != nil {
		stats.Errors = 1
		c.observe(ctx, stats)
		return stats, fmt.Errorf("artifact collector list candidates: %w", err)
	}
	if len(candidates) < c.config.BatchSize {
		c.offset = 0
	} else {
		c.offset += len(candidates)
	}
	var errs []error
	for _, candidate := range candidates {
		retained, candidateErr := c.candidateReferenced(ctx, candidate)
		if candidateErr != nil {
			stats.Errors++
			errs = append(errs, candidateErr)
			c.audit(candidate, tool.ToolAuditFailure, artifactAuditError, "reference_check_failed")
			continue
		}
		if retained {
			stats.Retained++
			continue
		}
		deleted, deleteErr := c.config.Store.DeleteExpired(ctx, candidate, cutoff)
		if deleteErr != nil {
			stats.Errors++
			errs = append(errs, deleteErr)
			c.audit(candidate, tool.ToolAuditFailure, artifactAuditError, "delete_failed")
			continue
		}
		if deleted {
			stats.Deleted++
			stats.BytesDeleted += candidate.Reference.Size
			c.audit(candidate, tool.ToolAuditInvocation, ArtifactAuditDeleted, "expired_unreferenced")
		} else {
			stats.Retained++
		}
	}
	c.observe(ctx, stats)
	return stats, errors.Join(errs...)
}

func (c *ArtifactCollector) candidateReferenced(ctx context.Context, metadata artifact.Metadata) (bool, error) {
	record, err := c.config.RunStore.Get(ctx, metadata.Scope.RunID)
	if errors.Is(err, runtime.ErrRunNotFound) {
		return false, nil
	}
	if err != nil {
		return true, fmt.Errorf("artifact collector get run: %w", err)
	}
	if record.TenantID != metadata.Scope.TenantID {
		return true, errors.New("artifact collector: artifact and run tenant mismatch")
	}
	if runProtectsAllArtifacts(record) {
		return true, nil
	}
	if containsArtifactReference(record, metadata.Reference) {
		return true, nil
	}
	events, err := c.config.EventStore.List(ctx, record.ID, 0, 0)
	if err != nil {
		return true, fmt.Errorf("artifact collector list events: %w", err)
	}
	return containsArtifactReference(events, metadata.Reference), nil
}

func runProtectsAllArtifacts(record *runtime.RunRecord) bool {
	if record.Status == runtime.RunStatusRunning || record.Status == runtime.RunStatusPaused {
		return true
	}
	return record.PendingAction != nil && !record.PendingAction.Completed
}

func containsArtifactReference(value any, reference artifact.Reference) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return true
	}
	for _, token := range []string{reference.Digest, artifact.ReferenceID(reference), artifact.ReferenceURI(reference)} {
		if bytes.Contains(encoded, []byte(token)) {
			return true
		}
	}
	return false
}

func (c *ArtifactCollector) observe(ctx context.Context, stats ArtifactGCObservation) {
	if c.config.Observer != nil {
		c.config.Observer.ObserveArtifactGC(ctx, stats)
	}
}

func (c *ArtifactCollector) audit(metadata artifact.Metadata, kind tool.ToolAuditKind, decision, reason string) {
	if c.config.AuditStore == nil {
		return
	}
	record := tool.ToolAuditRecord{
		ID: uuid.NewString(), TenantID: metadata.Scope.TenantID,
		WorkflowID: metadata.WorkflowID, RunID: metadata.Scope.RunID, NodeID: metadata.NodeID,
		Actor: "system", Tool: "artifact_gc", Action: "delete", Kind: kind,
		Decision: decision, Reason: reason, CreatedAt: c.config.Now().UTC(),
	}
	if err := c.config.AuditStore.Append(context.Background(), record); err != nil {
		c.config.Logger.Error("recording artifact garbage collection audit", "error", err)
	}
}
