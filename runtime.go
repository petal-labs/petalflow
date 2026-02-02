package petalflow

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Runtime errors
var (
	ErrMaxHopsExceeded = errors.New("maximum hops exceeded")
	ErrRunCanceled     = errors.New("run was canceled")
	ErrNodeExecution   = errors.New("node execution failed")
)

// Runtime executes graphs and emits events.
type Runtime interface {
	// Run executes the graph with the given initial envelope.
	Run(ctx context.Context, g Graph, env *Envelope, opts RunOptions) (*Envelope, error)

	// Events returns a channel for receiving runtime events.
	// The channel is closed when the run completes.
	Events() <-chan Event
}

// RunOptions controls execution behavior.
type RunOptions struct {
	// MaxHops protects against infinite cycles (default: 100).
	MaxHops int

	// ContinueOnError records errors and continues when possible.
	ContinueOnError bool

	// Concurrency sets the worker pool size for parallel execution (default: 1).
	Concurrency int

	// Now provides the current time (for testing). If nil, uses time.Now.
	Now func() time.Time

	// EventHandler receives events during execution.
	EventHandler EventHandler

	// StepController enables step-through debugging.
	// If nil, execution proceeds without pausing.
	StepController StepController

	// StepConfig provides additional step-through configuration.
	// If nil but StepController is set, DefaultStepConfig() is used.
	StepConfig *StepConfig
}

// DefaultRunOptions returns sensible default options.
func DefaultRunOptions() RunOptions {
	return RunOptions{
		MaxHops:         100,
		ContinueOnError: false,
		Concurrency:     1,
	}
}

// BasicRuntime is a simple sequential runtime implementation.
type BasicRuntime struct {
	eventCh chan Event
}

// NewRuntime creates a new runtime instance.
func NewRuntime() *BasicRuntime {
	return &BasicRuntime{
		eventCh: make(chan Event, 100), // buffered channel
	}
}

// Events returns the event channel.
func (r *BasicRuntime) Events() <-chan Event {
	return r.eventCh
}

// Run executes the graph sequentially, following edges from the entry node.
func (r *BasicRuntime) Run(ctx context.Context, g Graph, env *Envelope, opts RunOptions) (*Envelope, error) {
	// Apply defaults
	if opts.MaxHops <= 0 {
		opts.MaxHops = 100
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	// Validate graph
	if err := validateGraph(g); err != nil {
		return nil, err
	}

	// Initialize envelope if nil
	if env == nil {
		env = NewEnvelope()
	}

	// Generate run ID
	runID := generateRunID()
	env.Trace.RunID = runID
	env.Trace.Started = opts.Now()

	// Create event emitter
	emit := func(e Event) {
		if opts.EventHandler != nil {
			opts.EventHandler(e)
		}
		select {
		case r.eventCh <- e:
		default:
			// Drop if channel is full
		}
	}

	// Emit run started
	runStart := opts.Now()
	emit(NewEvent(EventRunStarted, runID).
		WithPayload("graph", g.Name()).
		WithPayload("entry", g.Entry()))

	// Execute graph
	result, err := r.executeGraph(ctx, g, env, opts, emit, runStart)

	// Emit run finished
	runElapsed := opts.Now().Sub(runStart)
	finishEvent := NewEvent(EventRunFinished, runID).
		WithElapsed(runElapsed)

	if err != nil {
		finishEvent = finishEvent.
			WithPayload("status", "failed").
			WithPayload("error", err.Error())
	} else {
		finishEvent = finishEvent.
			WithPayload("status", "completed")
	}
	emit(finishEvent)

	return result, err
}

// executeGraph performs the actual graph execution.
// It uses dynamic successor selection to support RouterNode decisions.
// When Concurrency > 1, parallel branches are executed concurrently.
func (r *BasicRuntime) executeGraph(
	ctx context.Context,
	g Graph,
	env *Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*Envelope, error) {
	// For concurrent execution, use the parallel executor
	if opts.Concurrency > 1 {
		return r.executeGraphParallel(ctx, g, env, opts, emit, runStart)
	}

	// Sequential execution (original behavior)
	return r.executeGraphSequential(ctx, g, env, opts, emit, runStart)
}

// executeGraphSequential is the original sequential execution logic.
func (r *BasicRuntime) executeGraphSequential(
	ctx context.Context,
	g Graph,
	env *Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*Envelope, error) {
	hopCount := make(map[string]int)
	current := env

	// Use a queue for dynamic execution order
	// Start with the entry node
	queue := []string{g.Entry()}
	visited := make(map[string]bool)

	for len(queue) > 0 {
		// Pop next node from queue
		nodeID := queue[0]
		queue = queue[1:]

		// Skip if already visited in this execution path
		// (this prevents infinite loops in non-router cases)
		if visited[nodeID] && hopCount[nodeID] > 0 {
			// For cycles, check max hops
			if hopCount[nodeID] >= opts.MaxHops {
				continue
			}
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return current, fmt.Errorf("%w: %v", ErrRunCanceled, ctx.Err())
		default:
		}

		// Check max hops
		hopCount[nodeID]++
		if hopCount[nodeID] > opts.MaxHops {
			return current, fmt.Errorf("%w: node %s executed %d times", ErrMaxHopsExceeded, nodeID, hopCount[nodeID])
		}

		// Get node
		node, ok := g.NodeByID(nodeID)
		if !ok {
			return current, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
		}

		// Step before node execution (if step controller is configured)
		if opts.StepController != nil {
			action, modifiedEnv, stepErr := r.handleStepPoint(
				ctx, g, node, current, opts, emit, runStart,
				StepPointBeforeNode, hopCount[nodeID], nil,
			)
			if stepErr != nil {
				return current, stepErr
			}
			if modifiedEnv != nil {
				current = modifiedEnv
			}
			switch action {
			case StepActionAbort:
				emit(NewEvent(EventStepAborted, current.Trace.RunID).
					WithNode(nodeID, node.Kind()).
					WithElapsed(opts.Now().Sub(runStart)).
					WithPayload("reason", "controller_abort"))
				return current, ErrStepAborted
			case StepActionSkipNode:
				emit(NewEvent(EventStepSkipped, current.Trace.RunID).
					WithNode(nodeID, node.Kind()).
					WithElapsed(opts.Now().Sub(runStart)).
					WithPayload("reason", "controller_skip"))
				visited[nodeID] = true
				// Determine successors even when skipping
				nextNodes := r.determineSuccessors(g, node, current, emit, runStart, opts)
				queue = append(queue, nextNodes...)
				continue
			}
		}

		// Execute node
		result, err := r.executeNode(ctx, node, current, opts, emit, runStart)

		// Step after node execution (if step controller is configured)
		if opts.StepController != nil {
			// Use result if available, otherwise use current (for failed nodes)
			envForStep := result
			if envForStep == nil {
				envForStep = current
			}
			action, _, stepErr := r.handleStepPoint(
				ctx, g, node, envForStep, opts, emit, runStart,
				StepPointAfterNode, hopCount[nodeID], err,
			)
			if stepErr != nil {
				return current, stepErr
			}
			if action == StepActionAbort {
				emit(NewEvent(EventStepAborted, current.Trace.RunID).
					WithNode(nodeID, node.Kind()).
					WithElapsed(opts.Now().Sub(runStart)).
					WithPayload("reason", "controller_abort"))
				return current, ErrStepAborted
			}
		}

		if err != nil {
			if opts.ContinueOnError {
				current.AppendError(NodeError{
					NodeID:  nodeID,
					Kind:    node.Kind(),
					Message: err.Error(),
					Attempt: hopCount[nodeID],
					At:      opts.Now(),
					Cause:   err,
				})
			} else {
				return current, fmt.Errorf("%w: node %s: %v", ErrNodeExecution, nodeID, err)
			}
		} else {
			current = result
		}

		// Mark as visited
		visited[nodeID] = true

		// Determine next nodes to execute
		nextNodes := r.determineSuccessors(g, node, current, emit, runStart, opts)
		queue = append(queue, nextNodes...)
	}

	return current, nil
}

// nodeResult holds the result of executing a node.
type nodeResult struct {
	nodeID   string
	envelope *Envelope
	err      error
}

// executeGraphParallel executes the graph with concurrent branches.
func (r *BasicRuntime) executeGraphParallel(
	ctx context.Context,
	g Graph,
	env *Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*Envelope, error) {
	// Track node states
	type nodeState struct {
		hopCount  int
		completed bool
		envelope  *Envelope // result envelope for this node
	}

	states := make(map[string]*nodeState)
	var statesMu sync.Mutex

	// Track errors across all branches
	var recordedErrors []NodeError
	var errorsMu sync.Mutex

	// Track pending inputs for merge nodes
	// mergeInputs[mergeNodeID] = list of envelopes from predecessors
	mergeInputs := make(map[string][]*Envelope)
	var mergeMu sync.Mutex

	// Worker pool
	type workItem struct {
		nodeID   string
		envelope *Envelope
	}
	workCh := make(chan workItem, opts.Concurrency*2)
	resultCh := make(chan nodeResult, opts.Concurrency*2)

	// Context with cancellation for worker shutdown
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-workerCtx.Done():
					return
				case work, ok := <-workCh:
					if !ok {
						return
					}
					node, exists := g.NodeByID(work.nodeID)
					if !exists {
						resultCh <- nodeResult{
							nodeID: work.nodeID,
							err:    fmt.Errorf("%w: %s", ErrNodeNotFound, work.nodeID),
						}
						continue
					}

					result, err := r.executeNode(workerCtx, node, work.envelope, opts, emit, runStart)
					resultCh <- nodeResult{
						nodeID:   work.nodeID,
						envelope: result,
						err:      err,
					}
				}
			}
		}()
	}

	// Initialize state for entry node
	entryID := g.Entry()
	states[entryID] = &nodeState{hopCount: 0, completed: false, envelope: env}

	// Submit entry node
	pendingCount := 1
	workCh <- workItem{nodeID: entryID, envelope: env}

	// Track the final result
	var finalEnvelope *Envelope
	var finalErr error

	// Process results
	for pendingCount > 0 {
		select {
		case <-ctx.Done():
			cancelWorkers()
			close(workCh)
			wg.Wait()
			return env, fmt.Errorf("%w: %v", ErrRunCanceled, ctx.Err())

		case result := <-resultCh:
			pendingCount--

			statesMu.Lock()
			state := states[result.nodeID]
			if state == nil {
				state = &nodeState{}
				states[result.nodeID] = state
			}
			state.hopCount++
			statesMu.Unlock()

			// Handle error
			if result.err != nil {
				if opts.ContinueOnError {
					node, _ := g.NodeByID(result.nodeID)
					nodeErr := NodeError{
						NodeID:  result.nodeID,
						Kind:    node.Kind(),
						Message: result.err.Error(),
						Attempt: state.hopCount,
						At:      opts.Now(),
						Cause:   result.err,
					}
					errorsMu.Lock()
					recordedErrors = append(recordedErrors, nodeErr)
					errorsMu.Unlock()
					// Continue with original envelope
					result.envelope = state.envelope
				} else {
					// Fatal error - stop execution
					cancelWorkers()
					close(workCh)
					wg.Wait()
					return env, fmt.Errorf("%w: node %s: %v", ErrNodeExecution, result.nodeID, result.err)
				}
			}

			// Update state
			statesMu.Lock()
			state.completed = true
			state.envelope = result.envelope
			statesMu.Unlock()

			// Update final envelope (last completed node wins)
			finalEnvelope = result.envelope

			// Get the node to determine successors
			node, _ := g.NodeByID(result.nodeID)
			successors := r.determineSuccessors(g, node, result.envelope, emit, runStart, opts)

			// Process each successor
			for _, succID := range successors {
				succNode, exists := g.NodeByID(succID)
				if !exists {
					continue
				}

				// Check if this is a merge node
				if mergeNode, ok := succNode.(*MergeNode); ok {
					mergeMu.Lock()
					mergeInputs[succID] = append(mergeInputs[succID], result.envelope)

					// Determine expected inputs
					expectedInputs := mergeNode.ExpectedInputs()
					if expectedInputs == 0 {
						expectedInputs = len(g.Predecessors(succID))
					}

					// Check if all inputs have arrived
					if len(mergeInputs[succID]) >= expectedInputs {
						// All inputs ready, merge them
						inputs := mergeInputs[succID]
						mergeMu.Unlock()

						mergedEnv, mergeErr := mergeNode.MergeInputs(ctx, inputs)
						if mergeErr != nil {
							if opts.ContinueOnError {
								nodeErr := NodeError{
									NodeID:  succID,
									Kind:    mergeNode.Kind(),
									Message: mergeErr.Error(),
									At:      opts.Now(),
									Cause:   mergeErr,
								}
								errorsMu.Lock()
								recordedErrors = append(recordedErrors, nodeErr)
								errorsMu.Unlock()
								mergedEnv = inputs[0] // fallback to first input
							} else {
								finalErr = fmt.Errorf("merge node %s failed: %w", succID, mergeErr)
								cancelWorkers()
								close(workCh)
								wg.Wait()
								return env, finalErr
							}
						}

						// Submit merged result for the merge node to process
						statesMu.Lock()
						states[succID] = &nodeState{hopCount: 0, envelope: mergedEnv}
						statesMu.Unlock()

						pendingCount++
						workCh <- workItem{nodeID: succID, envelope: mergedEnv}
					} else {
						mergeMu.Unlock()
						// Still waiting for more inputs
					}
				} else {
					// Not a merge node - submit with cloned envelope for parallel branches
					statesMu.Lock()
					succState := states[succID]
					if succState == nil {
						succState = &nodeState{}
						states[succID] = succState
					}

					// Check max hops
					if succState.hopCount >= opts.MaxHops {
						statesMu.Unlock()
						continue
					}
					statesMu.Unlock()

					// Clone envelope for parallel branches
					branchEnv := result.envelope.Clone()

					pendingCount++
					workCh <- workItem{nodeID: succID, envelope: branchEnv}
				}
			}
		}
	}

	// Cleanup
	close(workCh)
	wg.Wait()

	if finalEnvelope == nil {
		finalEnvelope = env
	}

	// Merge recorded errors into final envelope
	errorsMu.Lock()
	for _, err := range recordedErrors {
		finalEnvelope.AppendError(err)
	}
	errorsMu.Unlock()

	return finalEnvelope, finalErr
}

// determineSuccessors decides which nodes to execute next after the current node.
// For RouterNodes, it uses the RouteDecision; for others, it uses all graph successors.
func (r *BasicRuntime) determineSuccessors(
	g Graph,
	node Node,
	env *Envelope,
	emit EventEmitter,
	runStart time.Time,
	opts RunOptions,
) []string {
	nodeID := node.ID()
	graphSuccessors := g.Successors(nodeID)

	// If no successors in graph, nothing to execute next
	if len(graphSuccessors) == 0 {
		return nil
	}

	// Check for GateNode redirect
	// GateNodes with OnFail=GateActionRedirect set __gate_redirect__ in the envelope
	if redirectVal, ok := env.GetVar("__gate_redirect__"); ok {
		redirectNode, ok := redirectVal.(string)
		if ok && redirectNode != "" {
			// Verify the redirect target is a valid successor
			for _, succ := range graphSuccessors {
				if succ == redirectNode {
					// Emit gate redirect event
					emit(NewEvent(EventRouteDecision, env.Trace.RunID).
						WithNode(nodeID, node.Kind()).
						WithElapsed(opts.Now().Sub(runStart)).
						WithPayload("targets", []string{redirectNode}).
						WithPayload("reason", "gate redirect").
						WithPayload("confidence", 1.0))

					// Clear the redirect hint to prevent re-triggering
					env.Vars["__gate_redirect__"] = nil

					return []string{redirectNode}
				}
			}
		}
	}

	// Check if this is a RouterNode
	router, isRouter := node.(RouterNode)
	if !isRouter {
		// Not a router, use all graph successors
		return graphSuccessors
	}

	// Get the routing decision from the envelope
	// RouterNodes store their decision in the envelope during Run()
	decisionKey := nodeID + "_decision"
	decisionVal, ok := env.GetVar(decisionKey)
	if !ok {
		// No decision stored, fall back to all successors
		return graphSuccessors
	}

	decision, ok := decisionVal.(RouteDecision)
	if !ok {
		// Invalid decision type, fall back to all successors
		return graphSuccessors
	}

	// Emit routing decision event
	emit(NewEvent(EventRouteDecision, env.Trace.RunID).
		WithNode(nodeID, router.Kind()).
		WithElapsed(opts.Now().Sub(runStart)).
		WithPayload("targets", decision.Targets).
		WithPayload("reason", decision.Reason).
		WithPayload("confidence", decision.Confidence))

	// Filter successors to only those in the decision targets
	// The decision targets are node IDs that should be executed
	var filteredSuccessors []string
	targetSet := make(map[string]bool)
	for _, t := range decision.Targets {
		targetSet[t] = true
	}

	for _, succ := range graphSuccessors {
		if targetSet[succ] {
			filteredSuccessors = append(filteredSuccessors, succ)
		}
	}

	return filteredSuccessors
}

// executeNode executes a single node with event emission.
func (r *BasicRuntime) executeNode(
	ctx context.Context,
	node Node,
	env *Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*Envelope, error) {
	nodeID := node.ID()
	nodeKind := node.Kind()
	runID := env.Trace.RunID

	// Emit node started
	nodeStart := opts.Now()
	emit(NewEvent(EventNodeStarted, runID).
		WithNode(nodeID, nodeKind).
		WithElapsed(nodeStart.Sub(runStart)))

	// Execute node
	result, err := node.Run(ctx, env)

	// Calculate elapsed time
	nodeElapsed := opts.Now().Sub(nodeStart)

	if err != nil {
		// Emit node failed
		emit(NewEvent(EventNodeFailed, runID).
			WithNode(nodeID, nodeKind).
			WithElapsed(nodeElapsed).
			WithPayload("error", err.Error()))
		return nil, err
	}

	// Emit node finished
	emit(NewEvent(EventNodeFinished, runID).
		WithNode(nodeID, nodeKind).
		WithElapsed(nodeElapsed))

	return result, nil
}

// handleStepPoint handles step controller interaction at a step point.
// It returns the action to take, optionally modified envelope, and any error.
func (r *BasicRuntime) handleStepPoint(
	ctx context.Context,
	g Graph,
	node Node,
	env *Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
	point StepPoint,
	hopCount int,
	nodeErr error,
) (StepAction, *Envelope, error) {
	ctrl := opts.StepController
	if ctrl == nil {
		return StepActionContinue, nil, nil
	}

	config := opts.StepConfig
	if config == nil {
		config = DefaultStepConfig()
	}

	// Check if we should pause at this point based on config
	if point == StepPointBeforeNode && !config.PauseBeforeNode {
		return StepActionContinue, nil, nil
	}
	if point == StepPointAfterNode && !config.PauseAfterNode {
		return StepActionContinue, nil, nil
	}

	// Check controller's ShouldPause
	if !ctrl.ShouldPause(node.ID(), point) {
		return StepActionContinue, nil, nil
	}

	// Build step request
	req := &StepRequest{
		ID:        generateStepID(),
		RunID:     env.Trace.RunID,
		Point:     point,
		NodeID:    node.ID(),
		NodeKind:  node.Kind(),
		Envelope:  createEnvelopeSnapshot(env),
		HopCount:  hopCount,
		Error:     nodeErr,
		Graph:     createGraphSnapshot(g, node.ID()),
		CreatedAt: opts.Now(),
	}

	// Emit step paused event
	emit(NewEvent(EventStepPaused, env.Trace.RunID).
		WithNode(node.ID(), node.Kind()).
		WithElapsed(opts.Now().Sub(runStart)).
		WithPayload("step_id", req.ID).
		WithPayload("step_point", string(point)).
		WithPayload("hop_count", hopCount))

	// Apply timeout if configured
	stepCtx := ctx
	if config.StepTimeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, config.StepTimeout)
		defer cancel()
	}

	// Call controller
	resp, err := ctrl.Step(stepCtx, req)
	if err != nil {
		return StepActionAbort, nil, fmt.Errorf("step controller error: %w", err)
	}

	// Emit step resumed event
	emit(NewEvent(EventStepResumed, env.Trace.RunID).
		WithNode(node.ID(), node.Kind()).
		WithElapsed(opts.Now().Sub(runStart)).
		WithPayload("step_id", req.ID).
		WithPayload("action", string(resp.Action)))

	// Apply envelope modifications if provided (only for before-node steps)
	var modifiedEnv *Envelope
	if resp.ModifiedEnvelope != nil && point == StepPointBeforeNode && resp.Action == StepActionContinue {
		modifiedEnv = env.Clone()
		for k, v := range resp.ModifiedEnvelope.SetVars {
			modifiedEnv.SetVar(k, v)
		}
		for _, k := range resp.ModifiedEnvelope.DeleteVars {
			delete(modifiedEnv.Vars, k)
		}
	}

	return resp.Action, modifiedEnv, nil
}

// validateGraph performs basic validation.
func validateGraph(g Graph) error {
	if g == nil {
		return errors.New("graph is nil")
	}

	nodes := g.Nodes()
	if len(nodes) == 0 {
		return ErrEmptyGraph
	}

	entry := g.Entry()
	if entry == "" {
		return ErrNoEntryNode
	}

	if _, ok := g.NodeByID(entry); !ok {
		return fmt.Errorf("%w: entry node %q", ErrNodeNotFound, entry)
	}

	return nil
}

// generateRunID creates a unique run identifier.
// Uses crypto/rand for secure random generation.
func generateRunID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Ensure interface compliance at compile time.
var _ Runtime = (*BasicRuntime)(nil)
