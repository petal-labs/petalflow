package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/petal-labs/petalflow/registry"
	"github.com/petal-labs/petalflow/tool"
)

// ServerConfig controls daemon HTTP server dependencies.
type ServerConfig struct {
	Store    tool.Store
	Service  *tool.DaemonToolService
	Registry *registry.Registry
}

// Server exposes tool daemon APIs and node-type catalog endpoints.
type Server struct {
	service *tool.DaemonToolService
	reg     *registry.Registry

	mu           sync.Mutex
	dynamicTypes map[string]struct{}
}

// NewServer constructs a daemon API server with default in-memory storage.
func NewServer(cfg ServerConfig) (*Server, error) {
	reg := cfg.Registry
	if reg == nil {
		reg = registry.Global()
	}

	service := cfg.Service
	if service == nil {
		store := cfg.Store
		if store == nil {
			store = NewMemoryToolStore()
		}

		var err error
		service, err = tool.NewDaemonToolService(tool.DaemonToolServiceConfig{
			Store: store,
		})
		if err != nil {
			return nil, err
		}
	}

	s := &Server{
		service:      service,
		reg:          reg,
		dynamicTypes: make(map[string]struct{}),
	}
	if err := s.syncRegistry(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

// Service returns the backing daemon tool service.
func (s *Server) Service() *tool.DaemonToolService {
	return s.service
}

// SyncRegistry refreshes action-level node types from registered external tools.
func (s *Server) SyncRegistry(ctx context.Context) error {
	return s.syncRegistry(ctx)
}

// Handler returns an http.Handler exposing daemon APIs.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/tools", s.handleListTools)
	mux.HandleFunc("POST /api/tools", s.handleRegisterTool)
	mux.HandleFunc("GET /api/tools/{name}", s.handleGetTool)
	mux.HandleFunc("PUT /api/tools/{name}", s.handleUpdateTool)
	mux.HandleFunc("DELETE /api/tools/{name}", s.handleDeleteTool)

	mux.HandleFunc("PUT /api/tools/{name}/config", s.handleUpdateToolConfig)
	mux.HandleFunc("POST /api/tools/{name}/test", s.handleTestTool)
	mux.HandleFunc("GET /api/tools/{name}/health", s.handleToolHealth)
	mux.HandleFunc("POST /api/tools/{name}/refresh", s.handleRefreshTool)
	mux.HandleFunc("PUT /api/tools/{name}/overlay", s.handleOverlayTool)
	mux.HandleFunc("PUT /api/tools/{name}/disable", s.handleDisableTool)
	mux.HandleFunc("PUT /api/tools/{name}/enable", s.handleEnableTool)

	mux.HandleFunc("GET /api/node-types", s.handleNodeTypes)

	return mux
}

type registerToolRequest struct {
	Name        string             `json:"name"`
	Origin      string             `json:"origin,omitempty"`
	Type        string             `json:"type,omitempty"`
	Manifest    *tool.ToolManifest `json:"manifest,omitempty"`
	Config      map[string]string  `json:"config,omitempty"`
	Transport   *tool.MCPTransport `json:"transport,omitempty"`
	OverlayPath string             `json:"overlay_path,omitempty"`
	Enabled     *bool              `json:"enabled,omitempty"`
}

type updateToolRequest struct {
	Origin      *string            `json:"origin,omitempty"`
	Type        *string            `json:"type,omitempty"`
	Manifest    *tool.ToolManifest `json:"manifest,omitempty"`
	Config      map[string]string  `json:"config,omitempty"`
	Transport   *tool.MCPTransport `json:"transport,omitempty"`
	OverlayPath *string            `json:"overlay_path,omitempty"`
	Enabled     *bool              `json:"enabled,omitempty"`
}

type updateToolConfigRequest struct {
	Set       map[string]string `json:"set,omitempty"`
	SetSecret map[string]string `json:"set_secret,omitempty"`
}

type testToolRequest struct {
	Action string         `json:"action"`
	Inputs map[string]any `json:"inputs,omitempty"`
}

type overlayToolRequest struct {
	OverlayPath string `json:"overlay_path"`
	Path        string `json:"path,omitempty"`
}

type apiErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type apiErrorResponse struct {
	Error apiErrorDetail `json:"error"`
}

func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	filter := tool.ToolListFilter{}
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		filter.Status = tool.Status(status)
	}
	if raw, ok := queryParam(r, "enabled"); ok {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "INVALID_QUERY", "enabled must be a boolean", nil)
			return
		}
		filter.Enabled = &enabled
	}
	if raw, ok := queryParam(r, "include_builtins"); ok {
		includeBuiltins, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "INVALID_QUERY", "include_builtins must be a boolean", nil)
			return
		}
		filter.IncludeBuiltins = includeBuiltins
	}

	regs, err := s.service.List(r.Context(), filter)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tools": regs,
	})
}

func (s *Server) handleGetTool(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	includeBuiltins := true
	if raw, ok := queryParam(r, "include_builtins"); ok {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "INVALID_QUERY", "include_builtins must be a boolean", nil)
			return
		}
		includeBuiltins = parsed
	}

	reg, found, err := s.service.Get(r.Context(), name, includeBuiltins)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("tool %q not found", name), nil)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

func (s *Server) handleRegisterTool(w http.ResponseWriter, r *http.Request) {
	var req registerToolRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_JSON", err.Error(), nil)
		return
	}

	origin, err := parseOrigin(req.Origin, req.Type)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ORIGIN", err.Error(), nil)
		return
	}

	input := tool.RegisterToolInput{
		Name:         req.Name,
		Origin:       origin,
		Manifest:     req.Manifest,
		Config:       cloneStringMap(req.Config),
		MCPTransport: req.Transport,
		OverlayPath:  req.OverlayPath,
		Enabled:      req.Enabled,
	}

	registered, err := s.service.Register(r.Context(), input)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, registered)
}

func (s *Server) handleUpdateTool(w http.ResponseWriter, r *http.Request) {
	var req updateToolRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_JSON", err.Error(), nil)
		return
	}

	origin, err := parseOptionalOrigin(req.Origin, req.Type)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_ORIGIN", err.Error(), nil)
		return
	}

	input := tool.UpdateToolInput{
		Origin:       origin,
		Manifest:     req.Manifest,
		Config:       cloneStringMap(req.Config),
		MCPTransport: req.Transport,
		OverlayPath:  req.OverlayPath,
		Enabled:      req.Enabled,
	}
	updated, err := s.service.Update(r.Context(), r.PathValue("name"), input)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteTool(w http.ResponseWriter, r *http.Request) {
	if err := s.service.Delete(r.Context(), r.PathValue("name")); err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateToolConfig(w http.ResponseWriter, r *http.Request) {
	var req updateToolConfigRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_JSON", err.Error(), nil)
		return
	}

	updated, err := s.service.UpdateConfig(r.Context(), r.PathValue("name"), tool.ConfigUpdateInput{
		Set:       cloneStringMap(req.Set),
		SetSecret: cloneStringMap(req.SetSecret),
	})
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleTestTool(w http.ResponseWriter, r *http.Request) {
	var req testToolRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_JSON", err.Error(), nil)
		return
	}

	result, err := s.service.TestAction(r.Context(), r.PathValue("name"), req.Action, req.Inputs)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleToolHealth(w http.ResponseWriter, r *http.Request) {
	reg, report, err := s.service.Health(r.Context(), r.PathValue("name"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tool":   reg,
		"health": report,
	})
}

func (s *Server) handleRefreshTool(w http.ResponseWriter, r *http.Request) {
	reg, err := s.service.Refresh(r.Context(), r.PathValue("name"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

func (s *Server) handleOverlayTool(w http.ResponseWriter, r *http.Request) {
	var req overlayToolRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "INVALID_JSON", err.Error(), nil)
		return
	}

	path := strings.TrimSpace(req.OverlayPath)
	if path == "" {
		path = strings.TrimSpace(req.Path)
	}
	reg, err := s.service.UpdateOverlay(r.Context(), r.PathValue("name"), path)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

func (s *Server) handleDisableTool(w http.ResponseWriter, r *http.Request) {
	reg, err := s.service.SetEnabled(r.Context(), r.PathValue("name"), false)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

func (s *Server) handleEnableTool(w http.ResponseWriter, r *http.Request) {
	reg, err := s.service.SetEnabled(r.Context(), r.PathValue("name"), true)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, reg)
}

func (s *Server) handleNodeTypes(w http.ResponseWriter, r *http.Request) {
	if err := s.syncRegistry(r.Context()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "REGISTRY_SYNC_FAILED", err.Error(), nil)
		return
	}

	all := s.reg.All()
	if category := strings.TrimSpace(r.URL.Query().Get("category")); category != "" {
		filtered := make([]registry.NodeTypeDef, 0, len(all))
		for _, def := range all {
			if def.Category == category {
				filtered = append(filtered, def)
			}
		}
		all = filtered
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"node_types": all,
	})
}

func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	var validationErr *tool.RegistrationValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSONError(w, http.StatusBadRequest, validationErr.Code, validationErr.Message, validationErr.Details)
	case errors.Is(err, tool.ErrToolNotFound):
		writeJSONError(w, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, tool.ErrToolNotMCP):
		writeJSONError(w, http.StatusBadRequest, "NOT_MCP", err.Error(), nil)
	case errors.Is(err, tool.ErrToolDisabled):
		writeJSONError(w, http.StatusConflict, "TOOL_DISABLED", err.Error(), nil)
	default:
		writeJSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
	}
}

func (s *Server) syncRegistry(ctx context.Context) error {
	regs, err := s.service.List(ctx, tool.ToolListFilter{IncludeBuiltins: false})
	if err != nil {
		return err
	}

	nodeDefs := buildToolNodeTypes(regs)
	newDynamic := make(map[string]struct{}, len(nodeDefs))

	s.mu.Lock()
	defer s.mu.Unlock()

	for typeName := range s.dynamicTypes {
		s.reg.Delete(typeName)
	}
	for _, def := range nodeDefs {
		s.reg.Register(def)
		newDynamic[def.Type] = struct{}{}
	}
	s.dynamicTypes = newDynamic
	return nil
}

func buildToolNodeTypes(regs []tool.ToolRegistration) []registry.NodeTypeDef {
	sortedRegs := make([]tool.ToolRegistration, len(regs))
	copy(sortedRegs, regs)
	sort.Slice(sortedRegs, func(i, j int) bool {
		return sortedRegs[i].Name < sortedRegs[j].Name
	})

	out := make([]registry.NodeTypeDef, 0)
	for _, reg := range sortedRegs {
		actionNames := reg.ActionNames()
		for _, actionName := range actionNames {
			action := reg.Manifest.Actions[actionName]
			ref := toolActionReference(reg.Name, actionName)
			out = append(out, registry.NodeTypeDef{
				Type:        ref,
				Category:    "tool",
				DisplayName: ref,
				Description: action.Description,
				Ports: registry.PortSchema{
					Inputs:  fieldSpecsToPorts(action.Inputs, true),
					Outputs: fieldSpecsToPorts(action.Outputs, false),
				},
				ConfigSchema: map[string]any{
					"tool_name":   reg.Name,
					"action_name": actionName,
					"origin":      reg.Origin,
					"status":      reg.Status,
					"enabled":     reg.Enabled,
					"tool_config": cloneFieldMap(reg.Manifest.Config),
					"inputs":      cloneFieldMap(action.Inputs),
					"outputs":     cloneFieldMap(action.Outputs),
				},
				IsTool:   true,
				ToolMode: classifyToolActionMode(action),
			})
		}
	}
	return out
}

func fieldSpecsToPorts(fields map[string]tool.FieldSpec, includeRequired bool) []registry.PortDef {
	if len(fields) == 0 {
		return nil
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	ports := make([]registry.PortDef, 0, len(names))
	for _, name := range names {
		spec := fields[name]
		port := registry.PortDef{
			Name: name,
			Type: spec.Type,
		}
		if includeRequired {
			port.Required = spec.Required
		}
		ports = append(ports, port)
	}
	return ports
}

func classifyToolActionMode(action tool.ActionSpec) string {
	if action.LLMCallable != nil {
		if *action.LLMCallable {
			return "function_call"
		}
		return "standalone"
	}
	if hasBytesSchema(action.Inputs) || hasBytesSchema(action.Outputs) {
		return "standalone"
	}
	return "function_call"
}

func hasBytesSchema(fields map[string]tool.FieldSpec) bool {
	for _, field := range fields {
		if field.Type == tool.TypeBytes {
			return true
		}
		if field.Items != nil {
			if hasBytesSchema(map[string]tool.FieldSpec{"items": *field.Items}) {
				return true
			}
		}
		if len(field.Properties) > 0 {
			if hasBytesSchema(field.Properties) {
				return true
			}
		}
	}
	return false
}

func toolActionReference(toolName, actionName string) string {
	return strings.TrimSpace(toolName) + "." + strings.TrimSpace(actionName)
}

func parseOrigin(originValue, typeValue string) (tool.ToolOrigin, error) {
	origin := strings.TrimSpace(originValue)
	declType := strings.TrimSpace(typeValue)

	switch {
	case origin == "" && declType == "":
		return "", nil
	case origin == "":
		origin = declType
	case declType == "":
		declType = origin
	case !strings.EqualFold(origin, declType):
		return "", fmt.Errorf("origin %q does not match type %q", origin, declType)
	}

	switch strings.ToLower(origin) {
	case "native":
		return tool.OriginNative, nil
	case "mcp":
		return tool.OriginMCP, nil
	case "http":
		return tool.OriginHTTP, nil
	case "stdio":
		return tool.OriginStdio, nil
	default:
		return "", fmt.Errorf("unsupported origin/type %q", origin)
	}
}

func parseOptionalOrigin(originValue, typeValue *string) (*tool.ToolOrigin, error) {
	var originText string
	if originValue != nil {
		originText = *originValue
	}
	var typeText string
	if typeValue != nil {
		typeText = *typeValue
	}
	origin, err := parseOrigin(originText, typeText)
	if err != nil {
		return nil, err
	}
	if origin == "" {
		return nil, nil
	}
	value := origin
	return &value, nil
}

func queryParam(r *http.Request, key string) (string, bool) {
	values, ok := r.URL.Query()[key]
	if !ok || len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func decodeJSONBody(r *http.Request, target any) error {
	if target == nil {
		return errors.New("decode target is nil")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, apiErrorResponse{
		Error: apiErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}
