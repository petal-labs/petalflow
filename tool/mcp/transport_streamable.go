package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Sentinel transport errors. errors.Is traverses RequestError wrappers, so
// callers can classify terminal or retryable states without string matching.
// Error text never contains credentials, header values, or URL query strings.
var (
	// ErrSessionExpired indicates the MCP session (Mcp-Session-Id) is no
	// longer valid and the session must be re-established via initialize.
	ErrSessionExpired = errors.New("mcp: session expired")
	// ErrUnauthorized indicates the server rejected the request as
	// unauthenticated (HTTP 401) and credential refresh did not resolve
	// it. This is a terminal, non-retryable state.
	ErrUnauthorized = errors.New("mcp: unauthorized")
	// ErrInsufficientScope indicates the server rejected the request as
	// authenticated but not authorized (HTTP 403 / insufficient_scope).
	// This is a terminal, non-retryable state.
	ErrInsufficientScope = errors.New("mcp: insufficient scope")
)

const (
	headerMCPSessionID       = "Mcp-Session-Id"
	headerMCPProtocolVersion = "MCP-Protocol-Version"
	headerLastEventID        = "Last-Event-ID"

	acceptJSONAndEventStream = "application/json, text/event-stream"
	contentTypeJSON          = "application/json"
	contentTypeEventStream   = "text/event-stream"

	// streamDefaultHeaderTimeout bounds how long the transport waits for
	// response headers before considering the endpoint stalled. It does not
	// bound SSE stream bodies, which stay open for server-to-client delivery.
	streamDefaultHeaderTimeout = 30 * time.Second

	// defaults for streamable transport stream re-establishment.
	defaultStreamReconnectAttempts = 3
	defaultStreamReconnectBackoff  = 250 * time.Millisecond

	maxSSEEventSize = 1 << 20
)

// TransportAuthorizer adds authorization material to outgoing HTTP requests.
// Implementations must never log credentials or embed them in returned errors.
type TransportAuthorizer interface {
	// Authorize attaches authorization to a request before it is sent.
	Authorize(ctx context.Context, req *http.Request) error
	// Refresh invalidates and re-obtains credentials after a 401 response.
	// Returning nil authorizes exactly one retry of the failed request.
	Refresh(ctx context.Context) error
}

// StreamableHTTPConfig configures the Streamable HTTP MCP transport.
type StreamableHTTPConfig struct {
	Endpoint string
	Headers  map[string]string
	// Authorizer optionally attaches bearer or OAuth 2.1 authorization.
	Authorizer TransportAuthorizer
	Client     *http.Client
	// ProtocolVersion is sent in the MCP-Protocol-Version header before
	// initialize negotiates one. Empty uses the client default.
	ProtocolVersion string
	// MaxStreamReconnects bounds GET stream re-establishment attempts
	// before the transport reports an unambiguous terminal state.
	MaxStreamReconnects int
	// StreamReconnectBackoff is the base backoff between GET stream
	// re-establishment attempts.
	StreamReconnectBackoff time.Duration
}

// StreamableHTTPTransport implements the MCP Streamable HTTP transport with
// session handling (Mcp-Session-Id), SSE response parsing, server-to-client
// delivery over a GET stream, Last-Event-ID resumption, and typed terminal
// errors for authorization and session failures.
//
// Unlike the stdio and legacy SSE transports it is self-healing: when a
// server reports the session expired it replays the cached initialize
// handshake and retries the message, so callers observe recovery instead of
// protocol drift.
type StreamableHTTPTransport struct {
	cfg    StreamableHTTPConfig
	client *http.Client

	recvCh chan Message
	errCh  chan error

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
	initRequest     *Message
	closed          bool
	postLastEventID string
	getLastEventID  string
	getActive       bool
	getDisabled     bool
	getCancel       context.CancelFunc
}

// NewStreamableHTTPTransport creates a Streamable HTTP MCP transport.
func NewStreamableHTTPTransport(cfg StreamableHTTPConfig) (*StreamableHTTPTransport, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("mcp: streamable http endpoint is required")
	}
	if cfg.ProtocolVersion == "" {
		cfg.ProtocolVersion = defaultProtocolVersion
	}
	if cfg.MaxStreamReconnects <= 0 {
		cfg.MaxStreamReconnects = defaultStreamReconnectAttempts
	}
	if cfg.StreamReconnectBackoff <= 0 {
		cfg.StreamReconnectBackoff = defaultStreamReconnectBackoff
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: streamDefaultHeaderTimeout,
			},
		}
	}
	return &StreamableHTTPTransport{
		cfg:    cfg,
		client: cfg.Client,
		recvCh: make(chan Message, 64),
		errCh:  make(chan error, 1),
	}, nil
}

// Send posts one JSON-RPC message to the MCP endpoint and enqueues any
// response messages returned on the POST response stream.
func (t *StreamableHTTPTransport) Send(ctx context.Context, message Message) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return errors.New("mcp: streamable http transport is closed")
	}

	if message.Method == "initialize" {
		t.beginSession(message)
	}

	return t.sendOnce(ctx, message, false)
}

// sendOnce posts one message; afterSessionRecovery bounds session-expiry
// recovery to a single replay so a misbehaving endpoint cannot loop Send.
func (t *StreamableHTTPTransport) sendOnce(ctx context.Context, message Message, afterSessionRecovery bool) error {
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("mcp: encode request: %w", err)
	}

	resp, err := t.post(ctx, body, false)
	if err != nil {
		return err
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		drainAndClose(resp.Body)
		if afterSessionRecovery {
			return fmt.Errorf("%w: endpoint rejected recovered session", ErrSessionExpired)
		}
		return t.handleSessionExpired(ctx, message)
	case resp.StatusCode == http.StatusUnauthorized:
		drainAndClose(resp.Body)
		return ErrUnauthorized
	case resp.StatusCode == http.StatusForbidden:
		drainAndClose(resp.Body)
		return fmt.Errorf("%w: endpoint rejected requested scopes", ErrInsufficientScope)
	case resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices:
		drainAndClose(resp.Body)
		return fmt.Errorf("mcp: endpoint returned status %d", resp.StatusCode)
	}

	t.captureSession(resp)
	t.consumeResponseStream(ctx, resp)
	t.clearStaleStreamError()
	t.maybeStartGETStream(ctx)
	return nil
}

// Receive waits for the next queued JSON-RPC message. Terminal stream
// failures are surfaced as errors once no queued messages remain.
func (t *StreamableHTTPTransport) Receive(ctx context.Context) (Message, error) {
	for {
		select {
		case msg := <-t.recvCh:
			return msg, nil
		default:
		}

		select {
		case <-ctx.Done():
			return Message{}, ctx.Err()
		case msg := <-t.recvCh:
			return msg, nil
		case err := <-t.errCh:
			return Message{}, err
		}
	}
}

// Close cancels the GET stream and marks the transport closed.
func (t *StreamableHTTPTransport) Close(ctx context.Context) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	cancel := t.getCancel
	t.getCancel = nil
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return nil
}

// post performs one authorized POST to the endpoint. A 401 response
// triggers credential refresh and exactly one retry when an authorizer
// supports it.
func (t *StreamableHTTPTransport) post(ctx context.Context, body []byte, retriedAuth bool) (*http.Response, error) {
	t.mu.Lock()
	sessionID := t.sessionID
	protocolVersion := t.protocolVersion
	lastEventID := t.postLastEventID
	t.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", acceptJSONAndEventStream)
	if sessionID != "" {
		req.Header.Set(headerMCPSessionID, sessionID)
	}
	if protocolVersion != "" {
		req.Header.Set(headerMCPProtocolVersion, protocolVersion)
	}
	if lastEventID != "" {
		req.Header.Set(headerLastEventID, lastEventID)
	}
	for key, value := range t.cfg.Headers {
		req.Header.Set(key, value)
	}
	if t.cfg.Authorizer != nil {
		if err := t.cfg.Authorizer.Authorize(ctx, req); err != nil {
			return nil, fmt.Errorf("mcp: authorize request: %w", err)
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: send request: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized && t.cfg.Authorizer != nil && !retriedAuth {
		drainAndClose(resp.Body)
		if refreshErr := t.cfg.Authorizer.Refresh(ctx); refreshErr != nil {
			return nil, fmt.Errorf("%w: credential refresh failed", ErrUnauthorized)
		}
		return t.post(ctx, body, true)
	}

	return resp, nil
}

// captureSession records the negotiated session id from a response header.
func (t *StreamableHTTPTransport) captureSession(resp *http.Response) {
	sessionID := resp.Header.Get(headerMCPSessionID)
	if sessionID == "" {
		return
	}
	t.mu.Lock()
	t.sessionID = sessionID
	t.mu.Unlock()
}

// consumeResponseStream enqueues JSON-RPC messages from either a single
// application/json body or a text/event-stream POST response.
func (t *StreamableHTTPTransport) consumeResponseStream(ctx context.Context, resp *http.Response) {
	contentType := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, contentTypeEventStream):
		t.readEventStream(ctx, resp.Body, &t.postLastEventID, true)
	case strings.Contains(contentType, contentTypeJSON) || contentType == "":
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			_ = resp.Body.Close()
			return
		}
		_ = resp.Body.Close()
		if len(strings.TrimSpace(string(data))) == 0 {
			return
		}
		var response Message
		if err := json.Unmarshal(data, &response); err != nil {
			return
		}
		t.observeInitializeResult(&response)
		t.enqueue(ctx, response)
	default:
		_ = resp.Body.Close()
	}
}

// handleSessionExpired re-establishes the session by replaying the cached
// initialize handshake and then retrying the message once.
func (t *StreamableHTTPTransport) handleSessionExpired(ctx context.Context, message Message) error {
	t.mu.Lock()
	t.sessionID = ""
	t.mu.Unlock()

	if message.Method == "initialize" {
		return fmt.Errorf("%w: initialize rejected by endpoint", ErrSessionExpired)
	}
	if err := t.replayInitialize(ctx); err != nil {
		return err
	}
	return t.sendOnce(ctx, message, true)
}

// replayInitialize re-runs the cached initialize handshake after session
// loss, including the notifications/initialized follow-up.
func (t *StreamableHTTPTransport) replayInitialize(ctx context.Context) error {
	t.mu.Lock()
	initRequest := t.initRequest
	t.mu.Unlock()
	if initRequest == nil {
		return fmt.Errorf("%w: no cached initialize handshake", ErrSessionExpired)
	}

	body, err := json.Marshal(*initRequest)
	if err != nil {
		return fmt.Errorf("mcp: encode initialize replay: %w", err)
	}

	resp, err := t.post(ctx, body, false)
	if err != nil {
		return fmt.Errorf("mcp: re-initialize after session expiry: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		drainAndClose(resp.Body)
		return fmt.Errorf("%w: re-initialize rejected", ErrUnauthorized)
	}
	if resp.StatusCode == http.StatusNotFound {
		drainAndClose(resp.Body)
		return fmt.Errorf("%w: re-initialize rejected", ErrSessionExpired)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		drainAndClose(resp.Body)
		return fmt.Errorf("mcp: re-initialize returned status %d", resp.StatusCode)
	}

	t.captureSession(resp)
	// The replayed initialize result is consumed internally: the client
	// already holds the original initialize result and its correlation
	// loop ignores mismatched IDs anyway.
	drainAndClose(resp.Body)

	notification, err := json.Marshal(Message{
		JSONRPC: jsonRPCVersion,
		Method:  "notifications/initialized",
		Params:  json.RawMessage(`{}`),
	})
	if err != nil {
		return fmt.Errorf("mcp: encode initialized notification: %w", err)
	}
	notifResp, err := t.post(ctx, notification, false)
	if err != nil {
		return fmt.Errorf("mcp: send initialized notification: %w", err)
	}
	drainAndClose(notifResp.Body)
	return nil
}

// beginSession records the initialize request so the session can be
// re-established transparently after expiry. Event IDs belong to a single
// session's streams and reset with it.
func (t *StreamableHTTPTransport) beginSession(message Message) {
	t.mu.Lock()
	defer t.mu.Unlock()
	cached := message
	t.initRequest = &cached
	t.sessionID = ""
	t.postLastEventID = ""
	t.getLastEventID = ""
}

// clearStaleStreamError drops a queued terminal stream error after a
// successful POST, because a working POST proves the reported terminal
// state (e.g. an expired GET stream) has already been recovered.
func (t *StreamableHTTPTransport) clearStaleStreamError() {
	select {
	case <-t.errCh:
	default:
	}
}

// observeInitializeResult captures the negotiated protocol version from an
// initialize response flowing through the transport.
func (t *StreamableHTTPTransport) observeInitializeResult(response *Message) {
	if response == nil || len(response.Result) == 0 {
		return
	}

	var result InitializeResult
	if err := json.Unmarshal(response.Result, &result); err != nil || result.ProtocolVersion == "" {
		return
	}

	t.mu.Lock()
	t.protocolVersion = result.ProtocolVersion
	t.mu.Unlock()
}

// readEventStream parses one SSE body, enqueuing JSON-RPC messages and
// tracking the last event ID for resumption. It runs synchronously for POST
// responses and in the GET stream goroutine for server-to-client delivery.
func (t *StreamableHTTPTransport) readEventStream(ctx context.Context, body io.Reader, lastEventID *string, trackInit bool) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxSSEEventSize)

	var dataLines []string
	eventID := ""
	eventType := ""

	flush := func() {
		if len(dataLines) == 0 {
			eventID = ""
			eventType = ""
			return
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		id := eventID
		kind := eventType
		eventID = ""
		eventType = ""

		if id != "" {
			t.mu.Lock()
			*lastEventID = id
			t.mu.Unlock()
		}
		if kind != "" && kind != "message" {
			return
		}
		var message Message
		if err := json.Unmarshal([]byte(data), &message); err != nil {
			return
		}
		if trackInit {
			t.observeInitializeResult(&message)
		}
		t.enqueue(ctx, message)
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, ":"):
			// SSE comment/keepalive.
		case strings.HasPrefix(line, "id:"):
			eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	flush()
}

// enqueue delivers a message to Receive, dropping it only if the transport
// is closed or the stream context is done.
func (t *StreamableHTTPTransport) enqueue(ctx context.Context, message Message) {
	select {
	case t.recvCh <- message:
	case <-ctx.Done():
	}
}

// maybeStartGETStream opens the server-to-client SSE stream once a session
// is established.
func (t *StreamableHTTPTransport) maybeStartGETStream(ctx context.Context) {
	t.mu.Lock()
	if t.closed || t.getActive || t.getDisabled || t.sessionID == "" {
		t.mu.Unlock()
		return
	}
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t.getActive = true
	t.getCancel = cancel
	t.mu.Unlock()

	go t.runGETStream(streamCtx)
}

// runGETStream maintains the GET stream, re-establishing it with the last
// event id until the terminal budget is exhausted or the transport closes.
func (t *StreamableHTTPTransport) runGETStream(ctx context.Context) {
	for attempt := 0; ; attempt++ {
		statusCode, err := t.openGETStream(ctx)
		if ctx.Err() != nil {
			return
		}

		if statusCode == http.StatusMethodNotAllowed {
			t.disableGETStream()
			return
		}

		t.mu.Lock()
		closed := t.closed
		t.mu.Unlock()
		if closed {
			return
		}

		if statusCode == http.StatusNotFound {
			t.failGETStream(ErrSessionExpired)
			return
		}
		if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
			t.failGETStream(mapStatusToAuthError(statusCode))
			return
		}
		if attempt >= t.cfg.MaxStreamReconnects {
			if err == nil {
				t.failGETStream(fmt.Errorf("mcp: stream interrupted after %d reconnect attempts", attempt+1))
			} else {
				t.failGETStream(fmt.Errorf("mcp: stream interrupted after %d reconnect attempts: %w", attempt+1, err))
			}
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(t.cfg.StreamReconnectBackoff * time.Duration(1<<attempt)):
		}
	}
}

// openGETStream issues one GET request and consumes the event stream until
// the server closes it or the context is cancelled.
func (t *StreamableHTTPTransport) openGETStream(ctx context.Context) (int, error) {
	t.mu.Lock()
	sessionID := t.sessionID
	protocolVersion := t.protocolVersion
	lastEventID := t.getLastEventID
	t.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.cfg.Endpoint, nil)
	if err != nil {
		return 0, fmt.Errorf("mcp: build stream request: %w", err)
	}
	req.Header.Set("Accept", contentTypeEventStream)
	if sessionID != "" {
		req.Header.Set(headerMCPSessionID, sessionID)
	}
	if protocolVersion != "" {
		req.Header.Set(headerMCPProtocolVersion, protocolVersion)
	}
	if lastEventID != "" {
		req.Header.Set(headerLastEventID, lastEventID)
	}
	for key, value := range t.cfg.Headers {
		req.Header.Set(key, value)
	}
	if t.cfg.Authorizer != nil {
		if err := t.cfg.Authorizer.Authorize(ctx, req); err != nil {
			return 0, fmt.Errorf("mcp: authorize stream request: %w", err)
		}
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		drainAndClose(resp.Body)
		return resp.StatusCode, fmt.Errorf("mcp: stream returned status %d", resp.StatusCode)
	}

	t.readEventStream(ctx, resp.Body, &t.getLastEventID, false)
	return http.StatusOK, nil
}

// disableGETStream permanently disables the server-to-client stream because
// the endpoint does not offer one (HTTP 405), which is spec-permitted.
func (t *StreamableHTTPTransport) disableGETStream() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.getDisabled = true
	t.getActive = false
	if t.getCancel != nil {
		t.getCancel()
		t.getCancel = nil
	}
}

// failGETStream reports an unambiguous terminal stream state to Receive
// without discarding any queued response messages.
func (t *StreamableHTTPTransport) failGETStream(err error) {
	t.mu.Lock()
	t.getActive = false
	closed := t.closed
	t.mu.Unlock()
	if closed {
		return
	}
	select {
	case t.errCh <- err:
	default:
	}
}

func mapStatusToAuthError(statusCode int) error {
	if statusCode == http.StatusForbidden {
		return ErrInsufficientScope
	}
	return ErrUnauthorized
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64*1024))
	_ = body.Close()
}
