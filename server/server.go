package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
	StateStore    ServerStateStore
	StatePath     string
}

// Server is the PetalFlow HTTP API server.
type Server struct {
	store         WorkflowStore
	providers     hydrate.ProviderMap
	providerMeta  map[string]providerMetadata
	clientFactory hydrate.ClientFactory
	bus           bus.EventBus
	eventStore    bus.EventStore
	corsOrigin    string
	maxBody       int64
	logger        *slog.Logger
	stateStore    ServerStateStore

	providersMu sync.RWMutex

	settingsMu sync.RWMutex
	settings   AppSettings

	authMu   sync.RWMutex
	authUser *authAccount      // nil = setup not done
	tokens   map[string]string // token → username
}

// ServerStateStore persists auth/settings/provider state.
type ServerStateStore interface {
	Load() (serverState, error)
	Save(serverState) error
}

type providerMetadata struct {
	DefaultModel   string
	OrganizationID string
	ProjectID      string
	Verified       bool
	LatencyMS      int64
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
	providers := make(hydrate.ProviderMap, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		providers[name] = pc
	}
	providerMeta := make(map[string]providerMetadata, len(providers))
	for name := range providers {
		providerMeta[name] = providerMetadata{}
	}
	stateStore := cfg.StateStore
	if stateStore == nil {
		statePath := strings.TrimSpace(cfg.StatePath)
		if statePath != "" {
			stateStore = NewFileStateStore(filepath.Clean(statePath))
		}
	}

	srv := &Server{
		store:         cfg.Store,
		providers:     providers,
		providerMeta:  providerMeta,
		clientFactory: cfg.ClientFactory,
		bus:           cfg.Bus,
		eventStore:    cfg.EventStore,
		corsOrigin:    corsOrigin,
		maxBody:       maxBody,
		logger:        logger,
		stateStore:    stateStore,
		settings:      defaultAppSettings(),
		tokens:        make(map[string]string),
	}
	if err := srv.loadState(); err != nil {
		srv.logger.Warn("server: load persistent state failed", "error", err)
	}
	return srv
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
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/setup", s.handleAuthSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("POST /api/auth/refresh", s.handleAuthRefresh)
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
	mux.HandleFunc("POST /api/providers", s.handleCreateProvider)
	mux.HandleFunc("PUT /api/providers/{name}", s.handleUpdateProvider)
	mux.HandleFunc("DELETE /api/providers/{name}", s.handleDeleteProvider)
	mux.HandleFunc("POST /api/providers/{name}/test", s.handleTestProvider)
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

func normalizeAppSettings(in AppSettings) AppSettings {
	out := defaultAppSettings()
	out.OnboardingComplete = in.OnboardingComplete
	out.OnboardingStep = in.OnboardingStep
	out.Preferences = in.Preferences
	return out
}

func cloneProviderMap(in hydrate.ProviderMap) hydrate.ProviderMap {
	if len(in) == 0 {
		return hydrate.ProviderMap{}
	}
	out := make(hydrate.ProviderMap, len(in))
	for name, cfg := range in {
		out[name] = cfg
	}
	return out
}

func cloneProviderMetaMap(in map[string]providerMetadata) map[string]providerMetadata {
	if len(in) == 0 {
		return map[string]providerMetadata{}
	}
	out := make(map[string]providerMetadata, len(in))
	for name, meta := range in {
		out[name] = meta
	}
	return out
}

func cloneAuthAccount(in *authAccount) *authAccount {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func (s *Server) loadState() error {
	if s.stateStore == nil {
		return nil
	}
	state, err := s.stateStore.Load()
	if err != nil {
		return err
	}
	s.authMu.Lock()
	s.authUser = cloneAuthAccount(state.AuthUser)
	s.authMu.Unlock()

	s.settingsMu.Lock()
	s.settings = normalizeAppSettings(state.Settings)
	s.settingsMu.Unlock()

	s.providersMu.Lock()
	if len(state.Providers) > 0 {
		s.providers = cloneProviderMap(state.Providers)
	}
	if len(state.ProviderMeta) > 0 {
		s.providerMeta = cloneProviderMetaMap(state.ProviderMeta)
	}
	if s.providerMeta == nil {
		s.providerMeta = map[string]providerMetadata{}
	}
	for name := range s.providers {
		if _, ok := s.providerMeta[name]; !ok {
			s.providerMeta[name] = providerMetadata{}
		}
	}
	s.providersMu.Unlock()
	return nil
}

func (s *Server) persistState() error {
	if s.stateStore == nil {
		return nil
	}

	s.authMu.RLock()
	authUser := cloneAuthAccount(s.authUser)
	s.authMu.RUnlock()

	s.settingsMu.RLock()
	settings := s.settings
	s.settingsMu.RUnlock()

	s.providersMu.RLock()
	providers := cloneProviderMap(s.providers)
	providerMeta := cloneProviderMetaMap(s.providerMeta)
	s.providersMu.RUnlock()

	return s.stateStore.Save(serverState{
		AuthUser:     authUser,
		Settings:     settings,
		Providers:    providers,
		ProviderMeta: providerMeta,
	})
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
	previous := s.settings
	s.settings = updated
	s.settingsMu.Unlock()
	if err := s.persistState(); err != nil {
		s.settingsMu.Lock()
		s.settings = previous
		s.settingsMu.Unlock()
		s.logger.Error("server: persist settings failed", "error", err)
		writeError(w, http.StatusInternalServerError, "PERSISTENCE_ERROR", "failed to persist settings")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- Auth ---

type authAccount struct {
	Username string `json:"username"`
	Password string `json:"password"` // plaintext for now (in-memory only)
}

type authStatusResponse struct {
	SetupComplete bool `json:"setup_complete"`
}

type authLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authSetupRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authTokensResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func generateToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, _ *http.Request) {
	s.authMu.RLock()
	done := s.authUser != nil
	s.authMu.RUnlock()
	writeJSON(w, http.StatusOK, authStatusResponse{SetupComplete: done})
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	s.authMu.RLock()
	alreadySetup := s.authUser != nil
	s.authMu.RUnlock()
	if alreadySetup {
		writeError(w, http.StatusConflict, "ALREADY_SETUP", "admin account already exists")
		return
	}

	var req authSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "username and password are required")
		return
	}

	user := &authAccount{Username: req.Username, Password: req.Password}
	s.authMu.Lock()
	if s.authUser != nil {
		s.authMu.Unlock()
		writeError(w, http.StatusConflict, "ALREADY_SETUP", "admin account already exists")
		return
	}
	s.authUser = user
	s.authMu.Unlock()
	if err := s.persistState(); err != nil {
		s.authMu.Lock()
		if s.authUser == user {
			s.authUser = nil
		}
		s.authMu.Unlock()
		s.logger.Error("server: persist auth setup failed", "error", err)
		writeError(w, http.StatusInternalServerError, "PERSISTENCE_ERROR", "failed to persist auth setup")
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req authLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	s.authMu.Lock()
	defer s.authMu.Unlock()

	if s.authUser == nil || req.Username != s.authUser.Username || req.Password != s.authUser.Password {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password")
		return
	}

	token := generateToken()
	refresh := generateToken()
	s.tokens[token] = req.Username
	s.tokens[refresh] = req.Username
	expiresIn := int((24 * time.Hour).Seconds())

	writeJSON(w, http.StatusOK, authTokensResponse{
		AccessToken:  token,
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAuthRefresh(w http.ResponseWriter, _ *http.Request) {
	// Issue a new token unconditionally (simplified).
	s.authMu.Lock()
	defer s.authMu.Unlock()

	if s.authUser == nil {
		writeError(w, http.StatusUnauthorized, "NO_SESSION", "no active session")
		return
	}

	token := generateToken()
	s.tokens[token] = s.authUser.Username
	expiresIn := int((24 * time.Hour).Seconds())

	writeJSON(w, http.StatusOK, authTokensResponse{
		AccessToken:  token,
		RefreshToken: generateToken(),
		ExpiresIn:    expiresIn,
	})
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
