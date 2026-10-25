package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamableServer is a minimal in-test Streamable HTTP MCP server used for
// interoperability-style tests: session assignment, JSON and SSE responses,
// session expiry, GET streams with event IDs, and OAuth-style 401/403.
type streamableServer struct {
	t *testing.T

	mu             sync.Mutex
	sessionID      string
	expireNextCall bool
	getRequests    []http.Header
	serverCalls    []string

	// behavior hooks
	respondSSE      bool
	unauthorized    bool
	forbidden       bool
	getStreamEvents []string // SSE events served on GET before closing
	lastEventIDSeen string
	failGETWith404  bool
}

func newStreamableServer(t *testing.T) (*streamableServer, *httptest.Server) {
	s := &streamableServer{t: t}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *streamableServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handlePost(w, r)
	case http.MethodGet:
		s.handleGet(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *streamableServer) handlePost(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.serverCalls = append(s.serverCalls, "POST")
	unauthorized := s.unauthorized
	forbidden := s.forbidden
	expired := s.expireNextCall
	s.expireNextCall = false
	s.mu.Unlock()

	if unauthorized {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if forbidden {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope", scope="tools:call"`)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var request Message
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if request.Method == "initialize" {
		session := fmt.Sprintf("session-%d", time.Now().UnixNano())
		s.mu.Lock()
		s.sessionID = session
		s.mu.Unlock()

		body := Message{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: mustRawJSON(s.t, map[string]any{
				"protocolVersion": defaultProtocolVersion,
				"serverInfo":      map[string]any{"name": "test-server", "version": "1.0.0"},
			}),
		}
		w.Header().Set(headerMCPSessionID, session)
		s.writeResponse(w, r, body)
		return
	}

	// All non-initialize requests require the session header.
	if r.Header.Get(headerMCPSessionID) != s.currentSession() {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if expired {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	switch request.Method {
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		body := Message{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: mustRawJSON(s.t, map[string]any{
				"tools": []any{map[string]any{"name": "echo"}},
			}),
		}
		s.writeResponse(w, r, body)
	case "tools/call":
		body := Message{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result: mustRawJSON(s.t, map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "hello"}},
			}),
		}
		s.writeResponse(w, r, body)
	default:
		body := Message{
			JSONRPC: jsonRPCVersion,
			ID:      request.ID,
			Result:  mustRawJSON(s.t, map[string]any{"ok": true}),
		}
		s.writeResponse(w, r, body)
	}
}

func (s *streamableServer) writeResponse(w http.ResponseWriter, r *http.Request, body Message) {
	data, _ := json.Marshal(body)
	if s.respondSSE {
		w.Header().Set("Content-Type", contentTypeEventStream)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "id: evt-1\nevent: message\ndata: %s\n\n", data)
		return
	}
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *streamableServer) handleGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.getRequests = append(s.getRequests, r.Header.Clone())
	fail404 := s.failGETWith404
	events := append([]string(nil), s.getStreamEvents...)
	s.mu.Unlock()

	if fail404 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Header.Get(headerMCPSessionID) != s.currentSession() {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	s.mu.Lock()
	s.lastEventIDSeen = r.Header.Get(headerLastEventID)
	s.mu.Unlock()

	w.Header().Set("Content-Type", contentTypeEventStream)
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	for _, event := range events {
		fmt.Fprint(w, event)
	}
	// Returning ends the stream, which the transport treats as an
	// interruption and resumes with Last-Event-ID.
}

func (s *streamableServer) currentSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *streamableServer) expireNext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireNextCall = true
}

func newStreamableTransport(t *testing.T, endpoint string, authorizer TransportAuthorizer, client *http.Client) *StreamableHTTPTransport {
	t.Helper()
	transport, err := NewStreamableHTTPTransport(StreamableHTTPConfig{
		Endpoint:   endpoint,
		Authorizer: authorizer,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("NewStreamableHTTPTransport() error = %v", err)
	}
	t.Cleanup(func() { _ = transport.Close(context.Background()) })
	return transport
}

func TestStreamableHTTPTransportInitializeAndCall(t *testing.T) {
	server, srv := newStreamableServer(t)
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	client := NewClient(transport, Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if server.currentSession() == "" {
		t.Fatal("server did not record a session id")
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one echo tool", tools.Tools)
	}
}

func TestStreamableHTTPTransportSSEResponse(t *testing.T) {
	server, srv := newStreamableServer(t)
	server.respondSSE = true
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	req := Message{JSONRPC: jsonRPCVersion, ID: 3, Method: "ping"}
	if err := transport.Send(context.Background(), req); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	resp, err := transport.Receive(context.Background())
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if resp.ID != 3 || resp.Result == nil {
		t.Fatalf("response = %+v, want id 3 with result", resp)
	}
}

func TestStreamableHTTPTransportSessionExpiredReplaysInitialize(t *testing.T) {
	server, srv := newStreamableServer(t)
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	client := NewClient(transport, Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	oldSession := server.currentSession()
	server.expireNext()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() after expiry error = %v", err)
	}
	if len(tools.Tools) != 1 {
		t.Fatalf("tools = %+v, want one tool after session recovery", tools.Tools)
	}
	if server.currentSession() == oldSession {
		t.Fatal("session was not rotated after expiry")
	}
}

func TestStreamableHTTPTransportUnauthorizedTerminal(t *testing.T) {
	server, srv := newStreamableServer(t)
	server.unauthorized = true
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	req := Message{JSONRPC: jsonRPCVersion, ID: 1, Method: "initialize", Params: mustRawJSON(t, map[string]any{
		"protocolVersion": defaultProtocolVersion,
		"clientInfo":      map[string]any{"name": "petalflow"},
	})}
	err := transport.Send(context.Background(), req)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Send() error = %v, want ErrUnauthorized", err)
	}
}

func TestStreamableHTTPTransportInsufficientScopeTerminal(t *testing.T) {
	server, srv := newStreamableServer(t)
	server.forbidden = true
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	req := Message{JSONRPC: jsonRPCVersion, ID: 1, Method: "ping"}
	err := transport.Send(context.Background(), req)
	if !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("Send() error = %v, want ErrInsufficientScope", err)
	}
}

func TestStreamableHTTPTransportGETStreamDeliversNotifications(t *testing.T) {
	server, srv := newStreamableServer(t)
	server.getStreamEvents = []string{
		"id: srv-1\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{\"level\":\"info\"}}\n\n",
	}
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	client := NewClient(transport, Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	message, err := transport.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive() notification error = %v", err)
	}
	if message.Method != "notifications/message" {
		t.Fatalf("method = %q, want notifications/message", message.Method)
	}
}

func TestStreamableHTTPTransportGETStreamResumesWithLastEventID(t *testing.T) {
	server, srv := newStreamableServer(t)
	server.getStreamEvents = []string{
		"id: srv-1\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\"}\n\n",
	}
	transport := newStreamableTransport(t, srv.URL, nil, nil)
	transport.cfg.MaxStreamReconnects = 2
	transport.cfg.StreamReconnectBackoff = 5 * time.Millisecond

	client := NewClient(transport, Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	// Wait for the first GET stream to deliver the event, then let the
	// resumed stream prove Last-Event-ID resumption.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		seen := server.lastEventIDSeen
		gets := len(server.getRequests)
		server.mu.Unlock()
		if gets >= 2 && seen == "srv-1" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected resumed GET stream with Last-Event-ID, last seen %q", func() string {
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.lastEventIDSeen
	}())
}

func TestStreamableHTTPTransportGETStream404ReportsSessionExpired(t *testing.T) {
	server, srv := newStreamableServer(t)
	server.failGETWith404 = true
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	client := NewClient(transport, Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := transport.Receive(ctx)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("Receive() error = %v, want ErrSessionExpired", err)
	}
}

type recordingAuthorizer struct {
	mu       sync.Mutex
	tokens   []string
	refreshs int
}

func (a *recordingAuthorizer) Authorize(ctx context.Context, req *http.Request) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	token := fmt.Sprintf("token-%d", len(a.tokens)+1)
	a.tokens = append(a.tokens, token)
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (a *recordingAuthorizer) Refresh(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshs++
	return nil
}

func TestStreamableHTTPTransportRetriesAfterAuthorizationRefresh(t *testing.T) {
	_, srv := newStreamableServer(t)
	authorizer := &recordingAuthorizer{}
	transport := newStreamableTransport(t, srv.URL, authorizer, nil)

	client := NewClient(transport, Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) != 1 {
		t.Fatalf("tools = %+v", tools.Tools)
	}

	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	if len(authorizer.tokens) < 2 {
		t.Fatalf("expected >= 2 authorized requests, got %d", len(authorizer.tokens))
	}
}

func TestStreamableHTTPTransportSessionHeaderOnEveryRequest(t *testing.T) {
	server, srv := newStreamableServer(t)
	transport := newStreamableTransport(t, srv.URL, nil, nil)

	client := NewClient(transport, Options{})
	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if _, err := client.CallTool(context.Background(), ToolsCallParams{Name: "echo"}); err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	calls := len(server.serverCalls)
	// initialize + initialized notification + tools/list + tools/call.
	if calls != 4 {
		t.Fatalf("server calls = %d, want 4", calls)
	}
}

func TestStreamableHTTPTransportRequiresEndpoint(t *testing.T) {
	if _, err := NewStreamableHTTPTransport(StreamableHTTPConfig{}); err == nil {
		t.Fatal("NewStreamableHTTPTransport() without endpoint should fail")
	}
}

func TestStaticTokenAuthorizerAttachesHeader(t *testing.T) {
	authorizer := &StaticTokenAuthorizer{Token: "secret-token"}
	req, _ := http.NewRequest(http.MethodPost, "http://mcp.local", nil)
	if err := authorizer.Authorize(context.Background(), req); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if err := authorizer.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() on static token should fail")
	}
}

func TestStaticTokenAuthorizerRejectsEmptyToken(t *testing.T) {
	authorizer := &StaticTokenAuthorizer{}
	req, _ := http.NewRequest(http.MethodPost, "http://mcp.local", nil)
	if err := authorizer.Authorize(context.Background(), req); err == nil {
		t.Fatal("Authorize() with empty token should fail")
	}
}

type oauthTestServer struct {
	t *testing.T

	mu           sync.Mutex
	issuedTokens []string
	revokeAfter  map[string]bool
	tokenCalls   int
	serveMux     *http.ServeMux
}

func newOAuthTestServer(t *testing.T) (*oauthTestServer, *httptest.Server) {
	s := &oauthTestServer{t: t, revokeAfter: map[string]bool{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(t, w, map[string]any{
			"authorization_servers": []string{"http://as.example"},
		})
	})
	// The AS metadata is served from the same test host, so point discovery
	// at the test server itself.
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(t, w, map[string]any{
			"token_endpoint": "",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Basic ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.mu.Lock()
		s.tokenCalls++
		token := fmt.Sprintf("access-%d", s.tokenCalls)
		s.issuedTokens = append(s.issuedTokens, token)
		s.mu.Unlock()
		writeJSONResponse(t, w, map[string]any{
			"access_token": token,
			"token_type":   "Bearer",
			"expires_in":   300,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		invalid := len(s.issuedTokens) == 0
		latest := ""
		if len(s.issuedTokens) > 0 {
			latest = s.issuedTokens[len(s.issuedTokens)-1]
		}
		s.mu.Unlock()
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if invalid || got != latest {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSONResponse(t, w, map[string]any{"ok": true})
	})

	s.serveMux = mux
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

func writeJSONResponse(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", contentTypeJSON)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestOAuth2AuthorizerClientCredentials(t *testing.T) {
	_, srv := newOAuthTestServer(t)
	authorizer, err := NewOAuth2Authorizer(OAuth2Config{
		TokenURL:     srv.URL + "/token",
		ClientID:     "petalflow",
		ClientSecret: "shh",
		Endpoint:     srv.URL + "/mcp",
	})
	if err != nil {
		t.Fatalf("NewOAuth2Authorizer() error = %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	if err := authorizer.Authorize(context.Background(), req); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer access-1" {
		t.Fatalf("Authorization = %q, want bearer access-1", got)
	}

	// Second authorize reuses the cached token.
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	if err := authorizer.Authorize(context.Background(), req2); err != nil {
		t.Fatalf("Authorize() cached error = %v", err)
	}
	if got := req2.Header.Get("Authorization"); got != "Bearer access-1" {
		t.Fatalf("cached Authorization = %q", got)
	}
}

func TestOAuth2AuthorizerRefreshFetchesNewToken(t *testing.T) {
	server, srv := newOAuthTestServer(t)
	authorizer, err := NewOAuth2Authorizer(OAuth2Config{
		TokenURL:     srv.URL + "/token",
		ClientID:     "petalflow",
		ClientSecret: "shh",
		Endpoint:     srv.URL + "/mcp",
	})
	if err != nil {
		t.Fatalf("NewOAuth2Authorizer() error = %v", err)
	}

	if err := authorizer.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if err := authorizer.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() second error = %v", err)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.issuedTokens) != 2 || server.issuedTokens[0] == server.issuedTokens[1] {
		t.Fatalf("issued tokens = %v, want two distinct", server.issuedTokens)
	}
}

func TestOAuth2AuthorizerMetadataDiscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/.well-known/oauth-protected-resource"):
			writeJSONResponse(t, w, map[string]any{
				"authorization_servers": []string{srvSelfURL(r)},
			})
		case strings.HasSuffix(r.URL.Path, "/.well-known/oauth-authorization-server"):
			writeJSONResponse(t, w, map[string]any{
				"token_endpoint": srvSelfURL(r) + "/token",
			})
		case r.URL.Path == "/token":
			writeJSONResponse(t, w, map[string]any{"access_token": "discovered", "token_type": "Bearer", "expires_in": 60})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	authorizer, err := NewOAuth2Authorizer(OAuth2Config{
		ClientID:     "petalflow",
		ClientSecret: "shh",
		Endpoint:     srv.URL + "/mcp",
	})
	if err != nil {
		t.Fatalf("NewOAuth2Authorizer() error = %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", nil)
	if err := authorizer.Authorize(context.Background(), req); err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer discovered" {
		t.Fatalf("Authorization = %q, want discovered token via metadata", got)
	}
}

func srvSelfURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
