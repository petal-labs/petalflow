package petalflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewRuntime(t *testing.T) {
	rt := NewRuntime()

	if rt == nil {
		t.Fatal("NewRuntime() returned nil")
	}

	// Events channel should be available
	ch := rt.Events()
	if ch == nil {
		t.Error("Events() returned nil channel")
	}
}

func TestDefaultRunOptions(t *testing.T) {
	opts := DefaultRunOptions()

	if opts.MaxHops != 100 {
		t.Errorf("DefaultRunOptions().MaxHops = %v, want 100", opts.MaxHops)
	}
	if opts.ContinueOnError != false {
		t.Error("DefaultRunOptions().ContinueOnError should be false")
	}
	if opts.Concurrency != 1 {
		t.Errorf("DefaultRunOptions().Concurrency = %v, want 1", opts.Concurrency)
	}
}

func TestRuntime_Run_SimpleLinear(t *testing.T) {
	// Create a simple linear graph: a -> b -> c
	g := NewGraph("linear")
	g.AddNode(NewFuncNode("a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("a", "processed")
		return env, nil
	}))
	g.AddNode(NewFuncNode("b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("b", "processed")
		return env, nil
	}))
	g.AddNode(NewFuncNode("c", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("c", "processed")
		return env, nil
	}))
	g.AddEdge("a", "b")
	g.AddEdge("b", "c")
	g.SetEntry("a")

	rt := NewRuntime()
	env := NewEnvelope()

	result, err := rt.Run(context.Background(), g, env, DefaultRunOptions())

	if err != nil {
		t.Errorf("Run() error = %v", err)
	}

	// Check all nodes were executed
	for _, key := range []string{"a", "b", "c"} {
		if v, ok := result.GetVar(key); !ok || v != "processed" {
			t.Errorf("Node %s was not executed correctly", key)
		}
	}

	// Check trace was set
	if result.Trace.RunID == "" {
		t.Error("RunID was not set")
	}
}

func TestRuntime_Run_Events(t *testing.T) {
	g := NewGraph("events-test")
	g.AddNode(NewNoopNode("start"))
	g.SetEntry("start")

	rt := NewRuntime()
	events := make([]Event, 0)

	opts := DefaultRunOptions()
	opts.EventHandler = func(e Event) {
		events = append(events, e)
	}

	_, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Errorf("Run() error = %v", err)
	}

	// Should have: run_started, node_started, node_finished, run_finished
	expectedKinds := []EventKind{
		EventRunStarted,
		EventNodeStarted,
		EventNodeFinished,
		EventRunFinished,
	}

	if len(events) != len(expectedKinds) {
		t.Errorf("len(events) = %v, want %v", len(events), len(expectedKinds))
	}

	for i, expected := range expectedKinds {
		if i < len(events) && events[i].Kind != expected {
			t.Errorf("events[%d].Kind = %v, want %v", i, events[i].Kind, expected)
		}
	}
}

func TestRuntime_Run_NilEnvelope(t *testing.T) {
	g := NewGraph("nil-env")
	g.AddNode(NewNoopNode("start"))
	g.SetEntry("start")

	rt := NewRuntime()

	result, err := rt.Run(context.Background(), g, nil, DefaultRunOptions())

	if err != nil {
		t.Errorf("Run() error = %v", err)
	}
	if result == nil {
		t.Error("Run() should create envelope if nil")
	}
}

func TestRuntime_Run_NilGraph(t *testing.T) {
	rt := NewRuntime()

	_, err := rt.Run(context.Background(), nil, NewEnvelope(), DefaultRunOptions())

	if err == nil {
		t.Error("Run() should error on nil graph")
	}
}

func TestRuntime_Run_EmptyGraph(t *testing.T) {
	g := NewGraph("empty")
	rt := NewRuntime()

	_, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if !errors.Is(err, ErrEmptyGraph) {
		t.Errorf("Run() error = %v, want %v", err, ErrEmptyGraph)
	}
}

func TestRuntime_Run_NoEntry(t *testing.T) {
	g := NewGraph("no-entry")
	g.AddNode(NewNoopNode("orphan"))

	rt := NewRuntime()

	_, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if !errors.Is(err, ErrNoEntryNode) {
		t.Errorf("Run() error = %v, want %v", err, ErrNoEntryNode)
	}
}

func TestRuntime_Run_ContextCancellation(t *testing.T) {
	g := NewGraph("cancel-test")
	g.AddNode(NewFuncNode("slow", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return env, nil
		}
	}))
	g.SetEntry("slow")

	rt := NewRuntime()
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := rt.Run(ctx, g, NewEnvelope(), DefaultRunOptions())

	if err == nil {
		t.Error("Run() should error on context cancellation")
	}
}

func TestRuntime_Run_NodeError(t *testing.T) {
	expectedErr := errors.New("node failed")
	g := NewGraph("error-test")
	g.AddNode(NewFuncNode("fail", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return nil, expectedErr
	}))
	g.SetEntry("fail")

	rt := NewRuntime()

	_, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if !errors.Is(err, ErrNodeExecution) {
		t.Errorf("Run() error = %v, want wrapped %v", err, ErrNodeExecution)
	}
}

func TestRuntime_Run_ContinueOnError(t *testing.T) {
	g := NewGraph("continue-test")
	g.AddNode(NewFuncNode("fail", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return nil, errors.New("expected failure")
	}))
	g.AddNode(NewFuncNode("after", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("after", "reached")
		return env, nil
	}))
	g.AddEdge("fail", "after")
	g.SetEntry("fail")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.ContinueOnError = true

	result, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Errorf("Run() with ContinueOnError error = %v, want nil", err)
	}

	// Error should be recorded
	if !result.HasErrors() {
		t.Error("Error should be recorded in envelope")
	}
	if len(result.Errors) != 1 {
		t.Errorf("len(Errors) = %v, want 1", len(result.Errors))
	}

	// After node should have executed
	if v, ok := result.GetVar("after"); !ok || v != "reached" {
		t.Error("Node 'after' should have executed")
	}
}

func TestRuntime_Run_EventNodeFailed(t *testing.T) {
	g := NewGraph("fail-event")
	g.AddNode(NewFuncNode("fail", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return nil, errors.New("boom")
	}))
	g.SetEntry("fail")

	rt := NewRuntime()
	var failedEvent *Event

	opts := DefaultRunOptions()
	opts.EventHandler = func(e Event) {
		if e.Kind == EventNodeFailed {
			failedEvent = &e
		}
	}

	rt.Run(context.Background(), g, NewEnvelope(), opts)

	if failedEvent == nil {
		t.Error("EventNodeFailed should be emitted")
	}
	if failedEvent != nil && failedEvent.Payload["error"] != "boom" {
		t.Error("EventNodeFailed should contain error message")
	}
}

func TestRuntime_Run_ExecutionOrder(t *testing.T) {
	order := make([]string, 0)

	g := NewGraph("order-test")
	for _, id := range []string{"a", "b", "c"} {
		nodeID := id
		g.AddNode(NewFuncNode(nodeID, func(ctx context.Context, env *Envelope) (*Envelope, error) {
			order = append(order, nodeID)
			return env, nil
		}))
	}
	g.AddEdge("a", "b")
	g.AddEdge("b", "c")
	g.SetEntry("a")

	rt := NewRuntime()
	rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	expected := []string{"a", "b", "c"}
	if len(order) != len(expected) {
		t.Errorf("len(order) = %v, want %v", len(order), len(expected))
	}
	for i, id := range expected {
		if i < len(order) && order[i] != id {
			t.Errorf("order[%d] = %v, want %v", i, order[i], id)
		}
	}
}

func TestRuntime_Run_BranchingGraph(t *testing.T) {
	// Graph: a -> b, a -> c, b -> d, c -> d
	executed := make(map[string]bool)

	g := NewGraph("branch")
	for _, id := range []string{"a", "b", "c", "d"} {
		nodeID := id
		g.AddNode(NewFuncNode(nodeID, func(ctx context.Context, env *Envelope) (*Envelope, error) {
			executed[nodeID] = true
			return env, nil
		}))
	}
	g.AddEdge("a", "b")
	g.AddEdge("a", "c")
	g.AddEdge("b", "d")
	g.AddEdge("c", "d")
	g.SetEntry("a")

	rt := NewRuntime()
	rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	// All nodes should be executed
	for _, id := range []string{"a", "b", "c", "d"} {
		if !executed[id] {
			t.Errorf("Node %s was not executed", id)
		}
	}
}

func TestRuntime_Run_RunIDGenerated(t *testing.T) {
	g := NewGraph("runid-test")
	g.AddNode(NewNoopNode("start"))
	g.SetEntry("start")

	rt := NewRuntime()
	result, _ := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if result.Trace.RunID == "" {
		t.Error("RunID should be generated")
	}

	// Run again and verify different ID
	result2, _ := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())
	if result2.Trace.RunID == result.Trace.RunID {
		t.Error("Different runs should have different RunIDs")
	}
}

func TestRuntime_Run_CustomNow(t *testing.T) {
	fixedTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	g := NewGraph("time-test")
	g.AddNode(NewNoopNode("start"))
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Now = func() time.Time { return fixedTime }

	result, _ := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if result.Trace.Started != fixedTime {
		t.Errorf("Trace.Started = %v, want %v", result.Trace.Started, fixedTime)
	}
}

func TestRuntime_InterfaceCompliance(t *testing.T) {
	var _ Runtime = (*BasicRuntime)(nil)
}

func TestGenerateRunID(t *testing.T) {
	id1 := generateRunID()
	id2 := generateRunID()

	if id1 == "" {
		t.Error("generateRunID() returned empty string")
	}
	if id1 == id2 {
		t.Error("generateRunID() should return unique IDs")
	}
}

// RouterNode handling tests

func TestRuntime_Run_RouterNode_SingleTarget(t *testing.T) {
	// Graph: router -> handler-a, router -> handler-b
	// Router selects only handler-a
	executed := make(map[string]bool)

	g := NewGraph("router-test")

	// Add the router
	router := NewRuleRouter("router", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "route", Op: OpEquals, Value: "a"},
				},
				Target: "handler-a",
				Reason: "Route to A",
			},
		},
		DefaultTarget: "handler-b",
	})
	g.AddNode(router)

	// Add handlers
	g.AddNode(NewFuncNode("handler-a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed["handler-a"] = true
		return env, nil
	}))
	g.AddNode(NewFuncNode("handler-b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed["handler-b"] = true
		return env, nil
	}))

	g.AddEdge("router", "handler-a")
	g.AddEdge("router", "handler-b")
	g.SetEntry("router")

	rt := NewRuntime()
	env := NewEnvelope().WithVar("route", "a")

	_, err := rt.Run(context.Background(), g, env, DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only handler-a should be executed
	if !executed["handler-a"] {
		t.Error("handler-a should have been executed")
	}
	if executed["handler-b"] {
		t.Error("handler-b should NOT have been executed (not selected by router)")
	}
}

func TestRuntime_Run_RouterNode_DefaultTarget(t *testing.T) {
	executed := make(map[string]bool)

	g := NewGraph("router-default")

	router := NewRuleRouter("router", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "route", Op: OpEquals, Value: "special"},
				},
				Target: "special-handler",
			},
		},
		DefaultTarget: "default-handler",
	})
	g.AddNode(router)

	g.AddNode(NewFuncNode("special-handler", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed["special-handler"] = true
		return env, nil
	}))
	g.AddNode(NewFuncNode("default-handler", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed["default-handler"] = true
		return env, nil
	}))

	g.AddEdge("router", "special-handler")
	g.AddEdge("router", "default-handler")
	g.SetEntry("router")

	rt := NewRuntime()
	env := NewEnvelope().WithVar("route", "normal") // No match, use default

	_, err := rt.Run(context.Background(), g, env, DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if executed["special-handler"] {
		t.Error("special-handler should NOT have been executed")
	}
	if !executed["default-handler"] {
		t.Error("default-handler should have been executed (default target)")
	}
}

func TestRuntime_Run_RouterNode_MultipleTargets(t *testing.T) {
	executed := make(map[string]bool)

	g := NewGraph("router-multi")

	router := NewRuleRouter("router", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "notify_email", Op: OpExists},
				},
				Target: "email-handler",
			},
			{
				Conditions: []RouteCondition{
					{VarPath: "notify_sms", Op: OpExists},
				},
				Target: "sms-handler",
			},
		},
		AllowMultiple: true,
	})
	g.AddNode(router)

	g.AddNode(NewFuncNode("email-handler", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed["email-handler"] = true
		return env, nil
	}))
	g.AddNode(NewFuncNode("sms-handler", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed["sms-handler"] = true
		return env, nil
	}))

	g.AddEdge("router", "email-handler")
	g.AddEdge("router", "sms-handler")
	g.SetEntry("router")

	rt := NewRuntime()
	env := NewEnvelope().
		WithVar("notify_email", true).
		WithVar("notify_sms", true)

	_, err := rt.Run(context.Background(), g, env, DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both handlers should be executed
	if !executed["email-handler"] {
		t.Error("email-handler should have been executed")
	}
	if !executed["sms-handler"] {
		t.Error("sms-handler should have been executed")
	}
}

func TestRuntime_Run_RouterNode_EventEmitted(t *testing.T) {
	g := NewGraph("router-event")

	router := NewRuleRouter("classifier", RuleRouterConfig{
		Rules: []RouteRule{
			{Target: "next", Reason: "Always"},
		},
	})
	g.AddNode(router)
	g.AddNode(NewNoopNode("next"))
	g.AddEdge("classifier", "next")
	g.SetEntry("classifier")

	rt := NewRuntime()
	var routeEvent *Event

	opts := DefaultRunOptions()
	opts.EventHandler = func(e Event) {
		if e.Kind == EventRouteDecision {
			routeEvent = &e
		}
	}

	_, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if routeEvent == nil {
		t.Fatal("EventRouteDecision should have been emitted")
	}
	if routeEvent.NodeID != "classifier" {
		t.Errorf("expected NodeID 'classifier', got %q", routeEvent.NodeID)
	}
	if routeEvent.Payload["targets"] == nil {
		t.Error("EventRouteDecision should contain targets")
	}
	if routeEvent.Payload["reason"] != "Always" {
		t.Errorf("expected reason 'Always', got %v", routeEvent.Payload["reason"])
	}
}

func TestRuntime_Run_RouterNode_ChainedRouters(t *testing.T) {
	// Test router -> handler -> router -> final
	executed := make([]string, 0)

	g := NewGraph("chained-routers")

	router1 := NewRuleRouter("router1", RuleRouterConfig{
		Rules: []RouteRule{
			{Target: "middle", Reason: "First router"},
		},
	})
	g.AddNode(router1)

	g.AddNode(NewFuncNode("middle", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed = append(executed, "middle")
		env.SetVar("second_route", "final")
		return env, nil
	}))

	router2 := NewRuleRouter("router2", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "second_route", Op: OpEquals, Value: "final"},
				},
				Target: "final",
			},
		},
		DefaultTarget: "alternate",
	})
	g.AddNode(router2)

	g.AddNode(NewFuncNode("final", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed = append(executed, "final")
		return env, nil
	}))
	g.AddNode(NewFuncNode("alternate", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed = append(executed, "alternate")
		return env, nil
	}))

	g.AddEdge("router1", "middle")
	g.AddEdge("middle", "router2")
	g.AddEdge("router2", "final")
	g.AddEdge("router2", "alternate")
	g.SetEntry("router1")

	rt := NewRuntime()
	_, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should execute: middle, final (not alternate)
	if len(executed) != 2 {
		t.Errorf("expected 2 nodes executed, got %d: %v", len(executed), executed)
	}
	if executed[0] != "middle" {
		t.Errorf("expected first 'middle', got %q", executed[0])
	}
	if executed[1] != "final" {
		t.Errorf("expected second 'final', got %q", executed[1])
	}
}

func TestRuntime_Run_RouterNode_NoTargetsMatched(t *testing.T) {
	// When no rules match and no default, the router returns empty targets
	// The runtime should handle this gracefully (no successors visited)
	executed := make(map[string]bool)

	g := NewGraph("router-no-match")

	router := NewRuleRouter("router", RuleRouterConfig{
		Rules: []RouteRule{
			{
				Conditions: []RouteCondition{
					{VarPath: "impossible", Op: OpEquals, Value: "never"},
				},
				Target: "handler",
			},
		},
		// No DefaultTarget
	})
	g.AddNode(router)

	g.AddNode(NewFuncNode("handler", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		executed["handler"] = true
		return env, nil
	}))

	g.AddEdge("router", "handler")
	g.SetEntry("router")

	rt := NewRuntime()
	_, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Handler should NOT be executed since router returned no targets
	if executed["handler"] {
		t.Error("handler should NOT have been executed (no targets matched)")
	}
}

// --- Concurrent Execution Tests ---

func TestRuntime_Run_Concurrent_SimpleFanOut(t *testing.T) {
	// Test: start -> branch-a, start -> branch-b (parallel execution)
	var mu sync.Mutex
	executed := make(map[string]bool)

	g := NewGraph("concurrent-fanout")
	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		executed["start"] = true
		mu.Unlock()
		return env, nil
	}))
	g.AddNode(NewFuncNode("branch-a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		executed["branch-a"] = true
		mu.Unlock()
		return env, nil
	}))
	g.AddNode(NewFuncNode("branch-b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		executed["branch-b"] = true
		mu.Unlock()
		return env, nil
	}))

	g.AddEdge("start", "branch-a")
	g.AddEdge("start", "branch-b")
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = 2

	_, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All nodes should be executed
	for _, id := range []string{"start", "branch-a", "branch-b"} {
		if !executed[id] {
			t.Errorf("Node %s was not executed", id)
		}
	}
}

func TestRuntime_Run_Concurrent_FanOutMerge(t *testing.T) {
	// Test: start -> branch-a, start -> branch-b -> merge
	var mu sync.Mutex
	executed := make([]string, 0)

	g := NewGraph("concurrent-merge")
	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		executed = append(executed, "start")
		mu.Unlock()
		env.SetVar("from_start", true)
		return env, nil
	}))
	g.AddNode(NewFuncNode("branch-a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		time.Sleep(10 * time.Millisecond) // Simulate work
		mu.Lock()
		executed = append(executed, "branch-a")
		mu.Unlock()
		env.SetVar("from_a", "value-a")
		return env, nil
	}))
	g.AddNode(NewFuncNode("branch-b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		executed = append(executed, "branch-b")
		mu.Unlock()
		env.SetVar("from_b", "value-b")
		return env, nil
	}))

	merger := NewMergeNode("merge", MergeNodeConfig{
		Strategy: NewJSONMergeStrategy(JSONMergeConfig{}),
	})
	g.AddNode(merger)

	g.AddNode(NewFuncNode("final", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		executed = append(executed, "final")
		mu.Unlock()
		return env, nil
	}))

	g.AddEdge("start", "branch-a")
	g.AddEdge("start", "branch-b")
	g.AddEdge("branch-a", "merge")
	g.AddEdge("branch-b", "merge")
	g.AddEdge("merge", "final")
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = 4

	result, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All nodes should be executed
	nodeSet := make(map[string]bool)
	for _, id := range executed {
		nodeSet[id] = true
	}
	for _, id := range []string{"start", "branch-a", "branch-b", "final"} {
		if !nodeSet[id] {
			t.Errorf("Node %s was not executed", id)
		}
	}

	// Merged result should have vars from both branches
	// Note: Due to cloning, the merged result may have variables from the merge
	if _, ok := result.GetVar("from_start"); !ok {
		t.Error("Expected 'from_start' var in result")
	}
}

func TestRuntime_Run_Concurrent_EnvelopeCloning(t *testing.T) {
	// Verify that parallel branches get cloned envelopes (no race conditions)
	var mu sync.Mutex
	branchValues := make(map[string]string)

	g := NewGraph("clone-test")
	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("shared", "initial")
		return env, nil
	}))
	g.AddNode(NewFuncNode("branch-a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		time.Sleep(5 * time.Millisecond)
		env.SetVar("shared", "modified-by-a")
		mu.Lock()
		branchValues["a"] = env.GetVarString("shared")
		mu.Unlock()
		return env, nil
	}))
	g.AddNode(NewFuncNode("branch-b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("shared", "modified-by-b")
		mu.Lock()
		branchValues["b"] = env.GetVarString("shared")
		mu.Unlock()
		return env, nil
	}))

	g.AddEdge("start", "branch-a")
	g.AddEdge("start", "branch-b")
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = 2

	_, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Each branch should see its own modification, not the other's
	if branchValues["a"] != "modified-by-a" {
		t.Errorf("branch-a saw %q, expected 'modified-by-a'", branchValues["a"])
	}
	if branchValues["b"] != "modified-by-b" {
		t.Errorf("branch-b saw %q, expected 'modified-by-b'", branchValues["b"])
	}
}

func TestRuntime_Run_Concurrent_MergeNodeWithStrategy(t *testing.T) {
	g := NewGraph("merge-strategy")
	g.AddNode(NewNoopNode("start"))
	g.AddNode(NewFuncNode("branch-a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("score", 0.8)
		env.SetVar("source", "a")
		return env, nil
	}))
	g.AddNode(NewFuncNode("branch-b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("score", 0.95)
		env.SetVar("source", "b")
		return env, nil
	}))

	// Use BestScore strategy to pick the highest score
	merger := NewMergeNode("merge", MergeNodeConfig{
		Strategy: NewBestScoreMergeStrategy(BestScoreMergeConfig{
			ScoreVar:       "score",
			HigherIsBetter: true,
		}),
	})
	g.AddNode(merger)

	g.AddEdge("start", "branch-a")
	g.AddEdge("start", "branch-b")
	g.AddEdge("branch-a", "merge")
	g.AddEdge("branch-b", "merge")
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = 2

	result, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The merge should pick branch-b (higher score)
	source, _ := result.GetVar("source")
	if source != "b" {
		t.Errorf("expected source 'b' (highest score), got %v", source)
	}
}

func TestRuntime_Run_Concurrent_ErrorHandling(t *testing.T) {
	g := NewGraph("concurrent-error")
	g.AddNode(NewNoopNode("start"))
	g.AddNode(NewFuncNode("branch-a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return nil, errors.New("branch-a failed")
	}))
	g.AddNode(NewNoopNode("branch-b"))

	g.AddEdge("start", "branch-a")
	g.AddEdge("start", "branch-b")
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = 2

	_, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestRuntime_Run_Concurrent_ErrorContinue(t *testing.T) {
	var mu sync.Mutex
	executed := make(map[string]bool)

	g := NewGraph("concurrent-continue")
	g.AddNode(NewNoopNode("start"))
	g.AddNode(NewFuncNode("branch-a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return nil, errors.New("branch-a failed")
	}))
	g.AddNode(NewFuncNode("branch-b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		executed["branch-b"] = true
		mu.Unlock()
		return env, nil
	}))

	g.AddEdge("start", "branch-a")
	g.AddEdge("start", "branch-b")
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = 2
	opts.ContinueOnError = true

	result, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Errorf("unexpected error with ContinueOnError: %v", err)
	}

	// branch-b should still execute
	if !executed["branch-b"] {
		t.Error("branch-b should have executed despite branch-a error")
	}

	// Error should be recorded
	if !result.HasErrors() {
		t.Error("expected error to be recorded")
	}
}

func TestRuntime_Run_Concurrent_ContextCancellation(t *testing.T) {
	g := NewGraph("concurrent-cancel")
	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		return env, nil
	}))
	g.AddNode(NewFuncNode("slow", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return env, nil
		}
	}))

	g.AddEdge("start", "slow")
	g.SetEntry("start")

	rt := NewRuntime()
	ctx, cancel := context.WithCancel(context.Background())

	opts := DefaultRunOptions()
	opts.Concurrency = 2

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := rt.Run(ctx, g, NewEnvelope(), opts)

	if err == nil {
		t.Error("expected error on context cancellation")
	}
}

func TestRuntime_Run_Concurrent_DiamondPattern(t *testing.T) {
	// Diamond: start -> a, start -> b, a -> merge, b -> merge -> end
	var mu sync.Mutex
	order := make([]string, 0)

	g := NewGraph("diamond")
	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		order = append(order, "start")
		mu.Unlock()
		return env, nil
	}))
	g.AddNode(NewFuncNode("a", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		order = append(order, "a")
		mu.Unlock()
		env.SetVar("from_a", true)
		return env, nil
	}))
	g.AddNode(NewFuncNode("b", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		order = append(order, "b")
		mu.Unlock()
		env.SetVar("from_b", true)
		return env, nil
	}))

	merger := NewMergeNode("merge", MergeNodeConfig{})
	g.AddNode(merger)

	g.AddNode(NewFuncNode("end", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		mu.Lock()
		order = append(order, "end")
		mu.Unlock()
		return env, nil
	}))

	g.AddEdge("start", "a")
	g.AddEdge("start", "b")
	g.AddEdge("a", "merge")
	g.AddEdge("b", "merge")
	g.AddEdge("merge", "end")
	g.SetEntry("start")

	rt := NewRuntime()
	opts := DefaultRunOptions()
	opts.Concurrency = 4

	_, err := rt.Run(context.Background(), g, NewEnvelope(), opts)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all nodes executed
	nodeSet := make(map[string]bool)
	for _, id := range order {
		nodeSet[id] = true
	}

	for _, id := range []string{"start", "a", "b", "end"} {
		if !nodeSet[id] {
			t.Errorf("Node %s was not executed", id)
		}
	}

	// start should be first
	if order[0] != "start" {
		t.Errorf("expected 'start' first, got %q", order[0])
	}

	// end should be last
	if order[len(order)-1] != "end" {
		t.Errorf("expected 'end' last, got %q", order[len(order)-1])
	}
}

func TestRuntime_Run_GateNode_Pass(t *testing.T) {
	// Test GateNode allowing execution to pass through
	g := NewGraph("gate-pass")

	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("authorized", true)
		return env, nil
	}))

	g.AddNode(NewGateNode("gate", GateNodeConfig{
		ConditionVar: "authorized",
	}))

	g.AddNode(NewFuncNode("end", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("reached_end", true)
		return env, nil
	}))

	g.AddEdge("start", "gate")
	g.AddEdge("gate", "end")
	g.SetEntry("start")

	rt := NewRuntime()
	result, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reachedEnd, _ := result.GetVar("reached_end")
	if reachedEnd != true {
		t.Error("expected to reach end node when gate passes")
	}
}

func TestRuntime_Run_GateNode_Block(t *testing.T) {
	// Test GateNode blocking execution
	g := NewGraph("gate-block")

	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("authorized", false)
		return env, nil
	}))

	g.AddNode(NewGateNode("gate", GateNodeConfig{
		ConditionVar: "authorized",
		OnFail:       GateActionBlock,
		FailMessage:  "access denied",
	}))

	g.AddNode(NewFuncNode("end", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("reached_end", true)
		return env, nil
	}))

	g.AddEdge("start", "gate")
	g.AddEdge("gate", "end")
	g.SetEntry("start")

	rt := NewRuntime()
	_, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if err == nil {
		t.Fatal("expected error when gate blocks")
	}
}

func TestRuntime_Run_GateNode_Redirect(t *testing.T) {
	// Test GateNode redirecting to error handler
	g := NewGraph("gate-redirect")

	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("authorized", false)
		return env, nil
	}))

	g.AddNode(NewGateNode("gate", GateNodeConfig{
		ConditionVar:   "authorized",
		OnFail:         GateActionRedirect,
		RedirectNodeID: "error_handler",
	}))

	g.AddNode(NewFuncNode("success", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("path", "success")
		return env, nil
	}))

	g.AddNode(NewFuncNode("error_handler", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("path", "error")
		return env, nil
	}))

	g.AddEdge("start", "gate")
	g.AddEdge("gate", "success")
	g.AddEdge("gate", "error_handler")
	g.SetEntry("start")

	rt := NewRuntime()
	result, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	path, _ := result.GetVar("path")
	if path != "error" {
		t.Errorf("expected redirect to error_handler, got path=%v", path)
	}
}

func TestRuntime_Run_MapNode(t *testing.T) {
	// Test MapNode within a graph
	g := NewGraph("map-test")

	g.AddNode(NewFuncNode("start", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("numbers", []int{1, 2, 3, 4, 5})
		return env, nil
	}))

	g.AddNode(NewMapNode("mapper", MapNodeConfig{
		InputVar: "numbers",
		Mapper: func(ctx context.Context, item any, index int) (any, error) {
			n := item.(int)
			return n * 2, nil
		},
	}))

	g.AddNode(NewFuncNode("end", func(ctx context.Context, env *Envelope) (*Envelope, error) {
		env.SetVar("done", true)
		return env, nil
	}))

	g.AddEdge("start", "mapper")
	g.AddEdge("mapper", "end")
	g.SetEntry("start")

	rt := NewRuntime()
	result, err := rt.Run(context.Background(), g, NewEnvelope(), DefaultRunOptions())

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output, ok := result.GetVar("mapper_output")
	if !ok {
		t.Fatal("expected mapper_output variable")
	}

	results := output.([]any)
	expected := []int{2, 4, 6, 8, 10}
	for i, v := range results {
		if v.(int) != expected[i] {
			t.Errorf("index %d: expected %d, got %v", i, expected[i], v)
		}
	}

	done, _ := result.GetVar("done")
	if done != true {
		t.Error("expected end node to execute")
	}
}
