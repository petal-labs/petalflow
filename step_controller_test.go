package petalflow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// mockStepNode is a simple node for testing step controller.
type mockStepNode struct {
	BaseNode
	runCount int
	output   string
	err      error
	mu       sync.Mutex
}

func newMockStepNode(id, output string) *mockStepNode {
	return &mockStepNode{
		BaseNode: BaseNode{id: id, kind: NodeKindNoop},
		output:   output,
	}
}

func (n *mockStepNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	n.mu.Lock()
	n.runCount++
	n.mu.Unlock()

	if n.err != nil {
		return nil, n.err
	}

	result := env.Clone()
	result.SetVar(n.id+"_output", n.output)
	return result, nil
}

func (n *mockStepNode) RunCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.runCount
}

func (n *mockStepNode) SetError(err error) {
	n.mu.Lock()
	n.err = err
	n.mu.Unlock()
}

// buildTestGraph creates a simple linear graph for testing.
func buildTestGraph(nodeIDs ...string) Graph {
	builder := NewGraphBuilder("test-graph")
	for i, id := range nodeIDs {
		node := newMockStepNode(id, "output-"+id)
		builder.AddNode(node)
		if i == 0 {
			builder.Entry(id)
		}
		if i > 0 {
			builder.Connect(nodeIDs[i-1], id)
		}
	}
	g, _ := builder.Build()
	return g
}

func TestCallbackStepController_BasicStepThrough(t *testing.T) {
	// Track steps
	var steps []string
	var mu sync.Mutex

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		mu.Lock()
		steps = append(steps, req.NodeID+":"+string(req.Point))
		mu.Unlock()
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	})

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = &StepConfig{
		PauseBeforeNode: true,
		PauseAfterNode:  false,
	}

	result, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify all nodes executed
	if _, ok := result.GetVar("node1_output"); !ok {
		t.Error("node1 did not execute")
	}
	if _, ok := result.GetVar("node2_output"); !ok {
		t.Error("node2 did not execute")
	}
	if _, ok := result.GetVar("node3_output"); !ok {
		t.Error("node3 did not execute")
	}

	// Verify step points
	expected := []string{"node1:before_node", "node2:before_node", "node3:before_node"}
	if len(steps) != len(expected) {
		t.Errorf("Expected %d steps, got %d: %v", len(expected), len(steps), steps)
	}
	for i, s := range expected {
		if i < len(steps) && steps[i] != s {
			t.Errorf("Step %d: expected %s, got %s", i, s, steps[i])
		}
	}
}

func TestCallbackStepController_PauseAfterNode(t *testing.T) {
	var steps []string
	var mu sync.Mutex

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		mu.Lock()
		steps = append(steps, req.NodeID+":"+string(req.Point))
		mu.Unlock()
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	})

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = &StepConfig{
		PauseBeforeNode: true,
		PauseAfterNode:  true,
	}

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Should have both before and after steps
	expected := []string{
		"node1:before_node", "node1:after_node",
		"node2:before_node", "node2:after_node",
	}
	if len(steps) != len(expected) {
		t.Errorf("Expected %d steps, got %d: %v", len(expected), len(steps), steps)
	}
}

func TestCallbackStepController_SkipNode(t *testing.T) {
	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		action := StepActionContinue
		if req.NodeID == "node2" {
			action = StepActionSkipNode
		}
		return &StepResponse{
			RequestID: req.ID,
			Action:    action,
		}, nil
	})

	// Build graph with mock nodes we can track
	node1 := newMockStepNode("node1", "output1")
	node2 := newMockStepNode("node2", "output2")
	node3 := newMockStepNode("node3", "output3")

	builder := NewGraphBuilder("test-graph")
	builder.AddNode(node1).Entry("node1")
	builder.AddNode(node2)
	builder.AddNode(node3)
	builder.Connect("node1", "node2")
	builder.Connect("node2", "node3")
	graph, _ := builder.Build()

	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	result, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// node2 should be skipped
	if node2.RunCount() != 0 {
		t.Errorf("node2 should not have executed, ran %d times", node2.RunCount())
	}

	// node1 and node3 should have executed
	if node1.RunCount() != 1 {
		t.Errorf("node1 should have executed once, ran %d times", node1.RunCount())
	}
	if node3.RunCount() != 1 {
		t.Errorf("node3 should have executed once, ran %d times", node3.RunCount())
	}

	// node2 output should not be in result
	if _, ok := result.GetVar("node2_output"); ok {
		t.Error("node2_output should not exist (node was skipped)")
	}
}

func TestCallbackStepController_AbortExecution(t *testing.T) {
	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		action := StepActionContinue
		if req.NodeID == "node2" {
			action = StepActionAbort
		}
		return &StepResponse{
			RequestID: req.ID,
			Action:    action,
		}, nil
	})

	node1 := newMockStepNode("node1", "output1")
	node2 := newMockStepNode("node2", "output2")
	node3 := newMockStepNode("node3", "output3")

	builder := NewGraphBuilder("test-graph")
	builder.AddNode(node1).Entry("node1")
	builder.AddNode(node2)
	builder.AddNode(node3)
	builder.Connect("node1", "node2")
	builder.Connect("node2", "node3")
	graph, _ := builder.Build()

	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if !errors.Is(err, ErrStepAborted) {
		t.Errorf("Expected ErrStepAborted, got: %v", err)
	}

	// node1 should have executed
	if node1.RunCount() != 1 {
		t.Errorf("node1 should have executed once, ran %d times", node1.RunCount())
	}

	// node2 and node3 should not have executed (aborted before node2)
	if node2.RunCount() != 0 {
		t.Errorf("node2 should not have executed, ran %d times", node2.RunCount())
	}
	if node3.RunCount() != 0 {
		t.Errorf("node3 should not have executed, ran %d times", node3.RunCount())
	}
}

func TestCallbackStepController_ModifyEnvelope(t *testing.T) {
	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		resp := &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}
		// Modify envelope before node2 executes
		if req.NodeID == "node2" && req.Point == StepPointBeforeNode {
			resp.ModifiedEnvelope = &EnvelopeModification{
				SetVars: map[string]any{
					"injected_var": "injected_value",
				},
			}
		}
		return resp, nil
	})

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	result, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Check injected variable exists
	val, ok := result.GetVar("injected_var")
	if !ok {
		t.Error("injected_var should exist")
	}
	if val != "injected_value" {
		t.Errorf("injected_var = %v, want injected_value", val)
	}
}

func TestCallbackStepController_EnvelopeSnapshot(t *testing.T) {
	var capturedSnapshot *EnvelopeSnapshot

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		if req.NodeID == "node2" && req.Point == StepPointBeforeNode {
			capturedSnapshot = req.Envelope
		}
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	})

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()
	env.Input = "test-input"

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if capturedSnapshot == nil {
		t.Fatal("Envelope snapshot was not captured")
	}

	// node1 output should be in the snapshot (it ran before node2)
	if capturedSnapshot.Vars["node1_output"] != "output-node1" {
		t.Errorf("node1_output not in snapshot: %v", capturedSnapshot.Vars)
	}

	if capturedSnapshot.Input != "test-input" {
		t.Errorf("Input = %v, want test-input", capturedSnapshot.Input)
	}
}

func TestCallbackStepController_GraphSnapshot(t *testing.T) {
	var capturedGraph GraphSnapshot

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		if req.NodeID == "node2" {
			capturedGraph = req.Graph
		}
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	})

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if capturedGraph.CurrentNode != "node2" {
		t.Errorf("CurrentNode = %s, want node2", capturedGraph.CurrentNode)
	}

	// node2 has node1 as predecessor
	if len(capturedGraph.Predecessors) != 1 || capturedGraph.Predecessors[0] != "node1" {
		t.Errorf("Predecessors = %v, want [node1]", capturedGraph.Predecessors)
	}

	// node2 has node3 as successor
	if len(capturedGraph.Successors) != 1 || capturedGraph.Successors[0] != "node3" {
		t.Errorf("Successors = %v, want [node3]", capturedGraph.Successors)
	}
}

func TestCallbackStepController_NilController(t *testing.T) {
	// When StepController is nil, execution should proceed normally
	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	// No step controller set

	result, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// All nodes should have executed
	if _, ok := result.GetVar("node1_output"); !ok {
		t.Error("node1 did not execute")
	}
	if _, ok := result.GetVar("node2_output"); !ok {
		t.Error("node2 did not execute")
	}
	if _, ok := result.GetVar("node3_output"); !ok {
		t.Error("node3 did not execute")
	}
}

func TestCallbackStepController_WithShouldPause(t *testing.T) {
	var pausedNodes []string
	var mu sync.Mutex

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		mu.Lock()
		pausedNodes = append(pausedNodes, req.NodeID)
		mu.Unlock()
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	}).WithShouldPause(func(nodeID string, point StepPoint) bool {
		// Only pause at node2
		return nodeID == "node2"
	})

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Only node2 should have paused
	if len(pausedNodes) != 1 || pausedNodes[0] != "node2" {
		t.Errorf("pausedNodes = %v, want [node2]", pausedNodes)
	}
}

func TestChannelStepController_Interactive(t *testing.T) {
	ctrl := NewChannelStepController(10)

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	// Run in goroutine
	var result *Envelope
	var runErr error
	done := make(chan struct{})

	go func() {
		result, runErr = runtime.Run(context.Background(), graph, env, opts)
		close(done)
	}()

	// Respond to step requests
	for i := 0; i < 2; i++ {
		select {
		case req := <-ctrl.Requests():
			err := ctrl.Respond(&StepResponse{
				RequestID: req.ID,
				Action:    StepActionContinue,
			})
			if err != nil {
				t.Fatalf("Respond failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for step request")
		}
	}

	// Wait for completion
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for run completion")
	}

	if runErr != nil {
		t.Fatalf("Run failed: %v", runErr)
	}

	if _, ok := result.GetVar("node2_output"); !ok {
		t.Error("node2 did not execute")
	}
}

func TestChannelStepController_Breakpoints(t *testing.T) {
	ctrl := NewChannelStepController(10)
	ctrl.SetBreakpoint("node2")

	var pausedNodes []string
	var mu sync.Mutex

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	done := make(chan struct{})
	go func() {
		_, _ = runtime.Run(context.Background(), graph, env, opts)
		close(done)
	}()

	// Should only get request for node2
	select {
	case req := <-ctrl.Requests():
		mu.Lock()
		pausedNodes = append(pausedNodes, req.NodeID)
		mu.Unlock()
		ctrl.Respond(&StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		})
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for step request")
	}

	<-done

	if len(pausedNodes) != 1 || pausedNodes[0] != "node2" {
		t.Errorf("pausedNodes = %v, want [node2]", pausedNodes)
	}
}

func TestChannelStepController_RunToBreakpoint(t *testing.T) {
	ctrl := NewChannelStepController(10)
	ctrl.SetBreakpoint("node3")

	var pausedNodes []string
	var mu sync.Mutex

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	done := make(chan struct{})
	go func() {
		_, _ = runtime.Run(context.Background(), graph, env, opts)
		close(done)
	}()

	// First request is node1 (no breakpoints set initially, so all nodes pause)
	// Wait, actually we set a breakpoint on node3, so only node3 should pause
	select {
	case req := <-ctrl.Requests():
		mu.Lock()
		pausedNodes = append(pausedNodes, req.NodeID)
		mu.Unlock()
		ctrl.Respond(&StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		})
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for step request")
	}

	<-done

	// Only node3 should have paused (breakpoint was set)
	if len(pausedNodes) != 1 || pausedNodes[0] != "node3" {
		t.Errorf("pausedNodes = %v, want [node3]", pausedNodes)
	}
}

func TestBreakpointStepController_OnlyAtBreakpoints(t *testing.T) {
	var pausedNodes []string
	var mu sync.Mutex

	ctrl := NewBreakpointStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		mu.Lock()
		pausedNodes = append(pausedNodes, req.NodeID+":"+string(req.Point))
		mu.Unlock()
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	})

	ctrl.AddBreakpointBefore("node2")
	ctrl.AddBreakpointAfter("node3")

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = &StepConfig{
		PauseBeforeNode: true,
		PauseAfterNode:  true,
	}

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expected := []string{"node2:before_node", "node3:after_node"}
	if len(pausedNodes) != len(expected) {
		t.Errorf("pausedNodes = %v, want %v", pausedNodes, expected)
	}
	for i, e := range expected {
		if i < len(pausedNodes) && pausedNodes[i] != e {
			t.Errorf("pausedNodes[%d] = %s, want %s", i, pausedNodes[i], e)
		}
	}
}

func TestAutoStepController_Delay(t *testing.T) {
	var stepTimes []time.Time
	var mu sync.Mutex

	delay := 50 * time.Millisecond
	ctrl := NewAutoStepController(delay).WithLogHandler(func(req *StepRequest) {
		mu.Lock()
		stepTimes = append(stepTimes, time.Now())
		mu.Unlock()
	})

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	start := time.Now()
	_, err := runtime.Run(context.Background(), graph, env, opts)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Should have taken at least 3 * delay (3 nodes)
	minExpected := 3 * delay
	if elapsed < minExpected {
		t.Errorf("Execution took %v, expected at least %v", elapsed, minExpected)
	}
}

func TestAutoStepController_PauseResume(t *testing.T) {
	ctrl := NewAutoStepController(0) // No delay

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	// Pause the controller
	ctrl.Pause()

	done := make(chan struct{})
	go func() {
		_, _ = runtime.Run(context.Background(), graph, env, opts)
		close(done)
	}()

	// Give it a moment to reach the pause
	time.Sleep(50 * time.Millisecond)

	// Execution should be paused, done should not be closed yet
	select {
	case <-done:
		t.Fatal("Run completed while paused")
	default:
		// Expected - still paused
	}

	// Resume execution
	ctrl.Resume()

	// Now it should complete
	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for run completion after resume")
	}
}

func TestStepController_StepEvents(t *testing.T) {
	var events []Event
	var mu sync.Mutex

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	})

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()
	opts.EventHandler = func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Count step events
	stepPaused := 0
	stepResumed := 0
	for _, e := range events {
		switch e.Kind {
		case EventStepPaused:
			stepPaused++
		case EventStepResumed:
			stepResumed++
		}
	}

	// Should have 2 paused and 2 resumed (one for each node)
	if stepPaused != 2 {
		t.Errorf("stepPaused = %d, want 2", stepPaused)
	}
	if stepResumed != 2 {
		t.Errorf("stepResumed = %d, want 2", stepResumed)
	}
}

func TestStepController_StepSkippedEvent(t *testing.T) {
	var events []Event
	var mu sync.Mutex

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		action := StepActionContinue
		if req.NodeID == "node2" {
			action = StepActionSkipNode
		}
		return &StepResponse{
			RequestID: req.ID,
			Action:    action,
		}, nil
	})

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()
	opts.EventHandler = func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Find step_skipped event
	var skipEvent *Event
	for i := range events {
		if events[i].Kind == EventStepSkipped {
			skipEvent = &events[i]
			break
		}
	}

	if skipEvent == nil {
		t.Fatal("Expected step_skipped event")
	}

	if skipEvent.NodeID != "node2" {
		t.Errorf("step_skipped NodeID = %s, want node2", skipEvent.NodeID)
	}
}

func TestStepController_StepAbortedEvent(t *testing.T) {
	var events []Event
	var mu sync.Mutex

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		action := StepActionContinue
		if req.NodeID == "node2" {
			action = StepActionAbort
		}
		return &StepResponse{
			RequestID: req.ID,
			Action:    action,
		}, nil
	})

	graph := buildTestGraph("node1", "node2", "node3")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()
	opts.EventHandler = func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	_, _ = runtime.Run(context.Background(), graph, env, opts)

	// Find step_aborted event
	var abortEvent *Event
	for i := range events {
		if events[i].Kind == EventStepAborted {
			abortEvent = &events[i]
			break
		}
	}

	if abortEvent == nil {
		t.Fatal("Expected step_aborted event")
	}

	if abortEvent.NodeID != "node2" {
		t.Errorf("step_aborted NodeID = %s, want node2", abortEvent.NodeID)
	}
}

func TestStepController_ContextCancellation(t *testing.T) {
	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		// Block until context is cancelled
		<-ctx.Done()
		return nil, ctx.Err()
	})

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	_, err := runtime.Run(ctx, graph, env, opts)
	if err == nil {
		t.Fatal("Expected error due to context cancellation")
	}
}

func TestStepController_ErrorInAfterNode(t *testing.T) {
	var capturedError error

	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		if req.Point == StepPointAfterNode && req.NodeID == "node1" {
			capturedError = req.Error
		}
		return &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}, nil
	})

	// Create graph with a failing node
	node1 := newMockStepNode("node1", "output1")
	node1.SetError(errors.New("node1 failed"))
	node2 := newMockStepNode("node2", "output2")

	builder := NewGraphBuilder("test-graph")
	builder.AddNode(node1).Entry("node1")
	builder.AddNode(node2)
	builder.Connect("node1", "node2")
	graph, _ := builder.Build()

	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = &StepConfig{
		PauseBeforeNode: true,
		PauseAfterNode:  true,
	}
	opts.ContinueOnError = true

	_, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// The error should have been captured in the after-node step
	if capturedError == nil {
		t.Error("Expected to capture error in after-node step")
	} else if capturedError.Error() != "node1 failed" {
		t.Errorf("Captured error = %v, want 'node1 failed'", capturedError)
	}
}

func TestBreakpointStepController_ListBreakpoints(t *testing.T) {
	ctrl := NewBreakpointStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		return &StepResponse{RequestID: req.ID, Action: StepActionContinue}, nil
	})

	ctrl.AddBreakpointBefore("node1")
	ctrl.AddBreakpointAfter("node2")
	ctrl.AddBreakpoint("node3", StepPointBeforeNode)

	bps := ctrl.ListBreakpoints()
	if len(bps) != 3 {
		t.Errorf("Expected 3 breakpoints, got %d", len(bps))
	}

	if bps["node1"] != StepPointBeforeNode {
		t.Errorf("node1 breakpoint = %s, want before_node", bps["node1"])
	}
	if bps["node2"] != StepPointAfterNode {
		t.Errorf("node2 breakpoint = %s, want after_node", bps["node2"])
	}

	// Remove one
	ctrl.RemoveBreakpoint("node2")
	bps = ctrl.ListBreakpoints()
	if len(bps) != 2 {
		t.Errorf("Expected 2 breakpoints after removal, got %d", len(bps))
	}

	// Clear all
	ctrl.ClearAllBreakpoints()
	bps = ctrl.ListBreakpoints()
	if len(bps) != 0 {
		t.Errorf("Expected 0 breakpoints after clear, got %d", len(bps))
	}
}

func TestChannelStepController_ListPending(t *testing.T) {
	ctrl := NewChannelStepController(10)

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	done := make(chan struct{})
	go func() {
		_, _ = runtime.Run(context.Background(), graph, env, opts)
		close(done)
	}()

	// Wait for first request
	select {
	case <-ctrl.Requests():
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for step request")
	}

	// Check pending
	pending := ctrl.ListPending()
	if len(pending) != 1 {
		t.Errorf("Expected 1 pending request, got %d", len(pending))
	}
	if len(pending) > 0 && pending[0].NodeID != "node1" {
		t.Errorf("Pending request NodeID = %s, want node1", pending[0].NodeID)
	}

	// Respond and clean up
	ctrl.Respond(&StepResponse{RequestID: pending[0].ID, Action: StepActionAbort})
	<-done
}

func TestEnvelopeModification_DeleteVars(t *testing.T) {
	ctrl := NewCallbackStepController(func(ctx context.Context, req *StepRequest) (*StepResponse, error) {
		resp := &StepResponse{
			RequestID: req.ID,
			Action:    StepActionContinue,
		}
		// Before node2, delete a variable
		if req.NodeID == "node2" && req.Point == StepPointBeforeNode {
			resp.ModifiedEnvelope = &EnvelopeModification{
				DeleteVars: []string{"node1_output"},
			}
		}
		return resp, nil
	})

	graph := buildTestGraph("node1", "node2")
	runtime := NewRuntime()
	env := NewEnvelope()

	opts := DefaultRunOptions()
	opts.StepController = ctrl
	opts.StepConfig = DefaultStepConfig()

	result, err := runtime.Run(context.Background(), graph, env, opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// node1_output should have been deleted
	if _, ok := result.GetVar("node1_output"); ok {
		t.Error("node1_output should have been deleted")
	}

	// node2_output should exist
	if _, ok := result.GetVar("node2_output"); !ok {
		t.Error("node2_output should exist")
	}
}
