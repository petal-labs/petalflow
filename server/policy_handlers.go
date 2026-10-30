package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/petal-labs/petalflow/tool"
)

// policyEnvelope is the shared request/response shape for policy PUT/GET
// bodies. Policy is nullable so PUT can both set and clear overrides.
type policyEnvelope struct {
	Policy *tool.ToolPolicy `json:"policy"`
}

// Sources reported by the policy endpoints.
const (
	policySourceStored      = "stored"  // per-tenant override in the store
	policySourceDefault     = "default" // daemon-configured default
	policySourceNone        = "none"    // nothing configured; default-deny
	workflowPolicySourceSet = "workflow"
)

// tenantPolicyResult is the response shape for the tenant policy endpoint.
type tenantPolicyResult struct {
	// Policy has no omitempty: a cleared override must respond with an
	// explicit "policy": null so clients never keep a stale policy.
	Policy  *tool.ToolPolicy `json:"policy"`
	Source  string           `json:"source"`
	Version int              `json:"version,omitempty"`
}

// workflowPolicyResult is the response shape for the workflow policy
// endpoint. Effective carries the resolved policy so callers can audit
// merge outcomes without re-implementing precedence.
type workflowPolicyResult struct {
	// Policy has no omitempty: a cleared override must respond with an
	// explicit "policy": null so clients never keep a stale policy.
	Policy    *tool.ToolPolicy         `json:"policy"`
	Source    string                   `json:"source"`
	Version   int                      `json:"version,omitempty"`
	Effective tool.EffectiveToolPolicy `json:"effective"`
}

// tenantPolicyStore returns the optional TenantPolicyStore.
func (s *Server) tenantPolicyStore() (TenantPolicyStore, bool) {
	store, ok := s.store.(TenantPolicyStore)
	return store, ok
}

// effectiveTenantPolicy returns the policy that applies to the tenant: a
// stored override wins over the statically configured default. It returns
// a nil policy when neither exists (secure default-deny applies at
// evaluation).
func (s *Server) effectiveTenantPolicy(ctx context.Context, tenantID string) (*tool.ToolPolicy, string, error) {
	if policyStore, ok := s.tenantPolicyStore(); ok {
		rec, found, err := policyStore.GetTenantPolicy(ctx, tenantID)
		switch {
		case err != nil:
			return nil, "", err
		case found:
			policy := rec.Policy
			return &policy, policySourceStored, nil
		}
	}
	if s.defaultTenantPolicy != nil {
		policy := *s.defaultTenantPolicy
		return &policy, policySourceDefault, nil
	}
	return nil, policySourceNone, nil
}

// handleGetTenantPolicy returns the effective tenant tool policy and
// where it came from.
func (s *Server) handleGetTenantPolicy(w http.ResponseWriter, r *http.Request) {
	policy, source, err := s.effectiveTenantPolicy(r.Context(), s.tenantID(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	result := tenantPolicyResult{Source: source}
	if policy != nil {
		result.Policy = policy
		result.Version = policy.Version
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePutTenantPolicy stores the per-tenant tool policy override.
// An override narrows the configured tenant default policy.
func (s *Server) handlePutTenantPolicy(w http.ResponseWriter, r *http.Request) {
	policyStore, ok := s.tenantPolicyStore()
	if !ok {
		writeError(w, http.StatusNotImplemented, "POLICY_STORE_UNAVAILABLE", "tenant policy store is not configured")
		return
	}

	policy, ok := decodePolicyEnvelope(w, r)
	if !ok {
		return
	}

	tenantID := s.tenantID(r)
	if policy == nil {
		if err := policyStore.DeleteTenantPolicy(r.Context(), tenantID); err != nil && !errors.Is(err, ErrTenantPolicyNotFound) {
			writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, tenantPolicyResult{Source: policySourceDefault})
		return
	}

	updated, err := policyStore.UpsertTenantPolicy(r.Context(), TenantPolicyRecord{
		TenantID: tenantID,
		Policy:   *policy,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error())
		return
	}
	stored := updated.Policy
	writeJSON(w, http.StatusOK, tenantPolicyResult{Policy: &stored, Source: policySourceStored, Version: stored.Version})
}

// handleGetWorkflowPolicy returns the stored workflow policy plus the
// resolved effective policy.
func (s *Server) handleGetWorkflowPolicy(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.policyWorkflow(w, r)
	if !ok {
		return
	}

	tenantPolicy, _, err := s.effectiveTenantPolicy(r.Context(), rec.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	source := policySourceNone
	if rec.Policy != nil {
		source = workflowPolicySourceSet
	}
	writeJSON(w, http.StatusOK, workflowPolicyResult{
		Policy:    rec.Policy,
		Source:    source,
		Effective: tool.ResolveEffectivePolicy(tenantPolicy, rec.Policy),
	})
}

// handlePutWorkflowPolicy stores or clears the workflow tool policy
// override.
func (s *Server) handlePutWorkflowPolicy(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.policyWorkflow(w, r)
	if !ok {
		return
	}

	policy, ok := decodePolicyEnvelope(w, r)
	if !ok {
		return
	}

	rec.Policy = policy
	fmt.Fprintln(os.Stderr, "PUT-workflow:", r.Method, r.URL.Path, "clear:", policy == nil)
	if err := s.store.Update(r.Context(), rec); err != nil {
		fmt.Fprintln(os.Stderr, "PUT policy ->", rec.ID, rec.Policy, "clear:", policy == nil, "update err:", err)
	}
	if post, _, _ := s.store.Get(context.Background(), rec.ID); post.ID != "" {
		fmt.Fprintln(os.Stderr, "post-read:", post.ID, post.Policy)
	}

	tenantPolicy, _, err := s.effectiveTenantPolicy(r.Context(), rec.TenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return
	}

	source := policySourceNone
	if rec.Policy != nil {
		source = workflowPolicySourceSet
	}
	writeJSON(w, http.StatusOK, workflowPolicyResult{
		Policy:    rec.Policy,
		Source:    source,
		Effective: tool.ResolveEffectivePolicy(tenantPolicy, rec.Policy),
	})
}

// policyWorkflow loads the workflow record and enforces tenant visibility.
func (s *Server) policyWorkflow(w http.ResponseWriter, r *http.Request) (WorkflowRecord, bool) {
	rec, ok, err := s.store.Get(r.Context(), r.PathValue("id"))
	fmt.Fprintln(os.Stderr, "GET-workflow:", r.Method, r.URL.Path, ok, rec.Policy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORE_ERROR", err.Error())
		return WorkflowRecord{}, false
	}
	if !ok || !s.ownsTenant(r, rec.TenantID) {
		writeHiddenResource(w)
		return WorkflowRecord{}, false
	}
	return rec, true
}

// decodePolicyEnvelope decodes a policy PUT body: {"policy": {...}} sets
// the policy and {"policy": null} clears it. A body without a policy
// field is an error so intent stays explicit.
func decodePolicyEnvelope(w http.ResponseWriter, r *http.Request) (*tool.ToolPolicy, bool) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesError(err) {
			writeError(w, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds size limit")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "READ_ERROR", err.Error())
		return nil, false
	}

	var envelope policyEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", err.Error())
		return nil, false
	}
	if !rawJSONHasField(body, "policy") {
		writeError(w, http.StatusBadRequest, "PARSE_ERROR", `policy target required: {"policy": {...} | null}`)
		return nil, false
	}
	if envelope.Policy == nil {
		return nil, true
	}
	if err := envelope.Policy.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "VALIDATION_ERROR", fmt.Sprintf("tool policy validation failed: %v", err))
		return nil, false
	}
	normalized := envelope.Policy.Normalized()
	return &normalized, true
}

// rawJSONHasField reports whether the raw JSON object contains the
// top-level field, so null-vs-absent stay distinguishable.
func rawJSONHasField(body []byte, field string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	_, ok := raw[field]
	return ok
}
