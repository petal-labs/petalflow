package petalflow

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
)

// HumanRequestType specifies what kind of human input is needed.
type HumanRequestType string

const (
	// HumanRequestApproval is a yes/no decision.
	HumanRequestApproval HumanRequestType = "approval"

	// HumanRequestChoice is selecting from predefined options.
	HumanRequestChoice HumanRequestType = "choice"

	// HumanRequestEdit is modifying data.
	HumanRequestEdit HumanRequestType = "edit"

	// HumanRequestInput is free-form text input.
	HumanRequestInput HumanRequestType = "input"

	// HumanRequestReview is reviewing with notes.
	HumanRequestReview HumanRequestType = "review"
)

// HumanTimeoutAction specifies behavior when timeout is reached.
type HumanTimeoutAction string

const (
	// HumanTimeoutFail returns an error on timeout.
	HumanTimeoutFail HumanTimeoutAction = "fail"

	// HumanTimeoutDefault uses the default option on timeout.
	HumanTimeoutDefault HumanTimeoutAction = "default"

	// HumanTimeoutSkip continues without change on timeout.
	HumanTimeoutSkip HumanTimeoutAction = "skip"
)

// HumanOption represents a choice option for approval/choice requests.
type HumanOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// HumanRequest represents a request for human input.
type HumanRequest struct {
	ID          string           `json:"id"`
	Type        HumanRequestType `json:"type"`
	Prompt      string           `json:"prompt"`
	Data        any              `json:"data,omitempty"`
	Options     []HumanOption    `json:"options,omitempty"`
	Schema      map[string]any   `json:"schema,omitempty"`
	Timeout     time.Duration    `json:"timeout,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
	EnvelopeRef string           `json:"envelope_ref,omitempty"`
}

// HumanResponse represents a human's response to a request.
type HumanResponse struct {
	RequestID   string         `json:"request_id"`
	Choice      string         `json:"choice,omitempty"`
	Data        any            `json:"data,omitempty"`
	Notes       string         `json:"notes,omitempty"`
	Approved    bool           `json:"approved"`
	RespondedBy string         `json:"responded_by,omitempty"`
	RespondedAt time.Time      `json:"responded_at"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// HumanHandler is the interface for human interaction backends.
type HumanHandler interface {
	// Request presents the request to a human and waits for response.
	Request(ctx context.Context, req *HumanRequest) (*HumanResponse, error)
}

// HumanNodeConfig configures a HumanNode.
type HumanNodeConfig struct {
	// RequestType specifies what kind of human input is needed.
	RequestType HumanRequestType

	// Prompt is the message shown to the human reviewer.
	Prompt string

	// PromptTemplate renders the prompt from envelope data.
	// If set, overrides Prompt.
	PromptTemplate string

	// InputVars specifies which variables to show for review.
	InputVars []string

	// OutputVar stores the human response.
	OutputVar string

	// Options specifies allowed responses (for approval/choice types).
	Options []HumanOption

	// Schema specifies expected response format (for edit type).
	Schema map[string]any

	// Timeout is the maximum wait time (0 = no timeout).
	Timeout time.Duration

	// OnTimeout specifies behavior when timeout is reached.
	// Defaults to HumanTimeoutFail.
	OnTimeout HumanTimeoutAction

	// DefaultOption is used when timeout occurs (if OnTimeout is "default").
	DefaultOption string

	// Handler is the callback invoked when human input is needed.
	Handler HumanHandler
}

// HumanNode pauses workflow execution for human approval, edit, or input.
type HumanNode struct {
	BaseNode
	config HumanNodeConfig
}

// NewHumanNode creates a new HumanNode with the given configuration.
func NewHumanNode(id string, config HumanNodeConfig) *HumanNode {
	// Set defaults
	if config.RequestType == "" {
		config.RequestType = HumanRequestApproval
	}
	if config.OnTimeout == "" {
		config.OnTimeout = HumanTimeoutFail
	}

	return &HumanNode{
		BaseNode: NewBaseNode(id, NodeKindHuman),
		config:   config,
	}
}

// Config returns the node's configuration.
func (n *HumanNode) Config() HumanNodeConfig {
	return n.config
}

// Run executes the human node logic.
func (n *HumanNode) Run(ctx context.Context, env *Envelope) (*Envelope, error) {
	// Check context
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Validate handler
	if n.config.Handler == nil {
		return nil, fmt.Errorf("human node %s: no handler configured", n.id)
	}

	// Build prompt
	prompt, err := n.buildPrompt(env)
	if err != nil {
		return nil, fmt.Errorf("human node %s: failed to build prompt: %w", n.id, err)
	}

	// Build request data
	data := n.buildRequestData(env)

	// Create request
	req := &HumanRequest{
		ID:          uuid.New().String(),
		Type:        n.config.RequestType,
		Prompt:      prompt,
		Data:        data,
		Options:     n.config.Options,
		Schema:      n.config.Schema,
		Timeout:     n.config.Timeout,
		CreatedAt:   time.Now(),
		EnvelopeRef: env.Trace.RunID,
	}

	// Apply timeout if configured
	if n.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, n.config.Timeout)
		defer cancel()
	}

	// Request human input
	resp, err := n.config.Handler.Request(ctx, req)

	// Handle timeout
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return n.handleTimeout(env, req)
	}

	if err != nil {
		return nil, fmt.Errorf("human node %s: handler error: %w", n.id, err)
	}

	// Clone envelope for result
	result := env.Clone()

	// Store response if configured
	if n.config.OutputVar != "" {
		result.SetVar(n.config.OutputVar, resp)
	}

	// For approval type, also store approval status directly
	if n.config.RequestType == HumanRequestApproval {
		result.SetVar(n.config.OutputVar+"_approved", resp.Approved)
	}

	// For edit type, merge edited data back into envelope
	if n.config.RequestType == HumanRequestEdit && resp.Data != nil {
		if editedMap, ok := resp.Data.(map[string]any); ok {
			for k, v := range editedMap {
				result.SetVar(k, v)
			}
		}
	}

	return result, nil
}

// buildPrompt builds the prompt string.
func (n *HumanNode) buildPrompt(env *Envelope) (string, error) {
	// Use template if provided
	if n.config.PromptTemplate != "" {
		tmpl, err := template.New("prompt").Parse(n.config.PromptTemplate)
		if err != nil {
			return "", fmt.Errorf("invalid prompt template: %w", err)
		}

		data := map[string]any{
			"input": env.Input,
			"vars":  env.Vars,
			"trace": env.Trace,
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			return "", fmt.Errorf("template execution failed: %w", err)
		}

		return buf.String(), nil
	}

	// Use static prompt
	return n.config.Prompt, nil
}

// buildRequestData builds the data to show for review.
func (n *HumanNode) buildRequestData(env *Envelope) map[string]any {
	data := make(map[string]any)

	// Include specific vars or all vars
	if len(n.config.InputVars) > 0 {
		for _, varName := range n.config.InputVars {
			if val, ok := env.GetVar(varName); ok {
				data[varName] = val
			}
		}
	} else if len(env.Vars) > 0 {
		for k, v := range env.Vars {
			data[k] = v
		}
	}

	return data
}

// handleTimeout handles timeout based on configured action.
func (n *HumanNode) handleTimeout(env *Envelope, req *HumanRequest) (*Envelope, error) {
	switch n.config.OnTimeout {
	case HumanTimeoutFail:
		return nil, fmt.Errorf("human node %s: request timed out after %v", n.id, n.config.Timeout)

	case HumanTimeoutDefault:
		if n.config.DefaultOption == "" {
			return nil, fmt.Errorf("human node %s: timeout with default action but no DefaultOption configured", n.id)
		}

		// Create default response
		resp := &HumanResponse{
			RequestID:   req.ID,
			Choice:      n.config.DefaultOption,
			Approved:    n.config.DefaultOption == "approve" || n.config.DefaultOption == "yes",
			RespondedAt: time.Now(),
			Meta:        map[string]any{"timeout": true, "default": true},
		}

		result := env.Clone()
		if n.config.OutputVar != "" {
			result.SetVar(n.config.OutputVar, resp)
		}
		return result, nil

	case HumanTimeoutSkip:
		// Continue without change
		result := env.Clone()
		if n.config.OutputVar != "" {
			result.SetVar(n.config.OutputVar, &HumanResponse{
				RequestID:   req.ID,
				RespondedAt: time.Now(),
				Meta:        map[string]any{"timeout": true, "skipped": true},
			})
		}
		return result, nil

	default:
		return nil, fmt.Errorf("human node %s: unknown timeout action: %s", n.id, n.config.OnTimeout)
	}
}

// ChannelHumanHandler uses Go channels for in-process human simulation.
// Useful for testing and CLI tools.
type ChannelHumanHandler struct {
	requests  chan *HumanRequest
	responses chan *HumanResponse
	mu        sync.Mutex
	pending   map[string]*HumanRequest
}

// NewChannelHumanHandler creates a new channel-based handler.
func NewChannelHumanHandler(bufferSize int) *ChannelHumanHandler {
	if bufferSize <= 0 {
		bufferSize = 10
	}
	return &ChannelHumanHandler{
		requests:  make(chan *HumanRequest, bufferSize),
		responses: make(chan *HumanResponse, bufferSize),
		pending:   make(map[string]*HumanRequest),
	}
}

// Request implements HumanHandler interface.
func (h *ChannelHumanHandler) Request(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
	// Track pending request
	h.mu.Lock()
	h.pending[req.ID] = req
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.pending, req.ID)
		h.mu.Unlock()
	}()

	// Send request
	select {
	case h.requests <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Wait for response
	select {
	case resp := <-h.responses:
		if resp.RequestID != req.ID {
			// Response for different request - put it back and wait
			h.responses <- resp
			select {
			case resp := <-h.responses:
				return resp, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Requests returns the requests channel for receiving requests.
func (h *ChannelHumanHandler) Requests() <-chan *HumanRequest {
	return h.requests
}

// Respond sends a response to a pending request.
func (h *ChannelHumanHandler) Respond(resp *HumanResponse) error {
	h.mu.Lock()
	_, exists := h.pending[resp.RequestID]
	h.mu.Unlock()

	if !exists {
		return fmt.Errorf("no pending request with ID: %s", resp.RequestID)
	}

	h.responses <- resp
	return nil
}

// GetPending returns the pending request by ID.
func (h *ChannelHumanHandler) GetPending(id string) (*HumanRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	req, ok := h.pending[id]
	return req, ok
}

// PendingCount returns the number of pending requests.
func (h *ChannelHumanHandler) PendingCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pending)
}

// CallbackHumanHandler invokes a function for human interaction.
// Useful for custom integrations.
type CallbackHumanHandler struct {
	callback func(ctx context.Context, req *HumanRequest) (*HumanResponse, error)
}

// NewCallbackHumanHandler creates a new callback-based handler.
func NewCallbackHumanHandler(callback func(ctx context.Context, req *HumanRequest) (*HumanResponse, error)) *CallbackHumanHandler {
	return &CallbackHumanHandler{
		callback: callback,
	}
}

// Request implements HumanHandler interface.
func (h *CallbackHumanHandler) Request(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
	if h.callback == nil {
		return nil, fmt.Errorf("no callback configured")
	}
	return h.callback(ctx, req)
}

// AutoApproveHandler automatically approves all requests.
// Useful for testing workflows without human interaction.
type AutoApproveHandler struct {
	Approved    bool
	Choice      string
	Data        any
	Notes       string
	RespondedBy string
	Delay       time.Duration
}

// NewAutoApproveHandler creates a handler that auto-approves requests.
func NewAutoApproveHandler() *AutoApproveHandler {
	return &AutoApproveHandler{
		Approved:    true,
		RespondedBy: "auto",
	}
}

// NewAutoRejectHandler creates a handler that auto-rejects requests.
func NewAutoRejectHandler() *AutoApproveHandler {
	return &AutoApproveHandler{
		Approved:    false,
		RespondedBy: "auto",
	}
}

// Request implements HumanHandler interface.
func (h *AutoApproveHandler) Request(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
	// Simulate delay if configured
	if h.Delay > 0 {
		select {
		case <-time.After(h.Delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	choice := h.Choice
	if choice == "" && h.Approved {
		choice = "approve"
	} else if choice == "" {
		choice = "reject"
	}

	return &HumanResponse{
		RequestID:   req.ID,
		Choice:      choice,
		Data:        h.Data,
		Notes:       h.Notes,
		Approved:    h.Approved,
		RespondedBy: h.RespondedBy,
		RespondedAt: time.Now(),
	}, nil
}

// QueuedHumanHandler queues requests and allows async responses.
// Useful for web-based approval workflows.
type QueuedHumanHandler struct {
	mu       sync.RWMutex
	requests map[string]*queuedRequest
}

type queuedRequest struct {
	Request  *HumanRequest
	Response chan *HumanResponse
	Done     chan struct{}
}

// NewQueuedHumanHandler creates a new queued handler.
func NewQueuedHumanHandler() *QueuedHumanHandler {
	return &QueuedHumanHandler{
		requests: make(map[string]*queuedRequest),
	}
}

// Request implements HumanHandler interface.
func (h *QueuedHumanHandler) Request(ctx context.Context, req *HumanRequest) (*HumanResponse, error) {
	qr := &queuedRequest{
		Request:  req,
		Response: make(chan *HumanResponse, 1),
		Done:     make(chan struct{}),
	}

	h.mu.Lock()
	h.requests[req.ID] = qr
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.requests, req.ID)
		h.mu.Unlock()
		close(qr.Done)
	}()

	select {
	case resp := <-qr.Response:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ListPending returns all pending request IDs.
func (h *QueuedHumanHandler) ListPending() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	ids := make([]string, 0, len(h.requests))
	for id := range h.requests {
		ids = append(ids, id)
	}
	return ids
}

// GetRequest returns a pending request by ID.
func (h *QueuedHumanHandler) GetRequest(id string) (*HumanRequest, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	qr, ok := h.requests[id]
	if !ok {
		return nil, false
	}
	return qr.Request, true
}

// Respond sends a response to a pending request.
func (h *QueuedHumanHandler) Respond(id string, resp *HumanResponse) error {
	h.mu.RLock()
	qr, ok := h.requests[id]
	h.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no pending request with ID: %s", id)
	}

	resp.RequestID = id
	if resp.RespondedAt.IsZero() {
		resp.RespondedAt = time.Now()
	}

	select {
	case qr.Response <- resp:
		return nil
	default:
		return fmt.Errorf("response already sent for request: %s", id)
	}
}

// Ensure interface compliance at compile time.
var _ Node = (*HumanNode)(nil)
var _ HumanHandler = (*ChannelHumanHandler)(nil)
var _ HumanHandler = (*CallbackHumanHandler)(nil)
var _ HumanHandler = (*AutoApproveHandler)(nil)
var _ HumanHandler = (*QueuedHumanHandler)(nil)
