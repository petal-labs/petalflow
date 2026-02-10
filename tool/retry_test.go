package tool

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInvokeWithRetryRetriesRetryableErrors(t *testing.T) {
	attempts := 0
	resp, attemptCount, err := invokeWithRetry(context.Background(), RetryPolicy{
		MaxAttempts: 3,
	}, retryObservationMeta{}, func(ctx context.Context, attempt int) (InvokeResponse, error) {
		attempts++
		if attempt < 3 {
			return InvokeResponse{}, newToolError(
				ToolErrorCodeTransportFailure,
				"transient",
				true,
				nil,
			)
		}
		return InvokeResponse{Outputs: map[string]any{"ok": true}}, nil
	})
	if err != nil {
		t.Fatalf("invokeWithRetry() error = %v, want nil", err)
	}
	if attemptCount != 3 {
		t.Fatalf("attemptCount = %d, want 3", attemptCount)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if got := resp.Outputs["ok"]; got != true {
		t.Fatalf("outputs[ok] = %v, want true", got)
	}
}

func TestInvokeWithRetryStopsOnNonRetryableError(t *testing.T) {
	attempts := 0
	_, attemptCount, err := invokeWithRetry(context.Background(), RetryPolicy{
		MaxAttempts: 5,
	}, retryObservationMeta{}, func(ctx context.Context, attempt int) (InvokeResponse, error) {
		attempts++
		return InvokeResponse{}, newToolError(
			ToolErrorCodeUpstreamFailure,
			"permanent",
			false,
			nil,
		)
	})
	if err == nil {
		t.Fatal("invokeWithRetry() error = nil, want non-nil")
	}
	if attemptCount != 1 {
		t.Fatalf("attemptCount = %d, want 1", attemptCount)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestInvokeWithRetryRetriesOnDeadlineExceeded(t *testing.T) {
	attempts := 0
	_, attemptCount, err := invokeWithRetry(context.Background(), RetryPolicy{
		MaxAttempts: 2,
	}, retryObservationMeta{}, func(ctx context.Context, attempt int) (InvokeResponse, error) {
		attempts++
		return InvokeResponse{}, context.DeadlineExceeded
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if attemptCount != 2 {
		t.Fatalf("attemptCount = %d, want 2", attemptCount)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRetryBackoffDuration(t *testing.T) {
	policy := RetryPolicy{BackoffMS: 50}
	if got := retryBackoffDuration(policy, 0); got != 0 {
		t.Fatalf("retryBackoffDuration(attempt 0) = %v, want 0", got)
	}
	if got := retryBackoffDuration(policy, 1); got != 50*time.Millisecond {
		t.Fatalf("retryBackoffDuration(attempt 1) = %v, want 50ms", got)
	}
	if got := retryBackoffDuration(policy, 3); got != 150*time.Millisecond {
		t.Fatalf("retryBackoffDuration(attempt 3) = %v, want 150ms", got)
	}
}
