package graph_test

import (
	"testing"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
)

func TestNewGraphBuilder(t *testing.T) {
	b := graph.NewGraphBuilder("test-workflow")

	if b.Graph().Name() != "test-workflow" {
		t.Errorf("expected name 'test-workflow', got %q", b.Graph().Name())
	}
	if len(b.Current()) != 0 {
		t.Errorf("expected empty current, got %v", b.Current())
	}
	if len(b.Errors()) != 0 {
		t.Errorf("expected no errors, got %v", b.Errors())
	}
}

func TestGraphBuilder_AddNode(t *testing.T) {
	node := core.NewNoopNode("first")
	b := graph.NewGraphBuilder("test").AddNode(node)

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}
	if len(b.Current()) != 1 || b.Current()[0] != "first" {
		t.Errorf("expected current ['first'], got %v", b.Current())
	}

	// First node should be entry
	if b.Graph().Entry() != "first" {
		t.Errorf("expected entry 'first', got %q", b.Graph().Entry())
	}
}

func TestGraphBuilder_AddNode_Nil(t *testing.T) {
	b := graph.NewGraphBuilder("test").AddNode(nil)

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_AddNode_Duplicate(t *testing.T) {
	node1 := core.NewNoopNode("dup")
	node2 := core.NewNoopNode("dup")
	b := graph.NewGraphBuilder("test").AddNode(node1).AddNode(node2)

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error for duplicate, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_Entry(t *testing.T) {
	node1 := core.NewNoopNode("first")
	node2 := core.NewNoopNode("second")
	b := graph.NewGraphBuilder("test").
		AddNode(node1).
		AddNode(node2).
		Entry("second")

	if b.Graph().Entry() != "second" {
		t.Errorf("expected entry 'second', got %q", b.Graph().Entry())
	}
}

func TestGraphBuilder_Edge(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		Edge(core.NewNoopNode("b")).
		Edge(core.NewNoopNode("c"))

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	// Check edges
	successors := b.Graph().Successors("a")
	if len(successors) != 1 || successors[0] != "b" {
		t.Errorf("expected a->b, got %v", successors)
	}

	successors = b.Graph().Successors("b")
	if len(successors) != 1 || successors[0] != "c" {
		t.Errorf("expected b->c, got %v", successors)
	}

	// Current should be last node
	if len(b.Current()) != 1 || b.Current()[0] != "c" {
		t.Errorf("expected current ['c'], got %v", b.Current())
	}
}

func TestGraphBuilder_EdgeTo(t *testing.T) {
	existing := core.NewNoopNode("existing")
	b := graph.NewGraphBuilder("test").
		WithNodes(existing).
		AddNode(core.NewNoopNode("a")).
		EdgeTo("existing")

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	successors := b.Graph().Successors("a")
	if len(successors) != 1 || successors[0] != "existing" {
		t.Errorf("expected a->existing, got %v", successors)
	}

	if b.Current()[0] != "existing" {
		t.Errorf("expected current 'existing', got %v", b.Current())
	}
}

func TestGraphBuilder_EdgeTo_NotFound(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		EdgeTo("nonexistent")

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_Connect(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		WithNodes(core.NewNoopNode("a"), core.NewNoopNode("b")).
		Entry("a").
		Connect("a", "b")

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	successors := b.Graph().Successors("a")
	if len(successors) != 1 || successors[0] != "b" {
		t.Errorf("expected a->b, got %v", successors)
	}
}

func TestGraphBuilder_FanOut_Basic(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("start")).
		FanOut(
			core.NewNoopNode("branch-a"),
			core.NewNoopNode("branch-b"),
			core.NewNoopNode("branch-c"),
		)

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	// Check edges from start to all branches
	successors := b.Graph().Successors("start")
	if len(successors) != 3 {
		t.Errorf("expected 3 successors, got %d", len(successors))
	}

	// Current should be all branches
	if len(b.Current()) != 3 {
		t.Errorf("expected 3 current nodes, got %d", len(b.Current()))
	}
}

func TestGraphBuilder_FanOut_Empty(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("start")).
		FanOut()

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_FanOut_NoCurrent(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		FanOut(core.NewNoopNode("a"))

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_FanOutTo(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		WithNodes(
			core.NewNoopNode("start"),
			core.NewNoopNode("branch-a"),
			core.NewNoopNode("branch-b"),
		).
		Entry("start").
		Branch("start").
		FanOutTo("branch-a", "branch-b")

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	successors := b.Graph().Successors("start")
	if len(successors) != 2 {
		t.Errorf("expected 2 successors, got %d", len(successors))
	}

	if len(b.Current()) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.Current()))
	}
}

func TestGraphBuilder_Merge(t *testing.T) {
	merger := nodes.NewMergeNode("combiner", nodes.MergeNodeConfig{})

	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("start")).
		FanOut(
			core.NewNoopNode("branch-a"),
			core.NewNoopNode("branch-b"),
		).
		Merge(merger)

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	// Check edges from branches to merger
	predA := b.Graph().Predecessors("combiner")
	if len(predA) != 2 {
		t.Errorf("expected 2 predecessors for merger, got %d", len(predA))
	}

	// Current should be merger
	if len(b.Current()) != 1 || b.Current()[0] != "combiner" {
		t.Errorf("expected current ['combiner'], got %v", b.Current())
	}

	// ExpectedInputs should be set
	if merger.Config().ExpectedInputs != 2 {
		t.Errorf("expected ExpectedInputs 2, got %d", merger.Config().ExpectedInputs)
	}
}

func TestGraphBuilder_Merge_Nil(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		Merge(nil)

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_MergeTo(t *testing.T) {
	merger := nodes.NewMergeNode("combiner", nodes.MergeNodeConfig{ExpectedInputs: 2})

	b := graph.NewGraphBuilder("test").
		WithNodes(merger).
		AddNode(core.NewNoopNode("start")).
		FanOut(
			core.NewNoopNode("branch-a"),
			core.NewNoopNode("branch-b"),
		).
		MergeTo("combiner")

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	if b.Current()[0] != "combiner" {
		t.Errorf("expected current 'combiner', got %v", b.Current())
	}
}

func TestGraphBuilder_MergeTo_NotMergeNode(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		WithNodes(core.NewNoopNode("not-merge")).
		AddNode(core.NewNoopNode("a")).
		MergeTo("not-merge")

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error for non-merge node, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_Branch(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		WithNodes(core.NewNoopNode("a"), core.NewNoopNode("b")).
		Entry("a").
		Branch("b")

	if len(b.Current()) != 1 || b.Current()[0] != "b" {
		t.Errorf("expected current ['b'], got %v", b.Current())
	}
}

func TestGraphBuilder_Branch_NotFound(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		Branch("nonexistent")

	if len(b.Errors()) != 1 {
		t.Errorf("expected 1 error, got %d", len(b.Errors()))
	}
}

func TestGraphBuilder_Branches(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		WithNodes(core.NewNoopNode("a"), core.NewNoopNode("b"), core.NewNoopNode("c")).
		Entry("a").
		Branches("b", "c")

	if len(b.Current()) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.Current()))
	}
}

func TestGraphBuilder_WithNodes(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		WithNodes(
			core.NewNoopNode("a"),
			core.NewNoopNode("b"),
			core.NewNoopNode("c"),
		)

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	nodesList := b.Graph().Nodes()
	if len(nodesList) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(nodesList))
	}

	// WithNodes should not change current
	if len(b.Current()) != 0 {
		t.Errorf("expected empty current, got %v", b.Current())
	}
}

func TestGraphBuilder_Current(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		FanOut(core.NewNoopNode("b"), core.NewNoopNode("c"))

	current := b.Current()
	if len(current) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(current))
	}
}

func TestGraphBuilder_Build_Success(t *testing.T) {
	g, err := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		Edge(core.NewNoopNode("b")).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g == nil {
		t.Fatal("expected non-nil graph")
	}
	if g.Name() != "test" {
		t.Errorf("expected name 'test', got %q", g.Name())
	}
}

func TestGraphBuilder_Build_WithErrors(t *testing.T) {
	_, err := graph.NewGraphBuilder("test").
		AddNode(nil). // This causes an error
		Build()

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGraphBuilder_Build_ValidationFails(t *testing.T) {
	// Create builder but don't add any nodes
	b := graph.NewGraphBuilder("test")
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

	g := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		MustBuild()

	if g == nil {
		t.Error("expected non-nil graph")
	}
}

func TestGraphBuilder_MustBuild_Panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic, got nil")
		}
	}()

	graph.NewGraphBuilder("test").
		AddNode(nil).
		MustBuild()
}

func TestGraphBuilder_FanOutBranches_Basic(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("start")).
		FanOutBranches(
			graph.NewBranch(core.NewNoopNode("branch-a")),
			graph.NewBranch(core.NewNoopNode("branch-b")),
		)

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	// Check structure
	successors := b.Graph().Successors("start")
	if len(successors) != 2 {
		t.Errorf("expected 2 successors, got %d", len(successors))
	}

	// Current should be the branch ends
	if len(b.Current()) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.Current()))
	}
}

func TestGraphBuilder_FanOutBranches_Pipeline(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("start")).
		FanOutBranches(
			graph.NewPipelineBranch(
				core.NewNoopNode("a1"),
				core.NewNoopNode("a2"),
				core.NewNoopNode("a3"),
			),
			graph.NewPipelineBranch(
				core.NewNoopNode("b1"),
				core.NewNoopNode("b2"),
			),
		)

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	// Check pipeline structure
	// start -> a1 -> a2 -> a3
	// start -> b1 -> b2
	if b.Graph().Successors("a1")[0] != "a2" {
		t.Error("expected a1 -> a2")
	}
	if b.Graph().Successors("a2")[0] != "a3" {
		t.Error("expected a2 -> a3")
	}
	if b.Graph().Successors("b1")[0] != "b2" {
		t.Error("expected b1 -> b2")
	}

	// Current should be the last nodes of each branch
	if len(b.Current()) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.Current()))
	}
	// Current should be a3 and b2
	currentSet := make(map[string]bool)
	for _, c := range b.Current() {
		currentSet[c] = true
	}
	if !currentSet["a3"] || !currentSet["b2"] {
		t.Errorf("expected current [a3, b2], got %v", b.Current())
	}
}

func TestGraphBuilder_Conditional(t *testing.T) {
	router := nodes.NewRuleRouter("classifier", nodes.RuleRouterConfig{
		Rules: []nodes.RouteRule{
			{Target: "positive", Reason: "Positive sentiment"},
			{Target: "negative", Reason: "Negative sentiment"},
		},
	})

	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("start")).
		Conditional(router,
			core.NewNoopNode("positive"),
			core.NewNoopNode("negative"),
		)

	if len(b.Errors()) != 0 {
		t.Fatalf("unexpected errors: %v", b.Errors())
	}

	// Check structure
	successors := b.Graph().Successors("start")
	if len(successors) != 1 || successors[0] != "classifier" {
		t.Errorf("expected start->classifier, got %v", successors)
	}

	routerSuccessors := b.Graph().Successors("classifier")
	if len(routerSuccessors) != 2 {
		t.Errorf("expected 2 router successors, got %d", len(routerSuccessors))
	}

	// Current should be the targets
	if len(b.Current()) != 2 {
		t.Errorf("expected 2 current nodes, got %d", len(b.Current()))
	}
}

func TestGraphBuilder_Terminal(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a")).
		Terminal()

	if len(b.Current()) != 0 {
		t.Errorf("expected empty current after Terminal, got %v", b.Current())
	}
}

func TestGraphBuilder_ComplexWorkflow(t *testing.T) {
	// Build a complex workflow:
	// start -> router -> (branch-a, branch-b) -> merge -> end

	g, err := graph.NewGraphBuilder("complex").
		AddNode(core.NewNoopNode("start")).
		Edge(nodes.NewRuleRouter("router", nodes.RuleRouterConfig{
			Rules: []nodes.RouteRule{
				{Target: "branch-a", Reason: "Route A"},
				{Target: "branch-b", Reason: "Route B"},
			},
			AllowMultiple: true,
		})).
		FanOut(
			core.NewNoopNode("branch-a"),
			core.NewNoopNode("branch-b"),
		).
		Merge(nodes.NewMergeNode("merge", nodes.MergeNodeConfig{})).
		Edge(core.NewNoopNode("end")).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify structure
	if g.Entry() != "start" {
		t.Errorf("expected entry 'start', got %q", g.Entry())
	}

	nodesList := g.Nodes()
	if len(nodesList) != 6 {
		t.Errorf("expected 6 nodes, got %d", len(nodesList))
	}

	// Verify path
	if g.Successors("start")[0] != "router" {
		t.Error("expected start -> router")
	}
	if len(g.Successors("router")) != 2 {
		t.Error("expected router to have 2 successors")
	}
	if g.Successors("merge")[0] != "end" {
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

	g, err := graph.NewGraphBuilder("diamond").
		AddNode(core.NewNoopNode("start")).
		FanOut(
			core.NewNoopNode("a"),
			core.NewNoopNode("b"),
		).
		Merge(nodes.NewMergeNode("merge", nodes.MergeNodeConfig{})).
		Build()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify diamond structure
	if len(g.Successors("start")) != 2 {
		t.Errorf("expected 2 successors from start, got %d", len(g.Successors("start")))
	}
	if len(g.Predecessors("merge")) != 2 {
		t.Errorf("expected 2 predecessors to merge, got %d", len(g.Predecessors("merge")))
	}
}

func TestGraphBuilder_Graph_RawAccess(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(core.NewNoopNode("a"))

	raw := b.Graph()
	if raw == nil {
		t.Error("expected non-nil raw graph")
	}
	if raw.Name() != "test" {
		t.Errorf("expected name 'test', got %q", raw.Name())
	}
}

func TestGraphBuilder_Errors(t *testing.T) {
	b := graph.NewGraphBuilder("test").
		AddNode(nil).
		AddNode(nil)

	errs := b.Errors()
	if len(errs) != 2 {
		t.Errorf("expected 2 errors, got %d", len(errs))
	}
}

func TestNewBranch(t *testing.T) {
	node := core.NewNoopNode("test")
	branch := graph.NewBranch(node)

	if branch.Entry != node {
		t.Error("expected entry to be the node")
	}
	if len(branch.Nodes) != 0 {
		t.Errorf("expected no additional nodes, got %d", len(branch.Nodes))
	}
}

func TestNewPipelineBranch(t *testing.T) {
	n1 := core.NewNoopNode("n1")
	n2 := core.NewNoopNode("n2")
	n3 := core.NewNoopNode("n3")

	branch := graph.NewPipelineBranch(n1, n2, n3)

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
	branch := graph.NewPipelineBranch()

	if branch.Entry != nil {
		t.Error("expected nil entry for empty pipeline")
	}
}
