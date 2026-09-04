package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/graph"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/security"
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

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesError(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return
	}
	if err := s.authorizeWebhookRequest(r, triggerCfg, rawBody); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "webhook authentication failed")
		return
	}

	requestBody, err := decodeWebhookBody(rawBody, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", "invalid webhook body")
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
	plan.tenantID = rec.TenantID

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

func (s *Server) authorizeWebhookRequest(r *http.Request, cfg nodes.WebhookTriggerNodeConfig, body []byte) error {
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
	case nodes.WebhookAuthTypeHMACSHA256:
		secret, err := resolveWebhookAuthToken(cfg.Auth.Token)
		if err != nil {
			return err
		}
		timestamp, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get(cfg.Auth.TimestampHeader)), 10, 64)
		if err != nil {
			return fmt.Errorf("invalid webhook timestamp")
		}
		window := cfg.Auth.ReplayWindow
		if window <= 0 {
			window = s.security.WebhookReplayWindow
		}
		if window <= 0 {
			window = 5 * time.Minute
		}
		if delta := time.Since(time.Unix(timestamp, 0)); delta < -window || delta > window {
			return fmt.Errorf("webhook timestamp outside replay window")
		}
		provided := strings.TrimSpace(r.Header.Get(cfg.Auth.SignatureHeader))
		provided = strings.TrimPrefix(strings.TrimPrefix(provided, "sha256="), "SHA256=")
		providedBytes, err := hex.DecodeString(provided)
		if err != nil || len(providedBytes) != sha256.Size {
			return fmt.Errorf("invalid webhook signature")
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
		_, _ = mac.Write(body)
		if subtle.ConstantTimeCompare(providedBytes, mac.Sum(nil)) != 1 {
			return fmt.Errorf("invalid webhook signature")
		}
		replayKey := provided + ":" + strconv.FormatInt(timestamp, 10)
		s.replayMu.Lock()
		defer s.replayMu.Unlock()
		now := time.Now()
		for key, seenAt := range s.replayedHooks {
			if now.Sub(seenAt) > window {
				delete(s.replayedHooks, key)
			}
		}
		if _, exists := s.replayedHooks[replayKey]; exists {
			return fmt.Errorf("webhook request replayed")
		}
		s.replayedHooks[replayKey] = now
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

func decodeWebhookBody(bodyBytes []byte, r *http.Request) (any, error) {
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
		lowerKey := strings.ToLower(key)
		if lowerKey == "authorization" || lowerKey == "cookie" || lowerKey == "x-petalflow-webhook-token" {
			headers[lowerKey] = "[REDACTED]"
			continue
		}
		headers[lowerKey] = strings.Join(values, ", ")
	}

	payload := map[string]any{
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
	if identity, ok := security.IdentityFromContext(r.Context()); ok {
		payload["identity"] = map[string]any{
			"subject":   identity.Subject,
			"tenant_id": identity.TenantID,
		}
	}
	return payload
}
