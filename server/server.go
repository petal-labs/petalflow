package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/petal-labs/petalflow/bus"
	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/security"
	"github.com/petal-labs/petalflow/tool"
)

// ServerConfig configures a Server instance.
type ServerConfig struct {
	Store         WorkflowStore
	ScheduleStore WorkflowScheduleStore
	ToolStore     tool.Store
	Providers     hydrate.ProviderMap
	ClientFactory hydrate.ClientFactory
	Bus           bus.EventBus
	EventStore    bus.EventStore
	RunStore      runtime.RunStore
	RuntimeEvents runtime.EventHandler
	EmitDecorator runtime.EventEmitterDecorator
	CORSOrigin    string
	MaxBody       int64
	Security      SecurityConfig
	Logger        *slog.Logger
}

// SecurityConfig controls authentication, CORS, and request concurrency.
// Authentication is opt-in for embedded library compatibility; network-facing
// deployments should set RequireAuth and provide an authenticator.
type SecurityConfig struct {
	RequireAuth           bool
	Authenticator         security.Authenticator
	CORSOrigins           []string
	MaxConcurrentRequests int
	MaxConcurrentRuns     int
	AllowPrivateNetwork   bool
	WebhookReplayWindow   time.Duration
}

// Server is the PetalFlow HTTP API server.
type Server struct {
	store         WorkflowStore
	scheduleStore WorkflowScheduleStore
	toolStore     tool.Store
	providers     hydrate.ProviderMap
	clientFactory hydrate.ClientFactory
	bus           bus.EventBus
	eventStore    bus.EventStore
	runStore      runtime.RunStore
	runtimeEvents runtime.EventHandler
	emitDecorator runtime.EventEmitterDecorator
	corsOrigin    string
	security      SecurityConfig
	requestSlots  chan struct{}
	runSlots      chan struct{}
	maxBody       int64
	logger        *slog.Logger
	activeMu      sync.Mutex
	activeRuns    map[string]context.CancelFunc
	pendingMu     sync.Mutex
	replayMu      sync.Mutex
	replayedHooks map[string]time.Time
}

// NewServer creates a new Server with the given configuration.
func NewServer(cfg ServerConfig) *Server {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	corsOrigin := strings.TrimSpace(cfg.CORSOrigin)
	maxBody := cfg.MaxBody
	if maxBody <= 0 {
		maxBody = 1 << 20 // 1 MB default
	}
	securityConfig := cfg.Security
	if len(securityConfig.CORSOrigins) == 0 && corsOrigin != "" {
		securityConfig.CORSOrigins = strings.Split(corsOrigin, ",")
	}
	if securityConfig.MaxConcurrentRequests <= 0 {
		securityConfig.MaxConcurrentRequests = 128
	}
	if securityConfig.MaxConcurrentRuns <= 0 {
		securityConfig.MaxConcurrentRuns = 32
	}
	return &Server{
		store:         cfg.Store,
		scheduleStore: cfg.ScheduleStore,
		toolStore:     cfg.ToolStore,
		providers:     cfg.Providers,
		clientFactory: cfg.ClientFactory,
		bus:           cfg.Bus,
		eventStore:    cfg.EventStore,
		runStore:      cfg.RunStore,
		runtimeEvents: cfg.RuntimeEvents,
		emitDecorator: cfg.EmitDecorator,
		corsOrigin:    corsOrigin,
		security:      securityConfig,
		requestSlots:  make(chan struct{}, securityConfig.MaxConcurrentRequests),
		runSlots:      make(chan struct{}, securityConfig.MaxConcurrentRuns),
		maxBody:       maxBody,
		logger:        logger,
		activeRuns:    make(map[string]context.CancelFunc),
		replayedHooks: make(map[string]time.Time),
	}
}

// Handler returns an http.Handler with all routes and middleware wired.
// This is a standalone handler suitable for use without the daemon server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	return s.Middleware(mux)
}

// Middleware applies the server's body, security, authentication, and request
// concurrency controls to a composed handler.
func (s *Server) Middleware(next http.Handler) http.Handler {
	handler := next
	handler = s.corsMiddleware(handler)
	handler = s.authMiddleware(handler)
	handler = s.requestLimitMiddleware(handler)
	handler = s.maxBodyMiddleware(handler)
	handler = securityHeadersMiddleware(handler)
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
	mux.HandleFunc("GET /api/runs/{run_id}", s.handleGetRun)
	mux.HandleFunc("POST /api/runs/{run_id}/cancel", s.handleCancelRun)
	mux.HandleFunc("POST /api/runs/{run_id}/resume", s.handleResumeRun)
	mux.HandleFunc("GET /api/runs/{run_id}/pending-actions", s.handleGetPendingAction)
	mux.HandleFunc("POST /api/runs/{run_id}/pending-actions/{action_id}", s.handleCompletePendingAction)
	mux.HandleFunc("/api/workflows/{id}/webhooks/{trigger_id}", s.handleWorkflowWebhook)
	mux.HandleFunc("GET /api/workflows/{id}/schedules", s.handleListWorkflowSchedules)
	mux.HandleFunc("POST /api/workflows/{id}/schedules", s.handleCreateWorkflowSchedule)
	mux.HandleFunc("GET /api/workflows/{id}/schedules/{schedule_id}", s.handleGetWorkflowSchedule)
	mux.HandleFunc("PUT /api/workflows/{id}/schedules/{schedule_id}", s.handleUpdateWorkflowSchedule)
	mux.HandleFunc("DELETE /api/workflows/{id}/schedules/{schedule_id}", s.handleDeleteWorkflowSchedule)
	mux.HandleFunc("GET /api/runs/{run_id}/events", s.handleRunEvents)
}

// --- Middleware ---

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && s.corsOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-PetalFlow-Tenant")
		} else if origin == "" && s.corsOrigin == "*" {
			// Preserve the legacy embedded-server response when the caller did
			// not send an Origin header. Browser requests still use the exact
			// allowlist path above.
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			if origin != "" && !s.corsOriginAllowed(origin) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsOriginAllowed(origin string) bool {
	for _, configured := range s.security.CORSOrigins {
		configured = strings.TrimSpace(configured)
		if configured == "*" || configured == origin {
			return true
		}
	}
	return false
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.security.RequireAuth || r.URL.Path == "/health" || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if s.security.Authenticator == nil {
			writeError(w, http.StatusServiceUnavailable, "AUTH_NOT_CONFIGURED", "authentication is not configured")
			return
		}
		identity, err := s.security.Authenticator(r)
		if err != nil || strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(identity.TenantID) == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="petalflow"`)
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(security.ContextWithIdentity(r.Context(), identity)))
	})
}

func (s *Server) requestLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.requestSlots <- struct{}{}:
			defer func() { <-s.requestSlots }()
		default:
			writeError(w, http.StatusTooManyRequests, "REQUEST_LIMIT", "too many concurrent requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
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
	message = publicErrorMessage(code, message)
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

func publicErrorMessage(code, message string) string {
	switch code {
	case "STORE_ERROR", "RUN_STORE_ERROR", "RUNTIME_ERROR", "TOOL_REGISTRY_ERROR", "HYDRATE_ERROR", "INTERNAL", "REGISTRY_SYNC_FAILED":
		return "internal server error"
	default:
		return message
	}
}
