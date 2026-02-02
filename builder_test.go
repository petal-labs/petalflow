package petalflow

import (
	"testing"
)

func TestNewGraphBuilder(t *testing.T) {
	b := NewGraphBuilder("test-workflow")

	if b.graph.Name() != "test-workflow" {
		t.Errorf("expected name 'test-workflow', got %q", b.graph.Name())
	}
	if len(b.current) != 0 {
		t.Errorf("expected empty current, got %v", b.current)
	}
	if len(b.errors) != 0 {
		t.Errorf("expected no errors, got %v", b.errors)
	}
}

func TestGraphBuilder_AddNode(t *testing.T) {
	node := NewNoopNode("first")
	b := NewGraphBuilder("test").AddNode(node)

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}
	if len(b.current) != 1 || b.current[0] != "first" {
		t.Errorf("expected current ['first'], got %v", b.current)
	}

	// First node should be entry
	if b.graph.Entry() != "first" {
		t.Errorf("expected entry 'first', got %q", b.graph.Entry())
	}
}

func TestGraphBuilder_AddNode_Nil(t *testing.T) {
	b := NewGraphBuilder("test").AddNode(nil)

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.errors))
	}
}

func TestGraphBuilder_AddNode_Duplicate(t *testing.T) {
	node1 := NewNoopNode("dup")
	node2 := NewNoopNode("dup")
	b := NewGraphBuilder("test").AddNode(node1).AddNode(node2)

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error for duplicate, got %d", len(b.errors))
	}
}

func TestGraphBuilder_Entry(t *testing.T) {
	node1 := NewNoopNode("first")
	node2 := NewNoopNode("second")
	b := NewGraphBuilder("test").
		AddNode(node1).
		AddNode(node2).
		Entry("second")

	if b.graph.Entry() != "second" {
		t.Errorf("expected entry 'second', got %q", b.graph.Entry())
	}
}

func TestGraphBuilder_Edge(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		Edge(NewNoopNode("b")).
		Edge(NewNoopNode("c"))

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	// Check edges
	successors := b.graph.Successors("a")
	if len(successors) != 1 || successors[0] != "b" {
		t.Errorf("expected a->b, got %v", successors)
	}

	successors = b.graph.Successors("b")
	if len(successors) != 1 || successors[0] != "c" {
		t.Errorf("expected b->c, got %v", successors)
	}

	// Current should be last node
	if len(b.current) != 1 || b.current[0] != "c" {
		t.Errorf("expected current ['c'], got %v", b.current)
	}
}

func TestGraphBuilder_EdgeTo(t *testing.T) {
	existing := NewNoopNode("existing")
	b := NewGraphBuilder("test").
		WithNodes(existing).
		AddNode(NewNoopNode("a")).
		EdgeTo("existing")

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	successors := b.graph.Successors("a")
	if len(successors) != 1 || successors[0] != "existing" {
		t.Errorf("expected a->existing, got %v", successors)
	}

	if b.current[0] != "existing" {
		t.Errorf("expected current 'existing', got %v", b.current)
	}
}

func TestGraphBuilder_EdgeTo_NotFound(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		EdgeTo("nonexistent")

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.errors))
	}
}

func TestGraphBuilder_Connect(t *testing.T) {
	b := NewGraphBuilder("test").
		WithNodes(NewNoopNode("a"), NewNoopNode("b")).
		Entry("a").
		Connect("a", "b")

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	successors := b.graph.Successors("a")
	if len(successors) != 1 || successors[0] != "b" {
		t.Errorf("expected a->b, got %v", successors)
	}
}

func TestGraphBuilder_FanOut_Basic(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("start")).
		FanOut(
			NewNoopNode("branch-a"),
			NewNoopNode("branch-b"),
			NewNoopNode("branch-c"),
		)

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	// Check edges from start to all branches
	successors := b.graph.Successors("start")
	if len(successors) != 3 {
		t.Errorf("expected 3 successors, got %d", len(successors))
	}

	// Current should be all branches
	if len(b.current) != 3 {
		t.Errorf("expected 3 current nodes, got %d", len(b.current))
	}
}

func TestGraphBuilder_FanOut_Empty(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("start")).
		FanOut()

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.errors))
	}
}

func TestGraphBuilder_FanOut_NoCurrent(t *testing.T) {
	b := NewGraphBuilder("test").
		FanOut(NewNoopNode("a"))

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.errors))
	}
}

func TestGraphBuilder_FanOutTo(t *testing.T) {
	b := NewGraphBuilder("test").
		WithNodes(
			NewNoopNode("start"),
			NewNoopNode("branch-a"),
			NewNoopNode("branch-b"),
		).
		Entry("start").
		Branch("start").
		FanOutTo("branch-a", "branch-b")

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	successors := b.graph.Successors("start")
	if len(successors) != 2 {
		t.Errorf("expected 2 successors, got %d", len(successors))
	}

	if len(b.current) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.current))
	}
}

func TestGraphBuilder_Merge(t *testing.T) {
	merger := NewMergeNode("combiner", MergeNodeConfig{})

	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("start")).
		FanOut(
			NewNoopNode("branch-a"),
			NewNoopNode("branch-b"),
		).
		Merge(merger)

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	// Check edges from branches to merger
	predA := b.graph.Predecessors("combiner")
	if len(predA) != 2 {
		t.Errorf("expected 2 predecessors for merger, got %d", len(predA))
	}

	// Current should be merger
	if len(b.current) != 1 || b.current[0] != "combiner" {
		t.Errorf("expected current ['combiner'], got %v", b.current)
	}

	// ExpectedInputs should be set
	if merger.config.ExpectedInputs != 2 {
		t.Errorf("expected ExpectedInputs 2, got %d", merger.config.ExpectedInputs)
	}
}

func TestGraphBuilder_Merge_Nil(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		Merge(nil)

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.errors))
	}
}

func TestGraphBuilder_MergeTo(t *testing.T) {
	merger := NewMergeNode("combiner", MergeNodeConfig{ExpectedInputs: 2})

	b := NewGraphBuilder("test").
		WithNodes(merger).
		AddNode(NewNoopNode("start")).
		FanOut(
			NewNoopNode("branch-a"),
			NewNoopNode("branch-b"),
		).
		MergeTo("combiner")

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	if b.current[0] != "combiner" {
		t.Errorf("expected current 'combiner', got %v", b.current)
	}
}

func TestGraphBuilder_MergeTo_NotMergeNode(t *testing.T) {
	b := NewGraphBuilder("test").
		WithNodes(NewNoopNode("not-merge")).
		AddNode(NewNoopNode("a")).
		MergeTo("not-merge")

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error for non-merge node, got %d", len(b.errors))
	}
}

func TestGraphBuilder_Branch(t *testing.T) {
	b := NewGraphBuilder("test").
		WithNodes(NewNoopNode("a"), NewNoopNode("b")).
		Entry("a").
		Branch("b")

	if len(b.current) != 1 || b.current[0] != "b" {
		t.Errorf("expected current ['b'], got %v", b.current)
	}
}

func TestGraphBuilder_Branch_NotFound(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		Branch("nonexistent")

	if len(b.errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.errors))
	}
}

func TestGraphBuilder_Branches(t *testing.T) {
	b := NewGraphBuilder("test").
		WithNodes(NewNoopNode("a"), NewNoopNode("b"), NewNoopNode("c")).
		Entry("a").
		Branches("b", "c")

	if len(b.current) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.current))
	}
}

func TestGraphBuilder_WithNodes(t *testing.T) {
	b := NewGraphBuilder("test").
		WithNodes(
			NewNoopNode("a"),
			NewNoopNode("b"),
			NewNoopNode("c"),
		)

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	nodes := b.graph.Nodes()
	if len(nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(nodes))
	}

	// WithNodes should not change current
	if len(b.current) != 0 {
		t.Errorf("expected empty current, got %v", b.current)
	}
}

func TestGraphBuilder_Current(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		FanOut(NewNoopNode("b"), NewNoopNode("c"))

	current := b.Current()
	if len(current) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(current))
	}
}

func TestGraphBuilder_Build_Success(t *testing.T) {
	graph, err := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		Edge(NewNoopNode("b")).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if graph == nil {
		t.Fatal("expected non-nil graph")
	}
	if graph.Name() != "test" {
		t.Errorf("expected name 'test', got %q", graph.Name())
	}
}

func TestGraphBuilder_Build_WithErrors(t *testing.T) {
	_, err := NewGraphBuilder("test").
		AddNode(nil). // This causes an error
		Build()

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGraphBuilder_Build_ValidationFails(t *testing.T) {
	// Create builder but don't add any nodes
	b := NewGraphBuilder("test")
	_, err := b.Build()

	if err == nil {
		t.Fatal("expected validation error for empty graph")
	}
}

func TestGraphBuilder_MustBuild_Success(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("unexpected panic: %v", r)
		}
	}()

	graph := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		MustBuild()

	if graph == nil {
		t.Error("expected non-nil graph")
	}
}

func TestGraphBuilder_MustBuild_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic, got nil")
		}
	}()

	NewGraphBuilder("test").
		AddNode(nil).
		MustBuild()
}

func TestGraphBuilder_FanOutBranches_Basic(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("start")).
		FanOutBranches(
			NewBranch(NewNoopNode("branch-a")),
			NewBranch(NewNoopNode("branch-b")),
		)

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	// Check structure
	successors := b.graph.Successors("start")
	if len(successors) != 2 {
		t.Errorf("expected 2 successors, got %d", len(successors))
	}

	// Current should be the branch ends
	if len(b.current) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.current))
	}
}

func TestGraphBuilder_FanOutBranches_Pipeline(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("start")).
		FanOutBranches(
			NewPipelineBranch(
				NewNoopNode("a1"),
				NewNoopNode("a2"),
				NewNoopNode("a3"),
			),
			NewPipelineBranch(
				NewNoopNode("b1"),
				NewNoopNode("b2"),
			),
		)

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	// Check pipeline structure
	// start -> a1 -> a2 -> a3
	// start -> b1 -> b2
	if b.graph.Successors("a1")[0] != "a2" {
		t.Error("expected a1 -> a2")
	}
	if b.graph.Successors("a2")[0] != "a3" {
		t.Error("expected a2 -> a3")
	}
	if b.graph.Successors("b1")[0] != "b2" {
		t.Error("expected b1 -> b2")
	}

	// Current should be the last nodes of each branch
	if len(b.current) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.current))
	}
	// Current should be a3 and b2
	currentSet := make(map[string]bool)
	for _, c := range b.current {
		currentSet[c] = true
	}
	if !currentSet["a3"] || !currentSet["b2"] {
		t.Errorf("expected current [a3, b2], got %v", b.current)
	}
}

func TestGraphBuilder_Conditional(t *testing.T) {
	router := NewRuleRouter("classifier", RuleRouterConfig{
		Rules: []RouteRule{
			{Target: "positive", Reason: "Positive sentiment"},
			{Target: "negative", Reason: "Negative sentiment"},
		},
	})

	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("start")).
		Conditional(router,
			NewNoopNode("positive"),
			NewNoopNode("negative"),
		)

	if len(b.errors) != 0 {
		t.Fatalf("unexpected errors: %v", b.errors)
	}

	// Check structure
	successors := b.graph.Successors("start")
	if len(successors) != 1 || successors[0] != "classifier" {
		t.Errorf("expected start->classifier, got %v", successors)
	}

	routerSuccessors := b.graph.Successors("classifier")
	if len(routerSuccessors) != 2 {
		t.Errorf("expected 2 router successors, got %d", len(routerSuccessors))
	}

	// Current should be the targets
	if len(b.current) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.current))
	}
}

func TestGraphBuilder_Terminal(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("a")).
		Terminal()

	if len(b.current) != 0 {
		t.Errorf("expected empty current after Terminal, got %v", b.current)
	}
}

func TestGraphBuilder_ComplexWorkflow(t *testing.T) {
	// Build a complex workflow:
	// start -> router -> (branch-a, branch-b) -> merge -> end

	graph, err := NewGraphBuilder("complex").
		AddNode(NewNoopNode("start")).
		Edge(NewRuleRouter("router", RuleRouterConfig{
			Rules: []RouteRule{
				{Target: "branch-a", Reason: "Route A"},
				{Target: "branch-b", Reason: "Route B"},
			},
			AllowMultiple: true,
		})).
		FanOut(
			NewNoopNode("branch-a"),
			NewNoopNode("branch-b"),
		).
		Merge(NewMergeNode("merge", MergeNodeConfig{})).
		Edge(NewNoopNode("end")).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify structure
	if graph.Entry() != "start" {
		t.Errorf("expected entry 'start', got %q", graph.Entry())
	}

	nodes := graph.Nodes()
	if len(nodes) != 6 {
		t.Errorf("expected 6 nodes, got %d", len(nodes))
	}

	// Verify path
	if graph.Successors("start")[0] != "router" {
		t.Error("expected start -> router")
	}
	if len(graph.Successors("router")) != 2 {
		t.Error("expected router to have 2 successors")
	}
	if graph.Successors("merge")[0] != "end" {
		t.Error("expected merge -> end")
	}
}

func TestGraphBuilder_DiamondPattern(t *testing.T) {
	// Diamond pattern:
	//     start
	//    /     \
	//   a       b
	//    \     /
	//     merge

	graph, err := NewGraphBuilder("diamond").
		AddNode(NewNoopNode("start")).
		FanOut(
			NewNoopNode("a"),
			NewNoopNode("b"),
		).
		Merge(NewMergeNode("merge", MergeNodeConfig{})).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify diamond structure
	if len(graph.Successors("start")) != 2 {
		t.Errorf("expected 2 successors from start, got %d", len(graph.Successors("start")))
	}
	if len(graph.Predecessors("merge")) != 2 {
		t.Errorf("expected 2 predecessors to merge, got %d", len(graph.Predecessors("merge")))
	}
}

func TestGraphBuilder_Graph_RawAccess(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(NewNoopNode("a"))

	raw := b.Graph()
	if raw == nil {
		t.Error("expected non-nil raw graph")
	}
	if raw.Name() != "test" {
		t.Errorf("expected name 'test', got %q", raw.Name())
	}
}

func TestGraphBuilder_Errors(t *testing.T) {
	b := NewGraphBuilder("test").
		AddNode(nil).
		AddNode(nil)

	errs := b.Errors()
	if len(errs) != 2 {
		t.Errorf("expected 2 errors, got %d", len(errs))
	}
}

func TestNewBranch(t *testing.T) {
	node := NewNoopNode("test")
	branch := NewBranch(node)

	if branch.Entry != node {
		t.Error("expected entry to be the node")
	}
	if len(branch.Nodes) != 0 {
		t.Errorf("expected no additional nodes, got %d", len(branch.Nodes))
	}
}

func TestNewPipelineBranch(t *testing.T) {
	n1 := NewNoopNode("n1")
	n2 := NewNoopNode("n2")
	n3 := NewNoopNode("n3")

	branch := NewPipelineBranch(n1, n2, n3)

	if branch.Entry != n1 {
		t.Error("expected entry to be n1")
	}
	if len(branch.Nodes) != 2 {
		t.Errorf("expected 2 additional nodes, got %d", len(branch.Nodes))
	}
	if branch.Nodes[0] != n2 || branch.Nodes[1] != n3 {
		t.Error("expected nodes [n2, n3]")
	}
}

func TestNewPipelineBranch_Empty(t *testing.T) {
	branch := NewPipelineBranch()

	if branch.Entry != nil {
		t.Error("expected nil entry for empty pipeline")
	}
}
