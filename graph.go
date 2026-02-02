package petalflow

import (
	"errors"
	"fmt"
)

// Graph errors
var (
	ErrNodeNotFound     = errors.New("node not found")
	ErrDuplicateNode    = errors.New("duplicate node ID")
	ErrInvalidEdge      = errors.New("invalid edge")
	ErrNoEntryNode      = errors.New("no entry node defined")
	ErrCycleDetected    = errors.New("cycle detected in graph")
	ErrEmptyGraph       = errors.New("graph has no nodes")
	ErrNodeAlreadyAdded = errors.New("node already added to graph")
)

// Graph represents a directed graph of nodes connected by edges.
// By default, graphs are DAGs (directed acyclic graphs).
// Limited cycles are allowed for revise loops, guarded by MaxHops at runtime.
type Graph interface {
	// Name returns the graph's identifier.
	Name() string

	// Nodes returns all nodes in the graph.
	Nodes() []Node

	// Edges returns all edges in the graph.
	Edges() []Edge

	// NodeByID retrieves a node by its ID.
	NodeByID(id string) (Node, bool)

	// Entry returns the entry node ID for execution.
	Entry() string

	// Successors returns the IDs of nodes that follow the given node.
	Successors(nodeID string) []string

	// Predecessors returns the IDs of nodes that precede the given node.
	Predecessors(nodeID string) []string
}

// Edge represents a directed connection between two nodes.
type Edge struct {
	From string // source node ID
	To   string // target node ID
}

// BasicGraph is a simple implementation of the Graph interface.
type BasicGraph struct {
	name         string
	nodes        map[string]Node
	nodeOrder    []string // preserves insertion order
	edges        []Edge
	successors   map[string][]string // node ID -> successor IDs
	predecessors map[string][]string // node ID -> predecessor IDs
	entry        string
}

// NewGraph creates a new empty graph with the given name.
func NewGraph(name string) *BasicGraph {
	return &BasicGraph{
		name:         name,
		nodes:        make(map[string]Node),
		nodeOrder:    make([]string, 0),
		edges:        make([]Edge, 0),
		successors:   make(map[string][]string),
		predecessors: make(map[string][]string),
	}
}

// Name returns the graph's identifier.
func (g *BasicGraph) Name() string {
	return g.name
}

// Nodes returns all nodes in the graph in insertion order.
func (g *BasicGraph) Nodes() []Node {
	nodes := make([]Node, 0, len(g.nodeOrder))
	for _, id := range g.nodeOrder {
		nodes = append(nodes, g.nodes[id])
	}
	return nodes
}

// Edges returns all edges in the graph.
func (g *BasicGraph) Edges() []Edge {
	return g.edges
}

// NodeByID retrieves a node by its ID.
func (g *BasicGraph) NodeByID(id string) (Node, bool) {
	node, ok := g.nodes[id]
	return node, ok
}

// Entry returns the entry node ID for execution.
func (g *BasicGraph) Entry() string {
	return g.entry
}

// Successors returns the IDs of nodes that follow the given node.
func (g *BasicGraph) Successors(nodeID string) []string {
	return g.successors[nodeID]
}

// Predecessors returns the IDs of nodes that precede the given node.
func (g *BasicGraph) Predecessors(nodeID string) []string {
	return g.predecessors[nodeID]
}

// AddNode adds a node to the graph.
// Returns an error if a node with the same ID already exists.
func (g *BasicGraph) AddNode(node Node) error {
	if node == nil {
		return errors.New("cannot add nil node")
	}

	id := node.ID()
	if _, exists := g.nodes[id]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateNode, id)
	}

	g.nodes[id] = node
	g.nodeOrder = append(g.nodeOrder, id)

	// Initialize empty successor/predecessor lists
	if g.successors[id] == nil {
		g.successors[id] = make([]string, 0)
	}
	if g.predecessors[id] == nil {
		g.predecessors[id] = make([]string, 0)
	}

	return nil
}

// AddEdge adds a directed edge from one node to another.
// Both nodes must already exist in the graph.
func (g *BasicGraph) AddEdge(from, to string) error {
	if _, ok := g.nodes[from]; !ok {
		return fmt.Errorf("%w: source node %q not found", ErrInvalidEdge, from)
	}
	if _, ok := g.nodes[to]; !ok {
		return fmt.Errorf("%w: target node %q not found", ErrInvalidEdge, to)
	}

	// Check for duplicate edge
	for _, e := range g.edges {
		if e.From == from && e.To == to {
			return nil // Edge already exists, no-op
		}
	}

	g.edges = append(g.edges, Edge{From: from, To: to})
	g.successors[from] = append(g.successors[from], to)
	g.predecessors[to] = append(g.predecessors[to], from)

	return nil
}

// SetEntry sets the entry node for execution.
// The node must already exist in the graph.
func (g *BasicGraph) SetEntry(nodeID string) error {
	if _, ok := g.nodes[nodeID]; !ok {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}
	g.entry = nodeID
	return nil
}

// Validate checks the graph for common issues.
// Returns an error if the graph is invalid.
func (g *BasicGraph) Validate() error {
	if len(g.nodes) == 0 {
		return ErrEmptyGraph
	}

	if g.entry == "" {
		return ErrNoEntryNode
	}

	if _, ok := g.nodes[g.entry]; !ok {
		return fmt.Errorf("%w: entry node %q", ErrNodeNotFound, g.entry)
	}

	return nil
}

// TopologicalSort returns the nodes in topological order.
// Returns an error if a cycle is detected (unless allowCycles is true).
func (g *BasicGraph) TopologicalSort(allowCycles bool) ([]string, error) {
	// Kahn's algorithm
	inDegree := make(map[string]int)
	for id := range g.nodes {
		inDegree[id] = len(g.predecessors[id])
	}

	// Start with nodes that have no predecessors
	queue := make([]string, 0)
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	result := make([]string, 0, len(g.nodes))
	for len(queue) > 0 {
		// Pop from queue
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		// Reduce in-degree of successors
		for _, succ := range g.successors[current] {
			inDegree[succ]--
			if inDegree[succ] == 0 {
				queue = append(queue, succ)
			}
		}
	}

	// If we didn't process all nodes, there's a cycle
	if len(result) != len(g.nodes) && !allowCycles {
		return nil, ErrCycleDetected
	}

	return result, nil
}

// Reachable returns all node IDs reachable from the given start node.
func (g *BasicGraph) Reachable(startID string) []string {
	visited := make(map[string]bool)
	result := make([]string, 0)

	var visit func(id string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true
		result = append(result, id)
		for _, succ := range g.successors[id] {
			visit(succ)
		}
	}

	visit(startID)
	return result
}

// Ensure interface compliance at compile time.
var _ Graph = (*BasicGraph)(nil)
