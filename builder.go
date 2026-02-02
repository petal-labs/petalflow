package petalflow

import (
	"fmt"
)

// GraphBuilder provides a fluent API for constructing workflow graphs.
// It tracks the "current" node(s) to enable method chaining.
//
// Example usage:
//
//	graph, err := NewGraphBuilder("my-workflow").
//	    AddNode(inputNode).
//	    Edge(processorNode).
//	    FanOut(
//	        NewNoopNode("branch-a"),
//	        NewNoopNode("branch-b"),
//	    ).
//	    Merge(NewMergeNode("combiner", MergeNodeConfig{})).
//	    Edge(outputNode).
//	    Build()
type GraphBuilder struct {
	graph        *BasicGraph
	current      []string // current node IDs (can be multiple for parallel branches)
	errors       []error
	entryDefined bool
}

// NewGraphBuilder creates a new graph builder with the given name.
func NewGraphBuilder(name string) *GraphBuilder {
	return &GraphBuilder{
		graph:   NewGraph(name),
		current: make([]string, 0),
		errors:  make([]error, 0),
	}
}

// AddNode adds a node to the graph and makes it the current node.
// If this is the first node, it becomes the entry node.
func (b *GraphBuilder) AddNode(node Node) *GraphBuilder {
	if node == nil {
		b.errors = append(b.errors, fmt.Errorf("cannot add nil node"))
		return b
	}

	if err := b.graph.AddNode(node); err != nil {
		b.errors = append(b.errors, err)
		return b
	}

	// First node becomes entry by default
	if !b.entryDefined {
		_ = b.graph.SetEntry(node.ID())
		b.entryDefined = true
	}

	b.current = []string{node.ID()}
	return b
}

// Entry sets the entry node for the graph.
// The node must already be added to the graph.
func (b *GraphBuilder) Entry(nodeID string) *GraphBuilder {
	if err := b.graph.SetEntry(nodeID); err != nil {
		b.errors = append(b.errors, err)
		return b
	}
	b.entryDefined = true
	return b
}

// Edge adds a node and connects it from all current nodes.
// The new node becomes the single current node.
func (b *GraphBuilder) Edge(node Node) *GraphBuilder {
	if node == nil {
		b.errors = append(b.errors, fmt.Errorf("cannot add nil node"))
		return b
	}

	if err := b.graph.AddNode(node); err != nil {
		b.errors = append(b.errors, err)
		return b
	}

	// Connect from all current nodes
	for _, from := range b.current {
		if err := b.graph.AddEdge(from, node.ID()); err != nil {
			b.errors = append(b.errors, err)
		}
	}

	b.current = []string{node.ID()}
	return b
}

// EdgeTo creates an edge from all current nodes to an existing node by ID.
// The target node becomes the current node.
func (b *GraphBuilder) EdgeTo(nodeID string) *GraphBuilder {
	if _, ok := b.graph.NodeByID(nodeID); !ok {
		b.errors = append(b.errors, fmt.Errorf("node %q not found", nodeID))
		return b
	}

	for _, from := range b.current {
		if err := b.graph.AddEdge(from, nodeID); err != nil {
			b.errors = append(b.errors, err)
		}
	}

	b.current = []string{nodeID}
	return b
}

// Connect creates an edge between two existing nodes by their IDs.
// Does not change the current node.
func (b *GraphBuilder) Connect(fromID, toID string) *GraphBuilder {
	if err := b.graph.AddEdge(fromID, toID); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// FanOut splits execution from the current node to multiple parallel branches.
// Each provided node becomes a separate branch, and all branches become
// the "current" nodes for subsequent operations.
//
// Use Merge() after FanOut to combine the branches.
func (b *GraphBuilder) FanOut(nodes ...Node) *GraphBuilder {
	if len(nodes) == 0 {
		b.errors = append(b.errors, fmt.Errorf("FanOut requires at least one node"))
		return b
	}

	if len(b.current) == 0 {
		b.errors = append(b.errors, fmt.Errorf("FanOut requires a current node"))
		return b
	}

	newCurrent := make([]string, 0, len(nodes))

	for _, node := range nodes {
		if node == nil {
			b.errors = append(b.errors, fmt.Errorf("cannot add nil node in FanOut"))
			continue
		}

		if err := b.graph.AddNode(node); err != nil {
			b.errors = append(b.errors, err)
			continue
		}

		// Connect from all current nodes to this branch
		for _, from := range b.current {
			if err := b.graph.AddEdge(from, node.ID()); err != nil {
				b.errors = append(b.errors, err)
			}
		}

		newCurrent = append(newCurrent, node.ID())
	}

	b.current = newCurrent
	return b
}

// FanOutTo creates edges from the current node to existing nodes by their IDs.
// All target nodes become the current nodes (parallel branches).
func (b *GraphBuilder) FanOutTo(nodeIDs ...string) *GraphBuilder {
	if len(nodeIDs) == 0 {
		b.errors = append(b.errors, fmt.Errorf("FanOutTo requires at least one node ID"))
		return b
	}

	if len(b.current) == 0 {
		b.errors = append(b.errors, fmt.Errorf("FanOutTo requires a current node"))
		return b
	}

	newCurrent := make([]string, 0, len(nodeIDs))

	for _, nodeID := range nodeIDs {
		if _, ok := b.graph.NodeByID(nodeID); !ok {
			b.errors = append(b.errors, fmt.Errorf("node %q not found", nodeID))
			continue
		}

		for _, from := range b.current {
			if err := b.graph.AddEdge(from, nodeID); err != nil {
				b.errors = append(b.errors, err)
			}
		}

		newCurrent = append(newCurrent, nodeID)
	}

	b.current = newCurrent
	return b
}

// Merge adds a merge node that combines all current parallel branches.
// The merge node becomes the single current node.
//
// The merge node's ExpectedInputs will be set to the number of incoming branches
// if not already configured.
func (b *GraphBuilder) Merge(mergeNode *MergeNode) *GraphBuilder {
	if mergeNode == nil {
		b.errors = append(b.errors, fmt.Errorf("cannot merge with nil node"))
		return b
	}

	// Set expected inputs if not configured
	if mergeNode.config.ExpectedInputs == 0 {
		mergeNode.config.ExpectedInputs = len(b.current)
	}

	if err := b.graph.AddNode(mergeNode); err != nil {
		b.errors = append(b.errors, err)
		return b
	}

	// Connect all current branches to the merge node
	for _, from := range b.current {
		if err := b.graph.AddEdge(from, mergeNode.ID()); err != nil {
			b.errors = append(b.errors, err)
		}
	}

	b.current = []string{mergeNode.ID()}
	return b
}

// MergeTo connects all current branches to an existing merge node by ID.
// The merge node becomes the current node.
func (b *GraphBuilder) MergeTo(nodeID string) *GraphBuilder {
	node, ok := b.graph.NodeByID(nodeID)
	if !ok {
		b.errors = append(b.errors, fmt.Errorf("merge node %q not found", nodeID))
		return b
	}

	// Verify it's a merge node
	if node.Kind() != NodeKindMerge {
		b.errors = append(b.errors, fmt.Errorf("node %q is not a merge node (kind: %s)", nodeID, node.Kind()))
		return b
	}

	for _, from := range b.current {
		if err := b.graph.AddEdge(from, nodeID); err != nil {
			b.errors = append(b.errors, err)
		}
	}

	b.current = []string{nodeID}
	return b
}

// Branch temporarily switches to a different node as the current node.
// Useful for building non-linear graphs.
func (b *GraphBuilder) Branch(nodeID string) *GraphBuilder {
	if _, ok := b.graph.NodeByID(nodeID); !ok {
		b.errors = append(b.errors, fmt.Errorf("node %q not found", nodeID))
		return b
	}
	b.current = []string{nodeID}
	return b
}

// Branches sets multiple nodes as current for parallel building.
func (b *GraphBuilder) Branches(nodeIDs ...string) *GraphBuilder {
	newCurrent := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if _, ok := b.graph.NodeByID(nodeID); !ok {
			b.errors = append(b.errors, fmt.Errorf("node %q not found", nodeID))
			continue
		}
		newCurrent = append(newCurrent, nodeID)
	}
	b.current = newCurrent
	return b
}

// Current returns the current node IDs.
func (b *GraphBuilder) Current() []string {
	return b.current
}

// WithNodes adds multiple nodes without creating edges.
// Does not change the current node.
func (b *GraphBuilder) WithNodes(nodes ...Node) *GraphBuilder {
	for _, node := range nodes {
		if node == nil {
			b.errors = append(b.errors, fmt.Errorf("cannot add nil node"))
			continue
		}
		if err := b.graph.AddNode(node); err != nil {
			b.errors = append(b.errors, err)
		}
	}
	return b
}

// Errors returns any errors accumulated during building.
func (b *GraphBuilder) Errors() []error {
	return b.errors
}

// Build validates and returns the constructed graph.
// Returns an error if any errors occurred during building or validation fails.
func (b *GraphBuilder) Build() (Graph, error) {
	if len(b.errors) > 0 {
		return nil, fmt.Errorf("graph builder errors: %v", b.errors)
	}

	if err := b.graph.Validate(); err != nil {
		return nil, fmt.Errorf("graph validation failed: %w", err)
	}

	return b.graph, nil
}

// MustBuild is like Build but panics on error.
// Useful in tests and examples.
func (b *GraphBuilder) MustBuild() Graph {
	graph, err := b.Build()
	if err != nil {
		panic(err)
	}
	return graph
}

// Graph returns the underlying graph without validation.
// Use Build() for production code.
func (b *GraphBuilder) Graph() *BasicGraph {
	return b.graph
}

// FanOutBranch is a helper for building complex fan-out patterns.
// It represents a single branch in a fan-out with optional sub-pipeline.
type FanOutBranch struct {
	Entry Node   // First node of the branch
	Nodes []Node // Additional nodes in sequence
}

// NewBranch creates a simple branch with a single node.
func NewBranch(node Node) FanOutBranch {
	return FanOutBranch{Entry: node}
}

// NewPipelineBranch creates a branch with multiple nodes in sequence.
func NewPipelineBranch(nodes ...Node) FanOutBranch {
	if len(nodes) == 0 {
		return FanOutBranch{}
	}
	return FanOutBranch{
		Entry: nodes[0],
		Nodes: nodes[1:],
	}
}

// FanOutBranches creates a fan-out with multiple branch pipelines.
// Each branch can be a single node or a sequence of nodes.
// All branches are executed in parallel and can be merged later.
func (b *GraphBuilder) FanOutBranches(branches ...FanOutBranch) *GraphBuilder {
	if len(branches) == 0 {
		b.errors = append(b.errors, fmt.Errorf("FanOutBranches requires at least one branch"))
		return b
	}

	if len(b.current) == 0 {
		b.errors = append(b.errors, fmt.Errorf("FanOutBranches requires a current node"))
		return b
	}

	newCurrent := make([]string, 0, len(branches))

	for i, branch := range branches {
		if branch.Entry == nil {
			b.errors = append(b.errors, fmt.Errorf("branch %d has nil entry node", i))
			continue
		}

		// Add entry node
		if err := b.graph.AddNode(branch.Entry); err != nil {
			b.errors = append(b.errors, err)
			continue
		}

		// Connect from all current nodes to branch entry
		for _, from := range b.current {
			if err := b.graph.AddEdge(from, branch.Entry.ID()); err != nil {
				b.errors = append(b.errors, err)
			}
		}

		// Build the branch pipeline
		lastID := branch.Entry.ID()
		for _, node := range branch.Nodes {
			if node == nil {
				b.errors = append(b.errors, fmt.Errorf("nil node in branch %d pipeline", i))
				continue
			}
			if err := b.graph.AddNode(node); err != nil {
				b.errors = append(b.errors, err)
				continue
			}
			if err := b.graph.AddEdge(lastID, node.ID()); err != nil {
				b.errors = append(b.errors, err)
			}
			lastID = node.ID()
		}

		// The last node of the branch becomes a current node
		newCurrent = append(newCurrent, lastID)
	}

	b.current = newCurrent
	return b
}

// Conditional adds a router node with edges to all its potential targets.
// The router must have its targets already defined in its configuration.
// This is a convenience method for adding conditional routing.
func (b *GraphBuilder) Conditional(router RouterNode, targets ...Node) *GraphBuilder {
	if router == nil {
		b.errors = append(b.errors, fmt.Errorf("cannot add nil router"))
		return b
	}

	// Add the router
	if err := b.graph.AddNode(router); err != nil {
		b.errors = append(b.errors, err)
		return b
	}

	// Connect from current to router
	for _, from := range b.current {
		if err := b.graph.AddEdge(from, router.ID()); err != nil {
			b.errors = append(b.errors, err)
		}
	}

	// Add all target nodes and connect from router
	targetIDs := make([]string, 0, len(targets))
	for _, target := range targets {
		if target == nil {
			b.errors = append(b.errors, fmt.Errorf("cannot add nil target"))
			continue
		}
		if err := b.graph.AddNode(target); err != nil {
			b.errors = append(b.errors, err)
			continue
		}
		if err := b.graph.AddEdge(router.ID(), target.ID()); err != nil {
			b.errors = append(b.errors, err)
		}
		targetIDs = append(targetIDs, target.ID())
	}

	b.current = targetIDs
	return b
}

// Terminal marks the current nodes as terminal (no further edges).
// Returns the builder for optional further operations on other branches.
func (b *GraphBuilder) Terminal() *GraphBuilder {
	b.current = []string{}
	return b
}
