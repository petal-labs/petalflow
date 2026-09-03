package runtime

import "context"

// emitterKey is an unexported type used as the context key for EventEmitter.
// Using an unexported struct type prevents collisions with keys from other packages.
type emitterKey struct{}

type humanRequestHandlerKey struct{}

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
