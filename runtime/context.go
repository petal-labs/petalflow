package runtime

import (
	"context"

	"github.com/petal-labs/petalflow/memory"
)

// emitterKey is an unexported type used as the context key for EventEmitter.
// Using an unexported struct type prevents collisions with keys from other packages.
type emitterKey struct{}

type humanRequestHandlerKey struct{}

type idempotencyKeyContext struct{}

type scopeKey struct{}

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

type toolApprovalHandlerKey struct{}

type toolApprovalVerifierKey struct{}

// ToolApprovalHandler bridges a node's tool-approval request to a durable
// owner. The request and response are intentionally any to keep runtime
// independent from the nodes package (see nodes.ToolApprovalRequest).
// Implementations return a sentinel PendingError to pause the run while
// the decision is outstanding.
type ToolApprovalHandler func(context.Context, any) (any, error)

// ContextWithToolApprovalHandler attaches a tool-approval bridge.
func ContextWithToolApprovalHandler(ctx context.Context, handler ToolApprovalHandler) context.Context {
	return context.WithValue(ctx, toolApprovalHandlerKey{}, handler)
}

// ToolApprovalHandlerFromContext retrieves a tool-approval bridge.
func ToolApprovalHandlerFromContext(ctx context.Context) ToolApprovalHandler {
	if handler, ok := ctx.Value(toolApprovalHandlerKey{}).(ToolApprovalHandler); ok {
		return handler
	}
	return nil
}

// ToolApprovalVerifier validates and single-use-claims an approval grant
// against the durable approval records for a run. Implementations return
// an error when the grant is unknown, not approved, expired, or already
// consumed.
type ToolApprovalVerifier func(ctx context.Context, runID, approvalID, toolName string) error

// ContextWithToolApprovalVerifier attaches a grant verifier for nodes
// that check approval grants (e.g. Guardian approval checks).
func ContextWithToolApprovalVerifier(ctx context.Context, verifier ToolApprovalVerifier) context.Context {
	return context.WithValue(ctx, toolApprovalVerifierKey{}, verifier)
}

// ToolApprovalVerifierFromContext retrieves a grant verifier.
func ToolApprovalVerifierFromContext(ctx context.Context) ToolApprovalVerifier {
	if verifier, ok := ctx.Value(toolApprovalVerifierKey{}).(ToolApprovalVerifier); ok {
		return verifier
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

// ContextWithScope attaches the memory scope (session, thread, namespace,
// tenant) for the current run to the context. Memory nodes read this scope
// when their configuration does not override individual identifiers.
func ContextWithScope(ctx context.Context, scope memory.Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

// ScopeFromContext retrieves the memory scope for the current run.
// Returns the zero Scope when none is set.
func ScopeFromContext(ctx context.Context) memory.Scope {
	scope, _ := ctx.Value(scopeKey{}).(memory.Scope)
	return scope
}
