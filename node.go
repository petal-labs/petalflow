package petalflow

import (
	"context"
)

// Node is the fundamental unit of execution in a PetalFlow graph.
// Each node has a unique ID, a kind, and a Run method that transforms an envelope.
type Node interface {
	// ID returns the unique identifier for this node within a graph.
	ID() string

	// Kind returns the type of this node (llm, tool, router, etc.).
	Kind() NodeKind

	// Run executes the node's logic, transforming the input envelope.
	// The returned envelope may be the same instance (modified) or a new one.
	// Nodes should append to envelope fields rather than overwrite, unless configured.
	Run(ctx context.Context, env *Envelope) (*Envelope, error)
}

// BaseNode provides common functionality for node implementations.
// Embed this in concrete node types to get ID and Kind handling for free.
type BaseNode struct {
	id   string
	kind NodeKind
}

// NewBaseNode creates a new BaseNode with the given ID and kind.
func NewBaseNode(id string, kind NodeKind) BaseNode {
	return BaseNode{
		id:   id,
		kind: kind,
	}
}

// ID returns the node's unique identifier.
func (n BaseNode) ID() string {
	return n.id
}

// Kind returns the node's type.
func (n BaseNode) Kind() NodeKind {
	return n.kind
}

// NoopNode is a node that passes the envelope through unchanged.
// Useful for testing, placeholders, and explicit no-op steps in workflows.
type NoopNode struct {
	BaseNode
}

// NewNoopNode creates a new no-operation node with the given ID.
func NewNoopNode(id string) *NoopNode {
	return &NoopNode{
		BaseNode: NewBaseNode(id, NodeKindNoop),
	}
}

// Run passes the envelope through unchanged.
func (n *NoopNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	return env, nil
}

// FuncNode wraps a function as a Node.
// This is convenient for simple transformations and testing.
type FuncNode struct {
	BaseNode
	fn func(ctx context.Context, env *Envelope) (*Envelope, error)
}

// NewFuncNode creates a node that executes the given function.
// The kind defaults to NodeKindNoop but can be overridden via WithKind.
func NewFuncNode(id string, fn func(ctx context.Context, env *Envelope) (*Envelope, error)) *FuncNode {
	return &FuncNode{
		BaseNode: NewBaseNode(id, NodeKindNoop),
		fn:       fn,
	}
}

// WithKind sets the node kind and returns the node for chaining.
func (n *FuncNode) WithKind(kind NodeKind) *FuncNode {
	n.kind = kind
	return n
}

// Run executes the wrapped function.
func (n *FuncNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	if n.fn == nil {
		return env, nil
	}
	return n.fn(ctx, env)
}

// NodeFunc is a convenience type for node functions.
type NodeFunc func(ctx context.Context, env *Envelope) (*Envelope, error)

// Ensure interface compliance at compile time.
var (
	_ Node = (*NoopNode)(nil)
	_ Node = (*FuncNode)(nil)
)
