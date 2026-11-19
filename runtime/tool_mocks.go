package runtime

import "context"

// ToolMockMode controls tool invocation behavior for replay runs.
type ToolMockMode string

const (
	ToolMockModeNone ToolMockMode = "none"
	ToolMockModeAll  ToolMockMode = "all"
)

// ToolMockSet contains captured outputs keyed by workflow tool-node ID.
// Results are read-only for the duration of a run.
type ToolMockSet struct {
	Mode    ToolMockMode
	Results map[string]map[string]any
}

type toolMockContextKey struct{}

// ContextWithToolMocks attaches replay tool outputs to a run context.
func ContextWithToolMocks(ctx context.Context, mocks ToolMockSet) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolMockContextKey{}, mocks)
}

// ToolMocksFromContext returns replay tool outputs attached to a run context.
func ToolMocksFromContext(ctx context.Context) (ToolMockSet, bool) {
	if ctx == nil {
		return ToolMockSet{}, false
	}
	mocks, ok := ctx.Value(toolMockContextKey{}).(ToolMockSet)
	return mocks, ok
}
