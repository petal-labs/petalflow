package tool

import (
	"context"
	"errors"
	"net"
	"time"
)

type invokeFunc func(ctx context.Context, attempt int) (InvokeResponse, error)

type retryObservationMeta struct {
	toolName  string
	action    string
	transport TransportType
}

func invokeWithRetry(ctx context.Context, policy RetryPolicy, meta retryObservationMeta, fn invokeFunc) (InvokeResponse, int, error) {
	normalized := normalizeRetryPolicy(policy)
	var (
		lastErr error
		resp    InvokeResponse
	)

	for attempt := 1; attempt <= normalized.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return InvokeResponse{}, attempt, err
		}

		resp, lastErr = fn(ctx, attempt)
		if lastErr == nil {
			return resp, attempt, nil
		}
		if attempt == normalized.MaxAttempts || !isRetryableError(lastErr) {
			return InvokeResponse{}, attempt, lastErr
		}
		emitRetryObservation(ToolRetryObservation{
			ToolName:  meta.toolName,
			Action:    meta.action,
			Transport: meta.transport,
			Attempt:   attempt,
			ErrorCode: toolErrorCode(lastErr),
		})

		wait := retryBackoffDuration(normalized, attempt)
		if wait <= 0 {
			continue
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return InvokeResponse{}, attempt, ctx.Err()
		case <-timer.C:
		}
	}

	return InvokeResponse{}, normalized.MaxAttempts, lastErr
}

func normalizeRetryPolicy(policy RetryPolicy) RetryPolicy {
	out := policy
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = 1
	}
	if out.BackoffMS < 0 {
		out.BackoffMS = 0
	}
	return out
}

func retryBackoffDuration(policy RetryPolicy, attempt int) time.Duration {
	if policy.BackoffMS <= 0 || attempt <= 0 {
		return 0
	}
	return time.Duration(policy.BackoffMS*attempt) * time.Millisecond
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if toolErr, ok := toolErrorFrom(err); ok {
		return toolErr.Retryable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}
