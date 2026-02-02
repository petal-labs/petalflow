package petalflow

import (
	"errors"
	"testing"
)

func TestNewGraph(t *testing.T) {
	g := NewGraph("test-graph")

	if g.Name() != "test-graph" {
		t.Errorf("Graph.Name() = %v, want 'test-graph'", g.Name())
	}
	if len(g.Nodes()) != 0 {
		t.Errorf("Graph.Nodes() = %v, want empty", g.Nodes())
	}
	if len(g.Edges()) != 0 {
		t.Errorf("Graph.Edges() = %v, want empty", g.Edges())
	}
	if g.Entry() != "" {
		t.Errorf("Graph.Entry() = %v, want empty", g.Entry())
	}
}

func TestGraph_AddNode(t *testing.T) {
	g := NewGraph("test")

	node := NewNoopNode("node-1")
	err := g.AddNode(node)

	if err != nil {
		t.Errorf("AddNode() error = %v", err)
	}

	nodes := g.Nodes()
	if len(nodes) != 1 {
		t.Errorf("len(Nodes()) = %v, want 1", len(nodes))
	}
	if nodes[0].ID() != "node-1" {
		t.Error("Node ID mismatch")
	}
}

func TestGraph_AddNode_Duplicate(t *testing.T) {
	g := NewGraph("test")

	g.AddNode(NewNoopNode("node-1"))
	err := g.AddNode(NewNoopNode("node-1"))

	if !errors.Is(err, ErrDuplicateNode) {
		t.Errorf("AddNode() error = %v, want %v", err, ErrDuplicateNode)
	}
}

func TestGraph_AddNode_Nil(t *testing.T) {
	g := NewGraph("test")

	err := g.AddNode(nil)

	if err == nil {
		t.Error("AddNode(nil) should return error")
	}
}

func TestGraph_NodeByID(t *testing.T) {
	g := NewGraph("test")
	node := NewNoopNode("node-1")
	g.AddNode(node)

	// Found
	found, ok := g.NodeByID("node-1")
	if !ok {
		t.Error("NodeByID() should return true for existing node")
	}
	if found != node {
		t.Error("NodeByID() returned wrong node")
	}

	// Not found
	_, ok = g.NodeByID("missing")
	if ok {
		t.Error("NodeByID() should return false for missing node")
	}
}

func TestGraph_AddEdge(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))

	err := g.AddEdge("a", "b")

	if err != nil {
		t.Errorf("AddEdge() error = %v", err)
	}

	edges := g.Edges()
	if len(edges) != 1 {
		t.Errorf("len(Edges()) = %v, want 1", len(edges))
	}
	if edges[0].From != "a" || edges[0].To != "b" {
		t.Error("Edge values incorrect")
	}
}

func TestGraph_AddEdge_MissingSource(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("b"))

	err := g.AddEdge("a", "b")

	if !errors.Is(err, ErrInvalidEdge) {
		t.Errorf("AddEdge() error = %v, want %v", err, ErrInvalidEdge)
	}
}

func TestGraph_AddEdge_MissingTarget(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))

	err := g.AddEdge("a", "b")

	if !errors.Is(err, ErrInvalidEdge) {
		t.Errorf("AddEdge() error = %v, want %v", err, ErrInvalidEdge)
	}
}

func TestGraph_AddEdge_Duplicate(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))
	g.AddEdge("a", "b")

	// Adding same edge again should be a no-op
	err := g.AddEdge("a", "b")

	if err != nil {
		t.Errorf("AddEdge() duplicate error = %v, want nil", err)
	}
	if len(g.Edges()) != 1 {
		t.Errorf("len(Edges()) = %v, want 1 (no duplicate)", len(g.Edges()))
	}
}

func TestGraph_SetEntry(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("start"))

	err := g.SetEntry("start")

	if err != nil {
		t.Errorf("SetEntry() error = %v", err)
	}
	if g.Entry() != "start" {
		t.Errorf("Entry() = %v, want 'start'", g.Entry())
	}
}

func TestGraph_SetEntry_MissingNode(t *testing.T) {
	g := NewGraph("test")

	err := g.SetEntry("missing")

	if !errors.Is(err, ErrNodeNotFound) {
		t.Errorf("SetEntry() error = %v, want %v", err, ErrNodeNotFound)
	}
}

func TestGraph_Successors(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))
	g.AddNode(NewNoopNode("c"))
	g.AddEdge("a", "b")
	g.AddEdge("a", "c")

	successors := g.Successors("a")

	if len(successors) != 2 {
		t.Errorf("len(Successors('a')) = %v, want 2", len(successors))
	}
}

func TestGraph_Predecessors(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))
	g.AddNode(NewNoopNode("c"))
	g.AddEdge("a", "c")
	g.AddEdge("b", "c")

	predecessors := g.Predecessors("c")

	if len(predecessors) != 2 {
		t.Errorf("len(Predecessors('c')) = %v, want 2", len(predecessors))
	}
}

func TestGraph_Validate(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*BasicGraph)
		wantErr error
	}{
		{
			name:    "empty graph",
			setup:   func(g *BasicGraph) {},
			wantErr: ErrEmptyGraph,
		},
		{
			name: "no entry",
			setup: func(g *BasicGraph) {
				g.AddNode(NewNoopNode("a"))
			},
			wantErr: ErrNoEntryNode,
		},
		{
			name: "valid graph",
			setup: func(g *BasicGraph) {
				g.AddNode(NewNoopNode("a"))
				g.SetEntry("a")
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGraph("test")
			tt.setup(g)

			err := g.Validate()

			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() error = %v, want nil", err)
				}
			} else {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Validate() error = %v, want %v", err, tt.wantErr)
				}
			}
		})
	}
}

func TestGraph_TopologicalSort(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))
	g.AddNode(NewNoopNode("c"))
	g.AddNode(NewNoopNode("d"))
	g.AddEdge("a", "b")
	g.AddEdge("a", "c")
	g.AddEdge("b", "d")
	g.AddEdge("c", "d")

	order, err := g.TopologicalSort(false)

	if err != nil {
		t.Errorf("TopologicalSort() error = %v", err)
	}

	// 'a' must come before 'b' and 'c'
	// 'd' must come after 'b' and 'c'
	aIdx, bIdx, cIdx, dIdx := -1, -1, -1, -1
	for i, id := range order {
		switch id {
		case "a":
			aIdx = i
		case "b":
			bIdx = i
		case "c":
			cIdx = i
		case "d":
			dIdx = i
		}
	}

	if aIdx > bIdx || aIdx > cIdx {
		t.Error("'a' should come before 'b' and 'c'")
	}
	if dIdx < bIdx || dIdx < cIdx {
		t.Error("'d' should come after 'b' and 'c'")
	}
}

func TestGraph_TopologicalSort_Cycle(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))
	g.AddNode(NewNoopNode("c"))
	g.AddEdge("a", "b")
	g.AddEdge("b", "c")
	g.AddEdge("c", "a") // Creates cycle

	_, err := g.TopologicalSort(false)

	if !errors.Is(err, ErrCycleDetected) {
		t.Errorf("TopologicalSort() error = %v, want %v", err, ErrCycleDetected)
	}
}

func TestGraph_TopologicalSort_CycleAllowed(t *testing.T) {
	// Realistic revise loop: start -> process -> review -> process (cycle)
	// With allowCycles=true, we should get a partial order containing 'start'
	g := NewGraph("test")
	g.AddNode(NewNoopNode("start"))
	g.AddNode(NewNoopNode("process"))
	g.AddNode(NewNoopNode("review"))
	g.AddEdge("start", "process")
	g.AddEdge("process", "review")
	g.AddEdge("review", "process") // Creates cycle

	order, err := g.TopologicalSort(true)

	if err != nil {
		t.Errorf("TopologicalSort(allowCycles=true) error = %v", err)
	}
	// With cycles allowed, we get partial order (at least 'start' which has no predecessors)
	if len(order) < 1 {
		t.Error("TopologicalSort(allowCycles=true) should return partial order")
	}
	// 'start' should be in the result since it has no incoming edges
	found := false
	for _, id := range order {
		if id == "start" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'start' should be in partial order")
	}
}

func TestGraph_Reachable(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))
	g.AddNode(NewNoopNode("c"))
	g.AddNode(NewNoopNode("d")) // Disconnected
	g.AddEdge("a", "b")
	g.AddEdge("b", "c")

	reachable := g.Reachable("a")

	if len(reachable) != 3 {
		t.Errorf("len(Reachable('a')) = %v, want 3", len(reachable))
	}

	// Check that 'd' is not reachable from 'a'
	for _, id := range reachable {
		if id == "d" {
			t.Error("'d' should not be reachable from 'a'")
		}
	}
}

func TestGraph_NodeOrder(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(NewNoopNode("c"))
	g.AddNode(NewNoopNode("a"))
	g.AddNode(NewNoopNode("b"))

	nodes := g.Nodes()

	// Should preserve insertion order
	expected := []string{"c", "a", "b"}
	for i, n := range nodes {
		if n.ID() != expected[i] {
			t.Errorf("Nodes()[%d].ID() = %v, want %v", i, n.ID(), expected[i])
		}
	}
}

func TestGraph_InterfaceCompliance(t *testing.T) {
	var _ Graph = (*BasicGraph)(nil)
}
