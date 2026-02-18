package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
)

func (s *Server) handleWorkflowWebhook(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	triggerID := r.PathValue("trigger_id")

	rec, ok, err := s.store.Get(r.Context(), workflowID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("workflow %q not found", workflowID))
		return
	}
	if rec.Compiled == nil {
		writeError(w, http.StatusBadRequest, "NOT_COMPILED", "workflow has no compiled graph")
		return
	}

	triggerNode, ok := findNodeDef(rec.Compiled, triggerID)
	if !ok || triggerNode.Type != "webhook_trigger" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("webhook trigger %q not found", triggerID))
		return
	}

	triggerCfg, err := nodes.ParseWebhookTriggerConfig(triggerNode.Config)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "INVALID_WEBHOOK_TRIGGER", err.Error())
		return
	}
	if !methodAllowed(r.Method, triggerCfg.Methods) {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", fmt.Sprintf("method %q is not allowed", r.Method))
		return
	}

	if err := authorizeWebhookRequest(r, triggerCfg); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
		return
	}

	requestBody, err := decodeWebhookBody(r)
	if err != nil {
		if isMaxBytesError(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}

	requestPayload := normalizeWebhookRequestPayload(workflowID, triggerID, r, requestBody)

	compiled, err := cloneGraphDefinition(rec.Compiled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RUNTIME_ERROR", fmt.Sprintf("clone compiled graph: %v", err))
		return
	}
	compiled.Entry = triggerID

	runReq := RunRequest{
		Input: map[string]any{
			nodes.WebhookRequestEnvKey: requestPayload,
		},
	}
	if triggerCfg.Timeout > 0 {
		runReq.Options.Timeout = triggerCfg.Timeout.String()
	}

	plan, err := s.planWorkflowRunWithDefinition(r.Context(), workflowID, compiled, runReq)
	if err != nil {
		writeRunAPIError(w, err)
		return
	}

	resp, err := s.executeWorkflowRunSync(r.Context(), workflowID, plan, webhookRunMetadataDecorator(webhookRunMetadata{
		WorkflowID: workflowID,
		TriggerID:  triggerID,
		Method:     strings.ToUpper(r.Method),
	}))
	if err != nil {
		writeRunAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func cloneGraphDefinition(gd *graph.GraphDefinition) (*graph.GraphDefinition, error) {
	if gd == nil {
		return nil, fmt.Errorf("graph definition is nil")
	}
	data, err := json.Marshal(gd)
	if err != nil {
		return nil, err
	}
	var cloned graph.GraphDefinition
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func findNodeDef(gd *graph.GraphDefinition, nodeID string) (graph.NodeDef, bool) {
	if gd == nil {
		return graph.NodeDef{}, false
	}
	for _, node := range gd.Nodes {
		if node.ID == nodeID {
			return node, true
		}
	}
	return graph.NodeDef{}, false
}

func methodAllowed(method string, allowed []string) bool {
	upper := strings.ToUpper(strings.TrimSpace(method))
	for _, candidate := range allowed {
		if upper == strings.ToUpper(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func authorizeWebhookRequest(r *http.Request, cfg nodes.WebhookTriggerNodeConfig) error {
	switch cfg.Auth.Type {
	case nodes.WebhookAuthTypeNone:
		return nil
	case nodes.WebhookAuthTypeHeaderToken:
		expected, err := resolveWebhookAuthToken(cfg.Auth.Token)
		if err != nil {
			return err
		}
		provided := r.Header.Get(cfg.Auth.Header)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			return fmt.Errorf("invalid webhook token")
		}
		return nil
	default:
		return fmt.Errorf("unsupported auth type %q", cfg.Auth.Type)
	}
}

func resolveWebhookAuthToken(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", fmt.Errorf("configured webhook token is empty")
	}
	if strings.HasPrefix(token, "env:") {
		name := strings.TrimSpace(strings.TrimPrefix(token, "env:"))
		if name == "" {
			return "", fmt.Errorf("invalid env token reference")
		}
		value := strings.TrimSpace(strings.TrimSpace(getEnv(name)))
		if value == "" {
			return "", fmt.Errorf("webhook auth env var %q is empty", name)
		}
		return value, nil
	}
	return token, nil
}

func getEnv(key string) string {
	//nolint:gosec // environment lookup is expected for auth token resolution.
	return os.Getenv(key)
}

func decodeWebhookBody(r *http.Request) (any, error) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	if len(bodyBytes) == 0 {
		return nil, nil
	}

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "application/json") {
		var payload any
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			return nil, fmt.Errorf("invalid JSON body: %w", err)
		}
		return payload, nil
	}

	return string(bodyBytes), nil
}

func normalizeWebhookRequestPayload(workflowID string, triggerID string, r *http.Request, body any) map[string]any {
	query := make(map[string]any, len(r.URL.Query()))
	for key, values := range r.URL.Query() {
		copied := make([]string, len(values))
		copy(copied, values)
		query[key] = copied
	}

	headers := make(map[string]any, len(r.Header))
	for key, values := range r.Header {
		headers[strings.ToLower(key)] = strings.Join(values, ", ")
	}

	return map[string]any{
		"workflow_id": workflowID,
		"trigger_id":  triggerID,
		"method":      strings.ToUpper(r.Method),
		"path":        r.URL.Path,
		"query":       query,
		"headers":     headers,
		"remote_addr": r.RemoteAddr,
		"received_at": time.Now().UTC().Format(time.RFC3339Nano),
		"body":        body,
	}
}
