// Package runtime provides the execution engine for PetalFlow workflow graphs.
package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/graph"
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
	Run(ctx context.Context, g graph.Graph, env *core.Envelope, opts RunOptions) (*core.Envelope, error)

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

	// EventEmitterDecorator wraps the internal event emitter.
	// If nil, events are emitted without decoration.
	EventEmitterDecorator EventEmitterDecorator

	// StepController enables step-through debugging.
	// If nil, execution proceeds without pausing.
	StepController StepController

	// StepConfig provides additional step-through configuration.
	// If nil but StepController is set, DefaultStepConfig() is used.
	StepConfig *StepConfig

	// EventBus distributes events to subscribers.
	// If nil, events are only sent to EventHandler and eventCh.
	EventBus EventPublisher
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
func (r *BasicRuntime) Run(ctx context.Context, g graph.Graph, env *core.Envelope, opts RunOptions) (*core.Envelope, error) {
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
		env = core.NewEnvelope()
	}

	// Generate run ID
	runID := generateRunID()
	env.Trace.RunID = runID
	env.Trace.Started = opts.Now()

	// Create event emitter
	seq := newSeqGen()
	emit := func(e Event) {
		e.Seq = seq.Next()
		if opts.EventBus != nil {
			opts.EventBus.Publish(e)
		}
		if opts.EventHandler != nil {
			opts.EventHandler(e)
		}
		select {
		case r.eventCh <- e:
		default:
			// Drop if channel is full
		}
	}
	if opts.EventEmitterDecorator != nil {
		emit = opts.EventEmitterDecorator(emit)
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
	g graph.Graph,
	env *core.Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*core.Envelope, error) {
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
	g graph.Graph,
	env *core.Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*core.Envelope, error) {
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

		if shouldSkipVisitedNode(nodeID, visited, hopCount, opts.MaxHops) {
			continue
		}

		if err := checkRunContext(ctx); err != nil {
			return current, err
		}

		attempt, err := incrementAndValidateHop(nodeID, hopCount, opts.MaxHops)
		if err != nil {
			return current, err
		}

		node, ok := g.NodeByID(nodeID)
		if !ok {
			return current, fmt.Errorf("%w: %s", graph.ErrNodeNotFound, nodeID)
		}

		var skipNode bool
		current, skipNode, err = r.handleSequentialBeforeStep(
			ctx, g, node, current, opts, emit, runStart, attempt,
		)
		if err != nil {
			return current, err
		}
		if skipNode {
			visited[nodeID] = true
			queue = append(queue, r.determineSuccessors(g, node, current, emit, runStart, opts)...)
			continue
		}

		// Execute node
		result, nodeErr := r.executeNode(ctx, node, current, opts, emit, runStart)

		err = r.handleSequentialAfterStep(
			ctx, g, node, current, result, nodeErr, opts, emit, runStart, attempt,
		)
		if err != nil {
			return current, err
		}

		current, err = resolveSequentialNodeOutcome(
			current, result, node, nodeErr, attempt, opts, nodeID,
		)
		if err != nil {
			return current, err
		}

		// Mark as visited
		visited[nodeID] = true

		// Determine next nodes to execute
		nextNodes := r.determineSuccessors(g, node, current, emit, runStart, opts)
		queue = append(queue, nextNodes...)
	}

	return current, nil
}

func shouldSkipVisitedNode(nodeID string, visited map[string]bool, hopCount map[string]int, maxHops int) bool {
	if !visited[nodeID] || hopCount[nodeID] == 0 {
		return false
	}
	return hopCount[nodeID] >= maxHops
}

func checkRunContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrRunCanceled, ctx.Err())
	default:
		return nil
	}
}

func incrementAndValidateHop(nodeID string, hopCount map[string]int, maxHops int) (int, error) {
	hopCount[nodeID]++
	attempt := hopCount[nodeID]
	if attempt > maxHops {
		return attempt, fmt.Errorf("%w: node %s executed %d times", ErrMaxHopsExceeded, nodeID, attempt)
	}
	return attempt, nil
}

func (r *BasicRuntime) handleSequentialBeforeStep(
	ctx context.Context,
	g graph.Graph,
	node core.Node,
	current *core.Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
	attempt int,
) (*core.Envelope, bool, error) {
	if opts.StepController == nil {
		return current, false, nil
	}

	action, modifiedEnv, stepErr := r.handleStepPoint(
		ctx, g, node, current, opts, emit, runStart,
		StepPointBeforeNode, attempt, nil,
	)
	if stepErr != nil {
		return current, false, stepErr
	}
	if modifiedEnv != nil {
		current = modifiedEnv
	}

	switch action {
	case StepActionAbort:
		emitStepControlEvent(
			emit, opts, runStart, current.Trace.RunID, node,
			EventStepAborted, "controller_abort",
		)
		return current, false, ErrStepAborted
	case StepActionSkipNode:
		emitStepControlEvent(
			emit, opts, runStart, current.Trace.RunID, node,
			EventStepSkipped, "controller_skip",
		)
		return current, true, nil
	default:
		return current, false, nil
	}
}

func (r *BasicRuntime) handleSequentialAfterStep(
	ctx context.Context,
	g graph.Graph,
	node core.Node,
	current *core.Envelope,
	result *core.Envelope,
	nodeErr error,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
	attempt int,
) error {
	if opts.StepController == nil {
		return nil
	}

	envForStep := result
	if envForStep == nil {
		envForStep = current
	}

	action, _, stepErr := r.handleStepPoint(
		ctx, g, node, envForStep, opts, emit, runStart,
		StepPointAfterNode, attempt, nodeErr,
	)
	if stepErr != nil {
		return stepErr
	}
	if action != StepActionAbort {
		return nil
	}

	emitStepControlEvent(
		emit, opts, runStart, current.Trace.RunID, node,
		EventStepAborted, "controller_abort",
	)
	return ErrStepAborted
}

func emitStepControlEvent(
	emit EventEmitter,
	opts RunOptions,
	runStart time.Time,
	runID string,
	node core.Node,
	kind EventKind,
	reason string,
) {
	emit(NewEvent(kind, runID).
		WithNode(node.ID(), node.Kind()).
		WithElapsed(opts.Now().Sub(runStart)).
		WithPayload("reason", reason))
}

func resolveSequentialNodeOutcome(
	current *core.Envelope,
	result *core.Envelope,
	node core.Node,
	nodeErr error,
	attempt int,
	opts RunOptions,
	nodeID string,
) (*core.Envelope, error) {
	if nodeErr == nil {
		return result, nil
	}
	if !opts.ContinueOnError {
		return current, fmt.Errorf("%w: node %s: %v", ErrNodeExecution, nodeID, nodeErr)
	}

	current.AppendError(core.NodeError{
		NodeID:  nodeID,
		Kind:    node.Kind(),
		Message: nodeErr.Error(),
		Attempt: attempt,
		At:      opts.Now(),
		Cause:   nodeErr,
	})
	return current, nil
}

// nodeResult holds the result of executing a node.
type nodeResult struct {
	nodeID   string
	envelope *core.Envelope
	err      error
}

type nodeState struct {
	hopCount  int
	completed bool
	envelope  *core.Envelope // result envelope for this node
}

type workItem struct {
	nodeID   string
	envelope *core.Envelope
}

type mergeRunner interface {
	MergeInputs(ctx context.Context, inputs []*core.Envelope) (*core.Envelope, error)
}

type parallelState struct {
	states   map[string]*nodeState
	statesMu sync.Mutex

	recordedErrors []core.NodeError
	errorsMu       sync.Mutex

	// mergeInputs[mergeNodeID] = list of envelopes from predecessors
	mergeInputs map[string][]*core.Envelope
	mergeMu     sync.Mutex
}

func newParallelState(entryID string, entryEnv *core.Envelope) *parallelState {
	return &parallelState{
		states: map[string]*nodeState{
			entryID: {
				hopCount:  0,
				completed: false,
				envelope:  entryEnv,
			},
		},
		mergeInputs: make(map[string][]*core.Envelope),
	}
}

func (p *parallelState) incrementHop(nodeID string) (int, *core.Envelope) {
	p.statesMu.Lock()
	defer p.statesMu.Unlock()

	state := p.states[nodeID]
	if state == nil {
		state = &nodeState{}
		p.states[nodeID] = state
	}
	state.hopCount++
	return state.hopCount, state.envelope
}

func (p *parallelState) markNodeCompleted(nodeID string, env *core.Envelope) {
	p.statesMu.Lock()
	defer p.statesMu.Unlock()

	state := p.states[nodeID]
	if state == nil {
		state = &nodeState{}
		p.states[nodeID] = state
	}
	state.completed = true
	state.envelope = env
}

func (p *parallelState) resetNode(nodeID string, env *core.Envelope) {
	p.statesMu.Lock()
	defer p.statesMu.Unlock()
	p.states[nodeID] = &nodeState{hopCount: 0, envelope: env}
}

func (p *parallelState) canScheduleSuccessor(nodeID string, maxHops int) bool {
	p.statesMu.Lock()
	defer p.statesMu.Unlock()

	state := p.states[nodeID]
	if state == nil {
		state = &nodeState{}
		p.states[nodeID] = state
	}
	return state.hopCount < maxHops
}

func (p *parallelState) addMergeInput(nodeID string, env *core.Envelope, expectedInputs int) ([]*core.Envelope, bool) {
	p.mergeMu.Lock()
	defer p.mergeMu.Unlock()

	p.mergeInputs[nodeID] = append(p.mergeInputs[nodeID], env)
	if len(p.mergeInputs[nodeID]) < expectedInputs {
		return nil, false
	}
	return p.mergeInputs[nodeID], true
}

func (p *parallelState) addRecordedError(nodeErr core.NodeError) {
	p.errorsMu.Lock()
	defer p.errorsMu.Unlock()
	p.recordedErrors = append(p.recordedErrors, nodeErr)
}

func (p *parallelState) appendRecordedErrors(target *core.Envelope) {
	p.errorsMu.Lock()
	defer p.errorsMu.Unlock()
	for _, err := range p.recordedErrors {
		target.AppendError(err)
	}
}

// executeGraphParallel executes the graph with concurrent branches.
func (r *BasicRuntime) executeGraphParallel(
	ctx context.Context,
	g graph.Graph,
	env *core.Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*core.Envelope, error) {
	workCh := make(chan workItem, opts.Concurrency*2)
	resultCh := make(chan nodeResult, opts.Concurrency*2)
	state := newParallelState(g.Entry(), env)

	// Context with cancellation for worker shutdown
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	// Start workers
	var wg sync.WaitGroup
	r.startParallelWorkers(workerCtx, g, opts, emit, runStart, workCh, resultCh, &wg)

	stoppedWorkers := false
	stopWorkers := func() {
		if stoppedWorkers {
			return
		}
		stoppedWorkers = true
		cancelWorkers()
		close(workCh)
		wg.Wait()
	}
	defer stopWorkers()

	// Submit entry node
	pendingCount := 1
	workCh <- workItem{nodeID: g.Entry(), envelope: env}

	// Track the final result
	var finalEnvelope *core.Envelope

	// Process results
	for pendingCount > 0 {
		select {
		case <-ctx.Done():
			stopWorkers()
			return env, fmt.Errorf("%w: %v", ErrRunCanceled, ctx.Err())

		case result := <-resultCh:
			pendingCount--
			resultEnvelope, addedPending, err := r.handleParallelResult(ctx, g, result, opts, emit, runStart, state, workCh)
			if err != nil {
				stopWorkers()
				return env, err
			}
			finalEnvelope = resultEnvelope
			pendingCount += addedPending
		}
	}

	// Cleanup
	stopWorkers()

	if finalEnvelope == nil {
		finalEnvelope = env
	}

	// Merge recorded errors into final envelope
	state.appendRecordedErrors(finalEnvelope)

	return finalEnvelope, nil
}

func (r *BasicRuntime) startParallelWorkers(
	workerCtx context.Context,
	g graph.Graph,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
	workCh <-chan workItem,
	resultCh chan<- nodeResult,
	wg *sync.WaitGroup,
) {
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
							err:    fmt.Errorf("%w: %s", graph.ErrNodeNotFound, work.nodeID),
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
}

func (r *BasicRuntime) handleParallelResult(
	ctx context.Context,
	g graph.Graph,
	result nodeResult,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
	state *parallelState,
	workCh chan<- workItem,
) (*core.Envelope, int, error) {
	attempt, previousEnvelope := state.incrementHop(result.nodeID)
	resultEnvelope, err := resolveParallelResultEnvelope(g, result, opts, attempt, previousEnvelope, state)
	if err != nil {
		return nil, 0, err
	}

	state.markNodeCompleted(result.nodeID, resultEnvelope)

	node, exists := g.NodeByID(result.nodeID)
	if !exists {
		return resultEnvelope, 0, nil
	}
	successors := r.determineSuccessors(g, node, resultEnvelope, emit, runStart, opts)

	addedPending, err := r.scheduleParallelSuccessors(ctx, g, resultEnvelope, successors, opts, state, workCh)
	if err != nil {
		return nil, 0, err
	}
	return resultEnvelope, addedPending, nil
}

func resolveParallelResultEnvelope(
	g graph.Graph,
	result nodeResult,
	opts RunOptions,
	attempt int,
	previousEnvelope *core.Envelope,
	state *parallelState,
) (*core.Envelope, error) {
	if result.err == nil {
		return result.envelope, nil
	}
	if !opts.ContinueOnError {
		return nil, fmt.Errorf("%w: node %s: %v", ErrNodeExecution, result.nodeID, result.err)
	}

	node, _ := g.NodeByID(result.nodeID)
	state.addRecordedError(core.NodeError{
		NodeID:  result.nodeID,
		Kind:    nodeKindOrUnknown(node),
		Message: result.err.Error(),
		Attempt: attempt,
		At:      opts.Now(),
		Cause:   result.err,
	})
	return previousEnvelope, nil
}

func nodeKindOrUnknown(node core.Node) core.NodeKind {
	if node == nil {
		return core.NodeKind("unknown")
	}
	return node.Kind()
}

func (r *BasicRuntime) scheduleParallelSuccessors(
	ctx context.Context,
	g graph.Graph,
	resultEnvelope *core.Envelope,
	successors []string,
	opts RunOptions,
	state *parallelState,
	workCh chan<- workItem,
) (int, error) {
	addedPending := 0
	for _, succID := range successors {
		succNode, exists := g.NodeByID(succID)
		if !exists {
			continue
		}

		if mergeNode, ok := succNode.(core.MergeCapable); ok {
			scheduled, err := scheduleMergeSuccessor(ctx, g, succID, succNode, mergeNode, resultEnvelope, opts, state, workCh)
			if err != nil {
				return addedPending, err
			}
			if scheduled {
				addedPending++
			}
			continue
		}

		if !state.canScheduleSuccessor(succID, opts.MaxHops) {
			continue
		}

		// Clone envelope for parallel branches.
		branchEnv := resultEnvelope.Clone()
		workCh <- workItem{nodeID: succID, envelope: branchEnv}
		addedPending++
	}
	return addedPending, nil
}

func scheduleMergeSuccessor(
	ctx context.Context,
	g graph.Graph,
	succID string,
	succNode core.Node,
	mergeNode core.MergeCapable,
	resultEnvelope *core.Envelope,
	opts RunOptions,
	state *parallelState,
	workCh chan<- workItem,
) (bool, error) {
	expectedInputs := mergeNode.ExpectedInputs()
	if expectedInputs == 0 {
		expectedInputs = len(g.Predecessors(succID))
	}

	inputs, ready := state.addMergeInput(succID, resultEnvelope, expectedInputs)
	if !ready {
		return false, nil
	}

	merger, hasMerge := succNode.(mergeRunner)
	if !hasMerge {
		// Fallback: just use first input.
		state.resetNode(succID, inputs[0])
		workCh <- workItem{nodeID: succID, envelope: inputs[0]}
		return true, nil
	}

	mergedEnv, mergeErr := merger.MergeInputs(ctx, inputs)
	if mergeErr != nil {
		if !opts.ContinueOnError {
			return false, fmt.Errorf("merge node %s failed: %w", succID, mergeErr)
		}
		state.addRecordedError(core.NodeError{
			NodeID:  succID,
			Kind:    mergeNode.Kind(),
			Message: mergeErr.Error(),
			At:      opts.Now(),
			Cause:   mergeErr,
		})
		mergedEnv = inputs[0] // fallback to first input
	}

	state.resetNode(succID, mergedEnv)
	workCh <- workItem{nodeID: succID, envelope: mergedEnv}
	return true, nil
}

// determineSuccessors decides which nodes to execute next after the current node.
// For RouterNodes, it uses the RouteDecision; for others, it uses all graph successors.
func (r *BasicRuntime) determineSuccessors(
	g graph.Graph,
	node core.Node,
	env *core.Envelope,
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
	router, isRouter := node.(core.RouterNode)
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

	decision, ok := decisionVal.(core.RouteDecision)
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
	node core.Node,
	env *core.Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
) (*core.Envelope, error) {
	nodeID := node.ID()
	nodeKind := node.Kind()
	runID := env.Trace.RunID

	// Emit node started
	nodeStart := opts.Now()
	emit(NewEvent(EventNodeStarted, runID).
		WithNode(nodeID, nodeKind).
		WithElapsed(nodeStart.Sub(runStart)))

	// Inject emitter into context for node use
	nodeCtx := ContextWithEmitter(ctx, emit)

	// Execute node
	result, err := node.Run(nodeCtx, env)

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
	g graph.Graph,
	node core.Node,
	env *core.Envelope,
	opts RunOptions,
	emit EventEmitter,
	runStart time.Time,
	point StepPoint,
	hopCount int,
	nodeErr error,
) (StepAction, *core.Envelope, error) {
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
	var modifiedEnv *core.Envelope
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
func validateGraph(g graph.Graph) error {
	if g == nil {
		return errors.New("graph is nil")
	}

	nodes := g.Nodes()
	if len(nodes) == 0 {
		return graph.ErrEmptyGraph
	}

	entry := g.Entry()
	if entry == "" {
		return graph.ErrNoEntryNode
	}

	if _, ok := g.NodeByID(entry); !ok {
		return fmt.Errorf("%w: entry node %q", graph.ErrNodeNotFound, entry)
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
