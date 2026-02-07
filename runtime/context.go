package runtime

import "context"

// emitterKey is an unexported type used as the context key for EventEmitter.
// Using an unexported struct type prevents collisions with keys from other packages.
type emitterKey struct{}

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
