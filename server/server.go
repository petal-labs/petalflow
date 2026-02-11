package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/hydrate"
)

// ServerConfig configures a Server instance.
type ServerConfig struct {
	Store         WorkflowStore
	Providers     hydrate.ProviderMap
	ClientFactory hydrate.ClientFactory
	Bus           bus.EventBus
	EventStore    bus.EventStore
	CORSOrigin    string
	MaxBody       int64
	Logger        *slog.Logger
}

// Server is the PetalFlow HTTP API server.
type Server struct {
	store         WorkflowStore
	providers     hydrate.ProviderMap
	clientFactory hydrate.ClientFactory
	bus           bus.EventBus
	eventStore    bus.EventStore
	corsOrigin    string
	maxBody       int64
	logger        *slog.Logger

	settingsMu sync.RWMutex
	settings   AppSettings
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
		providers:     cfg.Providers,
		clientFactory: cfg.ClientFactory,
		bus:           cfg.Bus,
		eventStore:    cfg.EventStore,
		corsOrigin:    corsOrigin,
		maxBody:       maxBody,
		logger:        logger,
		settings:      defaultAppSettings(),
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
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/node-types", s.handleNodeTypes)
	mux.HandleFunc("GET /api/workflows", s.handleListWorkflows)
	mux.HandleFunc("POST /api/workflows", s.handleCreateWorkflow)
	mux.HandleFunc("POST /api/workflows/agent", s.handleCreateAgentWorkflow)
	mux.HandleFunc("POST /api/workflows/graph", s.handleCreateGraphWorkflow)
	mux.HandleFunc("GET /api/workflows/{id}", s.handleGetWorkflow)
	mux.HandleFunc("PUT /api/workflows/{id}", s.handleUpdateWorkflow)
	mux.HandleFunc("DELETE /api/workflows/{id}", s.handleDeleteWorkflow)
	mux.HandleFunc("POST /api/workflows/{id}/run", s.handleRunWorkflow)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{run_id}/events", s.handleRunEvents)
	mux.HandleFunc("GET /api/providers", s.handleListProviders)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handleUpdateSettings)
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

// --- App settings ---

// AppSettings stores user preferences (matches the UI's AppSettings type).
type AppSettings struct {
	OnboardingComplete bool            `json:"onboarding_complete"`
	OnboardingStep     int             `json:"onboarding_step,omitempty"`
	Preferences        UserPreferences `json:"preferences"`
}

// UserPreferences stores per-user UI/behavior settings.
type UserPreferences struct {
	DefaultWorkflowMode string `json:"default_workflow_mode,omitempty"`
	AutoSaveIntervalMs  int    `json:"auto_save_interval_ms,omitempty"`
	TracingDefault      *bool  `json:"tracing_default,omitempty"`
	Theme               string `json:"theme,omitempty"`
	SnapToGrid          *bool  `json:"snap_to_grid,omitempty"`
	ShowPortTypes       *bool  `json:"show_port_types,omitempty"`
	OutputFormat        string `json:"output_format,omitempty"`
}

func defaultAppSettings() AppSettings {
	return AppSettings{
		Preferences: UserPreferences{},
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	s.settingsMu.RLock()
	settings := s.settings
	s.settingsMu.RUnlock()
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var updated AppSettings
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}
	s.settingsMu.Lock()
	s.settings = updated
	s.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, updated)
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
