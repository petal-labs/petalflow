package runtime

import (
	"context"
	"fmt"

	"github.com/petal-labs/petalflow/memory"
)

// emitterKey is an unexported type used as the context key for EventEmitter.
// Using an unexported struct type prevents collisions with keys from other packages.
type emitterKey struct{}

type humanRequestHandlerKey struct{}

type idempotencyKeyContext struct{}

// HumanRequestHandler bridges a node's human interaction to a durable owner.
// The request and response are intentionally any to keep runtime independent
// from the nodes package and avoid an import cycle.
type HumanRequestHandler func(context.Context, any) (any, error)

// ContextWithEmitter attaches an event emitter to the context.
func ContextWithEmitter(ctx context.Context, emit EventEmitter) context.Context {
	return context.WithValue(ctx, emitterKey{}, emit)
}

// EmitterFromContext retrieves the event emitter from the context.
// Returns a no-op emitter if none is set.
func EmitterFromContext(ctx context.Context) EventEmitter {
	if emit, ok := ctx.Value(emitterKey{}).(EventEmitter); ok {
		return emit
	}
	return func(Event) {}
}

// ContextWithHumanRequestHandler attaches a human interaction bridge.
func ContextWithHumanRequestHandler(ctx context.Context, handler HumanRequestHandler) context.Context {
	return context.WithValue(ctx, humanRequestHandlerKey{}, handler)
}

// HumanRequestHandlerFromContext retrieves a human interaction bridge.
func HumanRequestHandlerFromContext(ctx context.Context) HumanRequestHandler {
	if handler, ok := ctx.Value(humanRequestHandlerKey{}).(HumanRequestHandler); ok {
		return handler
	}
	return nil
}

// ContextWithIdempotencyKey attaches the caller's stable idempotency key to a
// node execution. Side-effecting nodes should pass this value to their
// provider so a replay after a worker failure is safe.
func ContextWithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyContext{}, key)
}

// IdempotencyKeyFromContext retrieves the caller's stable idempotency key.
func IdempotencyKeyFromContext(ctx context.Context) string {
	key, _ := ctx.Value(idempotencyKeyContext{}).(string)
	return key
}

// resolveScope finalizes the memory scope for a run: it normalizes the
// caller's identifiers, fills RunID, inherits TenantID from RunOptions when
// the scope does not set one, and rejects a malformed scope before any node
// runs. A zero scope (no memory in this run) is returned unchanged.
func resolveScope(opts RunOptions, runID string) (memory.Scope, error) {
	scope := opts.Scope.Normalized()
	if scope.IsZero() && opts.TenantID == "" {
		return scope, nil
	}
	if scope.TenantID == "" {
		scope.TenantID = opts.TenantID
	}
	if scope.TenantID != "" && opts.TenantID != "" && scope.TenantID != opts.TenantID {
		return memory.Scope{}, fmt.Errorf("%w: scope tenant %q does not match run tenant %q", memory.ErrInvalidScope, scope.TenantID, opts.TenantID)
	}
	scope.RunID = runID
	// A tenant-only scope is valid: it means the run has no memory partition
	// yet still carries ownership for providers that inspect the context.
	if scope.Namespace == "" && scope.SessionID == "" && scope.ThreadID == "" {
		return scope, nil
	}
	if err := scope.Validate(); err != nil {
		return memory.Scope{}, err
	}
	return scope, nil
}

// restoreScope returns the scope a resumed run must use. The persisted scope
// is authoritative; a caller may repeat it but may not change it, since that
// would let a resume read another session's memory.
func restoreScope(opts RunOptions, record *RunRecord, runID string) (memory.Scope, error) {
	if record.Scope == nil {
		return resolveScope(opts, runID)
	}
	persisted := *record.Scope
	persisted.RunID = runID
	requested := opts.Scope.Normalized()
	if !requested.IsZero() {
		requested.RunID = runID
		if requested.TenantID == "" {
			requested.TenantID = persisted.TenantID
		}
		if requested != persisted {
			return memory.Scope{}, fmt.Errorf("%w: run %s", ErrScopeMismatch, runID)
		}
	}
	return persisted, nil
}
