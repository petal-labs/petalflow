package tool

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHealthSchedulerRunOnceHonorsPerToolInterval(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	manifest := NewManifest("interval_mcp")
	manifest.Transport = NewMCPTransport(MCPTransport{
		Mode:    MCPModeStdio,
		Command: "interval-mcp",
	})
	manifest.Health = &HealthConfig{
		IntervalSeconds:    5,
		UnhealthyThreshold: 1,
	}
	manifest.Actions["list"] = ActionSpec{
		Outputs: map[string]FieldSpec{
			"count": {Type: TypeInteger},
		},
	}
	seed := ToolRegistration{
		Name:     "interval_mcp",
		Origin:   OriginMCP,
		Manifest: manifest,
		Status:   StatusReady,
		Enabled:  true,
	}
	if err := store.Upsert(context.Background(), seed); err != nil {
		t.Fatalf("store.Upsert() error = %v", err)
	}

	var (
		nowMu         sync.Mutex
		now           = time.Date(2026, 2, 10, 7, 0, 0, 0, time.UTC)
		evaluatorRuns int
		events        int
	)

	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:               store,
		ReachabilityChecker: stubReachabilityChecker{},
		MCPHealthEvaluator: func(ctx context.Context, reg Registration) HealthReport {
			evaluatorRuns++
			nowMu.Lock()
			current := now
			nowMu.Unlock()
			return HealthReport{
				ToolName:  reg.Name,
				State:     HealthHealthy,
				CheckedAt: current,
			}
		},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	scheduler, err := NewHealthScheduler(HealthSchedulerConfig{
		Service: service,
		Now: func() time.Time {
			nowMu.Lock()
			defer nowMu.Unlock()
			return now
		},
		OnEvent: func(event HealthEvent) {
			events++
		},
	})
	if err != nil {
		t.Fatalf("NewHealthScheduler() error = %v", err)
	}

	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() first error = %v", err)
	}
	if evaluatorRuns != 1 {
		t.Fatalf("evaluatorRuns after first pass = %d, want 1", evaluatorRuns)
	}
	if events != 1 {
		t.Fatalf("events after first pass = %d, want 1", events)
	}

	nowMu.Lock()
	now = now.Add(2 * time.Second)
	nowMu.Unlock()
	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() second error = %v", err)
	}
	if evaluatorRuns != 1 {
		t.Fatalf("evaluatorRuns after second pass = %d, want 1", evaluatorRuns)
	}

	nowMu.Lock()
	now = now.Add(5 * time.Second)
	nowMu.Unlock()
	if err := scheduler.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() third error = %v", err)
	}
	if evaluatorRuns != 2 {
		t.Fatalf("evaluatorRuns after third pass = %d, want 2", evaluatorRuns)
	}
	if events != 2 {
		t.Fatalf("events after third pass = %d, want 2", events)
	}
}
