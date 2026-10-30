package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petal-labs/petalflow/tool"
)

func TestWorkflowPolicyHandlers(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()
	mustCreateWorkflowForScheduleHandlers(t, handler, "policy-crud")

	// GET on a fresh workflow: no policy, default-deny effective policy.
	getReq := httptest.NewRequest(http.MethodGet, "/api/workflows/policy-crud/policy", nil)
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get policy status=%d, want %d body=%s", getW.Code, http.StatusOK, getW.Body.String())
	}
	var got workflowPolicyResult
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal get policy: %v", err)
	}
	if got.Policy != nil {
		t.Fatalf("fresh workflow policy = %+v, want nil", got.Policy)
	}
	if got.Source != policySourceNone {
		t.Fatalf("fresh workflow policy source = %q, want %q", got.Source, policySourceNone)
	}
	if got.Effective.Evaluate("email-send") != tool.PolicyDeny {
		t.Fatalf("effective policy without allowlist must deny")
	}

	// PUT sets a policy.
	putBody := mustJSON(t, policyEnvelope{Policy: &tool.ToolPolicy{
		Allow:           []string{"mcp/*", "email-send"},
		Deny:            []string{"email-delete"},
		RequireApproval: []string{"email-send"},
	}})
	putReq := httptest.NewRequest(http.MethodPut, "/api/workflows/policy-crud/policy", bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	handler.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Fatalf("put policy status=%d, want %d body=%s", putW.Code, http.StatusOK, putW.Body.String())
	}
	var put workflowPolicyResult
	if err := json.Unmarshal(putW.Body.Bytes(), &put); err != nil {
		t.Fatalf("unmarshal put policy: %v", err)
	}
	if put.Source != workflowPolicySourceSet {
		t.Fatalf("put policy source = %q, want %q", put.Source, workflowPolicySourceSet)
	}
	if put.Effective.Evaluate("mcp/grep") != tool.PolicyAllow {
		t.Fatalf("mcp tools should be allowed")
	}
	if put.Effective.Evaluate("email-delete") != tool.PolicyDeny {
		t.Fatalf("deny rule should win")
	}
	if put.Effective.Evaluate("email-send") != tool.PolicyNeedsApproval {
		t.Fatalf("approval rule should produce needs_approval")
	}

	// Persistence survives a new GET.
	getW = httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal persisted policy: %v", err)
	}
	if got.Policy == nil || len(got.Policy.Allow) == 0 {
		t.Fatalf("persisted policy missing allow list: %+v", got.Policy)
	}

	// PUT null clears the policy.
	clearBody := []byte(`{"policy": null}`)
	clearReq := httptest.NewRequest(http.MethodPut, "/api/workflows/policy-crud/policy", bytes.NewReader(clearBody))
	clearW := httptest.NewRecorder()
	handler.ServeHTTP(clearW, clearReq)
	if clearW.Code != http.StatusOK {
		t.Fatalf("clear policy status=%d, want %d body=%s", clearW.Code, http.StatusOK, clearW.Body.String())
	}

	getW = httptest.NewRecorder()
	handler.ServeHTTP(getW, getReq)
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal cleared policy: %v", err)
	}
	if got.Policy != nil {
		t.Fatalf("cleared workflow policy = %+v, want nil", got.Policy)
	}
}

func TestWorkflowPolicyHandlers_Validation(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()
	mustCreateWorkflowForScheduleHandlers(t, handler, "policy-validation")

	// Missing policy field is an explicit error.
	req := httptest.NewRequest(http.MethodPut, "/api/workflows/policy-validation/policy", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing policy field status=%d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}

	// Invalid rule values are rejected.
	invalidBody := mustJSON(t, policyEnvelope{Policy: &tool.ToolPolicy{Deny: []string{"fs read"}}})
	req = httptest.NewRequest(http.MethodPut, "/api/workflows/policy-validation/policy", bytes.NewReader(invalidBody))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid rule status=%d, want %d body=%s", w.Code, http.StatusUnprocessableEntity, w.Body.String())
	}

	// Workflow policy for unknown workflow stays hidden.
	missingReq := httptest.NewRequest(http.MethodGet, "/api/workflows/does-not-exist/policy", nil)
	missingW := httptest.NewRecorder()
	handler.ServeHTTP(missingW, missingReq)
	if missingW.Code != http.StatusNotFound {
		t.Fatalf("missing workflow status=%d, want %d body=%s", missingW.Code, http.StatusNotFound, missingW.Body.String())
	}
}

func TestWorkflowPolicyHandlers_ExplicitEmptyAllow(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()
	mustCreateWorkflowForScheduleHandlers(t, handler, "policy-empty")

	body := []byte(`{"policy": {"allow": []}}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/workflows/policy-empty/policy", bytes.NewReader(body))
	putW := httptest.NewRecorder()
	handler.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Fatalf("put empty allow status=%d, want %d body=%s", putW.Code, http.StatusOK, putW.Body.String())
	}

	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/api/workflows/policy-empty/policy", nil))
	if getW.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getW.Code, getW.Body.String())
	}
	var got workflowPolicyResult
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Policy == nil || got.Policy.Allow == nil {
		t.Fatalf("explicit empty allow did not survive: %+v", got.Policy)
	}
	if got.Effective.Evaluate("anything") != tool.PolicyDeny {
		t.Fatalf("explicit empty allow must deny everything")
	}
}

func TestTenantPolicyHandlers(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()
	srv.defaultTenantPolicy = &tool.ToolPolicy{Allow: []string{"web-search"}}

	// Default applies when no override exists.
	getW := httptest.NewRecorder()
	handler.ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/api/policies/tenant", nil))
	if getW.Code != http.StatusOK {
		t.Fatalf("get tenant policy status=%d body=%s", getW.Code, getW.Body.String())
	}
	var got tenantPolicyResult
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal tenant policy: %v", err)
	}
	if got.Source != policySourceDefault {
		t.Fatalf("tenant policy source = %q, want %q", got.Source, policySourceDefault)
	}
	if got.Policy == nil || got.Policy.Evaluate("web-search") != tool.PolicyAllow {
		t.Fatalf("tenant default policy not returned: %+v", got.Policy)
	}

	// Storing an override takes precedence.
	putBody := mustJSON(t, policyEnvelope{Policy: &tool.ToolPolicy{Allow: []string{"email-send"}}})
	putReq := httptest.NewRequest(http.MethodPut, "/api/policies/tenant", bytes.NewReader(putBody))
	putW := httptest.NewRecorder()
	handler.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusOK {
		t.Fatalf("put tenant policy status=%d body=%s", putW.Code, putW.Body.String())
	}
	var put tenantPolicyResult
	if err := json.Unmarshal(putW.Body.Bytes(), &put); err != nil {
		t.Fatalf("unmarshal put tenant policy: %v", err)
	}
	if put.Source != policySourceStored || put.Version != 1 {
		t.Fatalf("put tenant policy = %+v, want stored version 1", put)
	}

	getW = httptest.NewRecorder()
	handler.ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/api/policies/tenant", nil))
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal stored tenant policy: %v", err)
	}
	if got.Source != policySourceStored {
		t.Fatalf("tenant policy source = %q, want %q", got.Source, policySourceStored)
	}
	if got.Policy.Evaluate("web-search") != tool.PolicyDeny {
		t.Fatalf("override should narrow: web-search must deny")
	}

	// Clearing restores the configured default.
	clearReq := httptest.NewRequest(http.MethodPut, "/api/policies/tenant", bytes.NewReader([]byte(`{"policy": null}`)))
	clearW := httptest.NewRecorder()
	handler.ServeHTTP(clearW, clearReq)
	if clearW.Code != http.StatusOK {
		t.Fatalf("clear tenant policy status=%d body=%s", clearW.Code, clearW.Body.String())
	}
	getW = httptest.NewRecorder()
	handler.ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/api/policies/tenant", nil))
	if err := json.Unmarshal(getW.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal restored tenant policy: %v", err)
	}
	if got.Source != policySourceDefault {
		t.Fatalf("tenant policy source = %q, want %q", got.Source, policySourceDefault)
	}
}

func TestTenantPolicyHandlers_ValidationAndClearIdempotent(t *testing.T) {
	srv := testServer(t)
	handler := srv.Handler()

	clearReq := httptest.NewRequest(http.MethodPut, "/api/policies/tenant", bytes.NewReader([]byte(`{"policy": null}`)))
	clearW := httptest.NewRecorder()
	handler.ServeHTTP(clearW, clearReq)
	if clearW.Code != http.StatusOK {
		t.Fatalf("clear absent tenant policy status=%d body=%s", clearW.Code, clearW.Body.String())
	}

	invalidBody := mustJSON(t, policyEnvelope{Policy: &tool.ToolPolicy{Allow: []string{"zoom *"}}})
	putReq := httptest.NewRequest(http.MethodPut, "/api/policies/tenant", bytes.NewReader(invalidBody))
	putW := httptest.NewRecorder()
	handler.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid tenant policy status=%d body=%s", putW.Code, putW.Body.String())
	}
}
