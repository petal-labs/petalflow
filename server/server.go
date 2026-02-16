package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/tool"
)

// ServerConfig configures a Server instance.
type ServerConfig struct {
	Store         WorkflowStore
	ToolStore     tool.Store
	Providers     hydrate.ProviderMap
	ClientFactory hydrate.ClientFactory
	Bus           bus.EventBus
	EventStore    bus.EventStore
	RuntimeEvents runtime.EventHandler
	EmitDecorator runtime.EventEmitterDecorator
	CORSOrigin    string
	MaxBody       int64
	Logger        *slog.Logger
}

// Server is the PetalFlow HTTP API server.
type Server struct {
	store         WorkflowStore
	toolStore     tool.Store
	providers     hydrate.ProviderMap
	clientFactory hydrate.ClientFactory
	bus           bus.EventBus
	eventStore    bus.EventStore
	runtimeEvents runtime.EventHandler
	emitDecorator runtime.EventEmitterDecorator
	corsOrigin    string
	maxBody       int64
	logger        *slog.Logger
}

// NewServer creates a new Server with the given configuration.
func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	corsOrigin := cfg.CORSOrigin
	if corsOrigin == "" {
		corsOrigin = "*"
	}
	maxBody := cfg.MaxBody
	if maxBody <= 0 {
		maxBody = 1 << 20 // 1 MB default
	}
	return &Server{
		store:         cfg.Store,
		toolStore:     cfg.ToolStore,
		providers:     cfg.Providers,
		clientFactory: cfg.ClientFactory,
		bus:           cfg.Bus,
		eventStore:    cfg.EventStore,
		runtimeEvents: cfg.RuntimeEvents,
		emitDecorator: cfg.EmitDecorator,
		corsOrigin:    corsOrigin,
		maxBody:       maxBody,
		logger:        logger,
	}
}

// Handler returns an http.Handler with all routes and middleware wired.
// This is a standalone handler suitable for use without the daemon server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	var handler http.Handler = mux
	handler = s.corsMiddleware(handler)
	handler = s.maxBodyMiddleware(handler)

	return handler
}

// RegisterRoutes mounts workflow API routes onto an existing mux.
// Use this when composing with other handlers (e.g. daemon server).
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /api/node-types", s.handleNodeTypes)
	mux.HandleFunc("GET /api/workflows", s.handleListWorkflows)
	mux.HandleFunc("POST /api/workflows/agent", s.handleCreateAgentWorkflow)
	mux.HandleFunc("POST /api/workflows/graph", s.handleCreateGraphWorkflow)
	mux.HandleFunc("GET /api/workflows/{id}", s.handleGetWorkflow)
	mux.HandleFunc("PUT /api/workflows/{id}", s.handleUpdateWorkflow)
	mux.HandleFunc("DELETE /api/workflows/{id}", s.handleDeleteWorkflow)
	mux.HandleFunc("POST /api/workflows/{id}/run", s.handleRunWorkflow)
	mux.HandleFunc("GET /api/runs/{run_id}/events", s.handleRunEvents)
}

// --- Middleware ---

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) maxBodyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBody)
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// apiError is the standard error envelope per FRD.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string, details ...string) {
	body := apiError{
		Error: apiErrorBody{
			Code:    code,
			Message: message,
		},
	}
	if len(details) > 0 {
		body.Error.Details = details
	}
	writeJSON(w, status, body)
}
