package petalflow

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Step-through execution errors.
var (
	ErrStepAborted         = errors.New("execution aborted by step controller")
	ErrStepRequestNotFound = errors.New("step request not found")
)

// StepAction specifies what the runtime should do at a step point.
type StepAction string

const (
	// StepActionContinue proceeds to the next step point.
	StepActionContinue StepAction = "continue"

	// StepActionSkipNode skips the current node entirely.
	StepActionSkipNode StepAction = "skip"

	// StepActionAbort stops execution immediately.
	StepActionAbort StepAction = "abort"

	// StepActionRunToBreakpoint continues until the next breakpoint or end.
	StepActionRunToBreakpoint StepAction = "run_to_breakpoint"
)

// StepPoint indicates when a step pause occurred.
type StepPoint string

const (
	// StepPointBeforeNode pauses before the node executes.
	StepPointBeforeNode StepPoint = "before_node"

	// StepPointAfterNode pauses after the node executes.
	StepPointAfterNode StepPoint = "after_node"
)

// StepRequest contains information about the current step point.
type StepRequest struct {
	// ID is a unique identifier for this step.
	ID string

	// RunID is the workflow run identifier.
	RunID string

	// Point indicates when this pause occurred (before/after node).
	Point StepPoint

	// NodeID is the ID of the node at this step point.
	NodeID string

	// NodeKind is the type of node.
	NodeKind NodeKind

	// Envelope is a read-only snapshot of current state.
	Envelope *EnvelopeSnapshot

	// HopCount is how many times this node has been visited.
	HopCount int

	// Error is set if StepPointAfterNode and node failed.
	Error error

	// Graph provides graph context for navigation.
	Graph GraphSnapshot

	// CreatedAt is when this step request was created.
	CreatedAt time.Time
}

// StepResponse contains the controller's decision.
type StepResponse struct {
	// RequestID matches the StepRequest.ID.
	RequestID string

	// Action specifies what to do next.
	Action StepAction

	// ModifiedEnvelope optionally provides envelope modifications.
	// Only applied for StepPointBeforeNode with StepActionContinue.
	ModifiedEnvelope *EnvelopeModification

	// Meta contains optional debugging metadata.
	Meta map[string]any
}

// EnvelopeSnapshot is a read-only view of the envelope state.
type EnvelopeSnapshot struct {
	Input     any
	Vars      map[string]any
	Artifacts []Artifact
	Messages  []Message
	Errors    []NodeError
	Trace     TraceInfo
}

// EnvelopeModification specifies changes to apply to the envelope.
type EnvelopeModification struct {
	// SetVars specifies variables to set or update.
	SetVars map[string]any

	// DeleteVars specifies variable names to remove.
	DeleteVars []string
}

// GraphSnapshot provides read-only graph context.
type GraphSnapshot struct {
	Name         string
	CurrentNode  string
	Successors   []string
	Predecessors []string
	AllNodes     []string
}

// StepConfig configures step-through behavior.
type StepConfig struct {
	// PauseBeforeNode pauses before each node executes.
	PauseBeforeNode bool

	// PauseAfterNode pauses after each node executes.
	PauseAfterNode bool

	// StepTimeout for each step decision. 0 means no timeout.
	StepTimeout time.Duration
}

// DefaultStepConfig returns sensible defaults for step-through.
func DefaultStepConfig() *StepConfig {
	return &StepConfig{
		PauseBeforeNode: true,
		PauseAfterNode:  false,
		StepTimeout:     0,
	}
}

// StepController is the interface for controlling step-through execution.
// Implementations block on Step() until they decide what action to take.
type StepController interface {
	// Step is called at each step point and blocks until a decision is made.
	// The context allows for timeout/cancellation.
	Step(ctx context.Context, req *StepRequest) (*StepResponse, error)

	// ShouldPause returns true if execution should pause at this point.
	// This allows breakpoint-only controllers to skip non-breakpoint nodes.
	ShouldPause(nodeID string, point StepPoint) bool
}

// -----------------------------------------------------------------------------
// CallbackStepController
// -----------------------------------------------------------------------------

// StepCallback is the function signature for CallbackStepController.
type StepCallback func(ctx context.Context, req *StepRequest) (*StepResponse, error)

// ShouldPauseFunc is an optional predicate for CallbackStepController.
type ShouldPauseFunc func(nodeID string, point StepPoint) bool

// CallbackStepController invokes a function at each step.
// Simplest controller for custom integrations.
type CallbackStepController struct {
	callback    StepCallback
	shouldPause ShouldPauseFunc
}

// NewCallbackStepController creates a callback-based controller.
func NewCallbackStepController(callback StepCallback) *CallbackStepController {
	return &CallbackStepController{
		callback:    callback,
		shouldPause: nil, // pause at all points by default
	}
}

// WithShouldPause sets a custom pause predicate.
func (c *CallbackStepController) WithShouldPause(fn ShouldPauseFunc) *CallbackStepController {
	c.shouldPause = fn
	return c
}

// Step implements StepController.
func (c *CallbackStepController) Step(ctx context.Context, req *StepRequest) (*StepResponse, error) {
	return c.callback(ctx, req)
}

// ShouldPause implements StepController.
func (c *CallbackStepController) ShouldPause(nodeID string, point StepPoint) bool {
	if c.shouldPause != nil {
		return c.shouldPause(nodeID, point)
	}
	return true // pause at all points by default
}

// -----------------------------------------------------------------------------
// ChannelStepController
// -----------------------------------------------------------------------------

// ChannelStepController uses Go channels for interactive debugging.
// Similar to ChannelHumanHandler - useful for CLI tools and testing.
type ChannelStepController struct {
	requests  chan *StepRequest
	responses chan *StepResponse
	mu        sync.Mutex
	pending   map[string]*StepRequest

	// Breakpoints is a set of node IDs that should pause.
	// If empty, all nodes pause.
	Breakpoints map[string]bool

	// PausePoints specifies which step points to pause at.
	// If empty, pauses at all points.
	PausePoints map[StepPoint]bool

	// runToBreakpoint when true, only pause at breakpoints
	runToBreakpoint bool
}

// NewChannelStepController creates a new channel-based controller.
func NewChannelStepController(bufferSize int) *ChannelStepController {
	return &ChannelStepController{
		requests:    make(chan *StepRequest, bufferSize),
		responses:   make(chan *StepResponse, bufferSize),
		pending:     make(map[string]*StepRequest),
		Breakpoints: make(map[string]bool),
		PausePoints: make(map[StepPoint]bool),
	}
}

// Step implements StepController.
func (c *ChannelStepController) Step(ctx context.Context, req *StepRequest) (*StepResponse, error) {
	c.mu.Lock()
	c.pending[req.ID] = req
	c.mu.Unlock()

	// Send request to channel
	select {
	case c.requests <- req:
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return nil, ctx.Err()
	}

	// Wait for response
	for {
		select {
		case resp := <-c.responses:
			if resp.RequestID == req.ID {
				c.mu.Lock()
				delete(c.pending, req.ID)
				// Handle run-to-breakpoint
				if resp.Action == StepActionRunToBreakpoint {
					c.runToBreakpoint = true
				}
				c.mu.Unlock()
				return resp, nil
			}
			// Response is for a different request, put it back
			select {
			case c.responses <- resp:
			default:
			}
		case <-ctx.Done():
			c.mu.Lock()
			delete(c.pending, req.ID)
			c.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// ShouldPause implements StepController.
func (c *ChannelStepController) ShouldPause(nodeID string, point StepPoint) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If running to breakpoint, only pause at breakpoints
	if c.runToBreakpoint {
		if c.Breakpoints[nodeID] {
			c.runToBreakpoint = false // stop run-to-breakpoint mode
			return true
		}
		return false
	}

	// Check pause points filter
	if len(c.PausePoints) > 0 && !c.PausePoints[point] {
		return false
	}

	// Check breakpoints filter (if set)
	if len(c.Breakpoints) > 0 {
		return c.Breakpoints[nodeID]
	}

	return true // pause at all points by default
}

// Requests returns the channel for receiving step requests.
func (c *ChannelStepController) Requests() <-chan *StepRequest {
	return c.requests
}

// Respond sends a response for a pending request.
func (c *ChannelStepController) Respond(resp *StepResponse) error {
	c.mu.Lock()
	_, ok := c.pending[resp.RequestID]
	c.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrStepRequestNotFound, resp.RequestID)
	}

	c.responses <- resp
	return nil
}

// SetBreakpoint adds a breakpoint on a node.
func (c *ChannelStepController) SetBreakpoint(nodeID string) {
	c.mu.Lock()
	c.Breakpoints[nodeID] = true
	c.mu.Unlock()
}

// ClearBreakpoint removes a breakpoint from a node.
func (c *ChannelStepController) ClearBreakpoint(nodeID string) {
	c.mu.Lock()
	delete(c.Breakpoints, nodeID)
	c.mu.Unlock()
}

// ClearAllBreakpoints removes all breakpoints.
func (c *ChannelStepController) ClearAllBreakpoints() {
	c.mu.Lock()
	c.Breakpoints = make(map[string]bool)
	c.mu.Unlock()
}

// ListPending returns a list of pending step requests.
func (c *ChannelStepController) ListPending() []*StepRequest {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := make([]*StepRequest, 0, len(c.pending))
	for _, req := range c.pending {
		result = append(result, req)
	}
	return result
}

// -----------------------------------------------------------------------------
// BreakpointStepController
// -----------------------------------------------------------------------------

// StepHandler is called when a breakpoint is hit.
type StepHandler func(ctx context.Context, req *StepRequest) (*StepResponse, error)

// BreakpointStepController only pauses at specified breakpoints.
// Between breakpoints, execution proceeds normally.
type BreakpointStepController struct {
	breakpoints map[string]StepPoint // nodeID -> which point to break at
	handler     StepHandler
	mu          sync.RWMutex
}

// NewBreakpointStepController creates a breakpoint-only controller.
func NewBreakpointStepController(handler StepHandler) *BreakpointStepController {
	return &BreakpointStepController{
		breakpoints: make(map[string]StepPoint),
		handler:     handler,
	}
}

// AddBreakpoint adds a breakpoint at a specific node and point.
func (c *BreakpointStepController) AddBreakpoint(nodeID string, point StepPoint) {
	c.mu.Lock()
	c.breakpoints[nodeID] = point
	c.mu.Unlock()
}

// AddBreakpointBefore adds a breakpoint before the node executes.
func (c *BreakpointStepController) AddBreakpointBefore(nodeID string) {
	c.AddBreakpoint(nodeID, StepPointBeforeNode)
}

// AddBreakpointAfter adds a breakpoint after the node executes.
func (c *BreakpointStepController) AddBreakpointAfter(nodeID string) {
	c.AddBreakpoint(nodeID, StepPointAfterNode)
}

// RemoveBreakpoint removes a breakpoint.
func (c *BreakpointStepController) RemoveBreakpoint(nodeID string) {
	c.mu.Lock()
	delete(c.breakpoints, nodeID)
	c.mu.Unlock()
}

// ClearAllBreakpoints removes all breakpoints.
func (c *BreakpointStepController) ClearAllBreakpoints() {
	c.mu.Lock()
	c.breakpoints = make(map[string]StepPoint)
	c.mu.Unlock()
}

// Step implements StepController.
func (c *BreakpointStepController) Step(ctx context.Context, req *StepRequest) (*StepResponse, error) {
	return c.handler(ctx, req)
}

// ShouldPause implements StepController.
func (c *BreakpointStepController) ShouldPause(nodeID string, point StepPoint) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	bp, ok := c.breakpoints[nodeID]
	if !ok {
		return false
	}
	return bp == point
}

// ListBreakpoints returns all breakpoints.
func (c *BreakpointStepController) ListBreakpoints() map[string]StepPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]StepPoint, len(c.breakpoints))
	for k, v := range c.breakpoints {
		result[k] = v
	}
	return result
}

// -----------------------------------------------------------------------------
// AutoStepController
// -----------------------------------------------------------------------------

// AutoStepController automatically continues with a configurable delay.
// Useful for observing execution flow without manual intervention.
type AutoStepController struct {
	delay       time.Duration
	logHandler  func(req *StepRequest)
	breakpoints map[string]bool
	paused      bool
	pauseCh     chan struct{}
	resumeCh    chan struct{}
	mu          sync.Mutex
}

// NewAutoStepController creates an auto-stepping controller.
func NewAutoStepController(delay time.Duration) *AutoStepController {
	return &AutoStepController{
		delay:       delay,
		breakpoints: make(map[string]bool),
		pauseCh:     make(chan struct{}),
		resumeCh:    make(chan struct{}),
	}
}

// WithLogHandler sets a logging callback for each step.
func (c *AutoStepController) WithLogHandler(fn func(req *StepRequest)) *AutoStepController {
	c.logHandler = fn
	return c
}

// Step implements StepController.
func (c *AutoStepController) Step(ctx context.Context, req *StepRequest) (*StepResponse, error) {
	// Call log handler if set
	if c.logHandler != nil {
		c.logHandler(req)
	}

	// Check if paused
	c.mu.Lock()
	isPaused := c.paused
	c.mu.Unlock()

	if isPaused {
		// Wait for resume or context cancellation
		select {
		case <-c.resumeCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Apply delay
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return &StepResponse{
		RequestID: req.ID,
		Action:    StepActionContinue,
	}, nil
}

// ShouldPause implements StepController.
func (c *AutoStepController) ShouldPause(nodeID string, point StepPoint) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If breakpoints are set, only pause at those nodes
	if len(c.breakpoints) > 0 {
		return c.breakpoints[nodeID]
	}

	return true // pause (with auto-continue) at all points
}

// Pause stops auto-stepping until Resume is called.
func (c *AutoStepController) Pause() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

// Resume continues auto-stepping.
func (c *AutoStepController) Resume() {
	c.mu.Lock()
	c.paused = false
	c.mu.Unlock()

	// Signal any waiting Step() calls
	select {
	case c.resumeCh <- struct{}{}:
	default:
	}
}

// SetDelay changes the step delay.
func (c *AutoStepController) SetDelay(d time.Duration) {
	c.mu.Lock()
	c.delay = d
	c.mu.Unlock()
}

// SetBreakpoint adds a breakpoint on a node.
func (c *AutoStepController) SetBreakpoint(nodeID string) {
	c.mu.Lock()
	c.breakpoints[nodeID] = true
	c.mu.Unlock()
}

// ClearBreakpoint removes a breakpoint from a node.
func (c *AutoStepController) ClearBreakpoint(nodeID string) {
	c.mu.Lock()
	delete(c.breakpoints, nodeID)
	c.mu.Unlock()
}

// -----------------------------------------------------------------------------
// Helper functions
// -----------------------------------------------------------------------------

// createEnvelopeSnapshot creates a read-only snapshot of the envelope.
func createEnvelopeSnapshot(env *Envelope) *EnvelopeSnapshot {
	if env == nil {
		return nil
	}

	snapshot := &EnvelopeSnapshot{
		Input:     env.Input,
		Vars:      make(map[string]any, len(env.Vars)),
		Artifacts: make([]Artifact, len(env.Artifacts)),
		Messages:  make([]Message, len(env.Messages)),
		Errors:    make([]NodeError, len(env.Errors)),
		Trace:     env.Trace,
	}

	for k, v := range env.Vars {
		snapshot.Vars[k] = v
	}
	copy(snapshot.Artifacts, env.Artifacts)
	copy(snapshot.Messages, env.Messages)
	copy(snapshot.Errors, env.Errors)

	return snapshot
}

// createGraphSnapshot creates a read-only snapshot of graph context.
func createGraphSnapshot(g Graph, currentNode string) GraphSnapshot {
	nodes := g.Nodes()
	nodeIDs := make([]string, len(nodes))
	for i, n := range nodes {
		nodeIDs[i] = n.ID()
	}

	return GraphSnapshot{
		Name:         g.Name(),
		CurrentNode:  currentNode,
		Successors:   g.Successors(currentNode),
		Predecessors: g.Predecessors(currentNode),
		AllNodes:     nodeIDs,
	}
}

// generateStepID creates a unique step identifier.
func generateStepID() string {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("step-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("step-%x", b)
}

// Ensure interface compliance at compile time.
var (
	_ StepController = (*CallbackStepController)(nil)
	_ StepController = (*ChannelStepController)(nil)
	_ StepController = (*BreakpointStepController)(nil)
	_ StepController = (*AutoStepController)(nil)
)
