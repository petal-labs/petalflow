package tool

import (
	"context"
	"testing"
)

func TestCorrelationFromContext(t *testing.T) {
	// No audit context: zero correlation (daemon-level call).
	correlation := CorrelationFromContext(context.Background())
	if correlation != (ObservationCorrelation{}) {
		t.Errorf("correlation without audit context = %+v, want zero", correlation)
	}

	// Audit context attached: correlation extracted.
	ctx := ContextWithAuditContext(context.Background(), ToolAuditContext{
		TenantID: "tenant-a",
		RunID:    "run-1",
		NodeID:   "node-1",
	})
	correlation = CorrelationFromContext(ctx)
	if correlation.TenantID != "tenant-a" || correlation.RunID != "run-1" || correlation.NodeID != "node-1" {
		t.Errorf("correlation = %+v, want tenant-a/run-1/node-1", correlation)
	}
}

func TestObserverEmitRoundTrip(t *testing.T) {
	recording := &recordingObserver{}
	SetObserver(recording)
	defer SetObserver(nil)

	emitInvokeObservation(ToolInvokeObservation{
		ToolName:  "s3_fetch",
		Action:    "list",
		Success:   true,
	})
	emitRetryObservation(ToolRetryObservation{
		ToolName: "s3_fetch",
		Action:   "list",
		Attempt:  1,
	})
	emitHealthObservation(ToolHealthObservation{
		ToolName: "s3_fetch",
		State:    HealthHealthy,
	})

	if len(recording.invokes) != 1 || recording.invokes[0].ToolName != "s3_fetch" {
		t.Errorf("invoke observations = %+v", recording.invokes)
	}
	if len(recording.retries) != 1 || recording.retries[0].Attempt != 1 {
		t.Errorf("retry observations = %+v", recording.retries)
	}
	if len(recording.health) != 1 || recording.health[0].State != HealthHealthy {
		t.Errorf("health observations = %+v", recording.health)
	}

	// Resetting to nil restores the silent no-op observer.
	SetObserver(nil)
	emitInvokeObservation(ToolInvokeObservation{ToolName: "silent"})
	if len(recording.invokes) != 1 {
		t.Errorf("observer still active after SetObserver(nil): %+v", recording.invokes)
	}
}
