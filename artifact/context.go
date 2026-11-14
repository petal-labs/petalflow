package artifact

import "context"

type resolverContextKey struct{}

// Resolver identifies the store and authorized scope used to dereference an
// artifact URI while a node is executing.
type Resolver struct {
	Store Store
	Scope Scope
	// MaxBytes bounds a single read. Zero uses DefaultResolveMaxBytes.
	MaxBytes int64
}

// ContextWithResolver attaches a run-scoped artifact resolver.
func ContextWithResolver(ctx context.Context, resolver Resolver) context.Context {
	return context.WithValue(ctx, resolverContextKey{}, resolver)
}

// ResolverFromContext returns the run-scoped artifact resolver, if present.
func ResolverFromContext(ctx context.Context) (Resolver, bool) {
	resolver, ok := ctx.Value(resolverContextKey{}).(Resolver)
	return resolver, ok && resolver.Store != nil
}
