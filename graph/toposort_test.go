package graph

import (
	"testing"

	"github.com/petal-labs/petalflow/core"
)

// TestBasicGraph_TopologicalSort_Deterministic verifies the sort produces the
// same order every call. Nodes "a" and "b" both have in-degree 0, so a
// map-seeded queue would order them nondeterministically across calls.
func TestBasicGraph_TopologicalSort_Deterministic(t *testing.T) {
	g := NewGraph("x")
	for _, id := range []string{"a", "b", "c", "d"} {
		g.AddNode(core.NewNoopNode(id))
	}
	g.AddEdge("a", "c")
	g.AddEdge("b", "c")
	g.AddEdge("c", "d")

	first, err := g.TopologicalSort(false)
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	for i := 0; i < 50; i++ {
		got, err := g.TopologicalSort(false)
		if err != nil {
			t.Fatalf("TopologicalSort() error = %v", err)
		}
		if len(got) != len(first) {
			t.Fatalf("length changed: %v vs %v", got, first)
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("nondeterministic order: run %d = %v, first = %v", i, got, first)
			}
		}
	}
}
