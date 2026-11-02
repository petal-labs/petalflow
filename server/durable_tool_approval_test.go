package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/tool"
	toolpkg "github.com/petal-labs/petalflow/tool"
)

// approvedFetchWorkflowPayload builds a single-node tool workflow whose
// http_fetch call is approval-gated (network class) and, once approved,
// hits a local target so the resumed invocation succeeds.
func approvedFetchWorkflowPayload(workflowID string, target string) map[string]any {
	return graphWorkflowPayload(workflowID, map[string]any{
		"id":   "approval_fetch",
		"type": "tool",
		"config": map[string]any{
			"tool_name":  "http_fetch.fetch",
			"output_key": "fetch_output",
			"static_args": map[string]any{
				"url":    target,
				"method": "GET",
			},
		},
	})
}

// newToolApprovalTestHandler builds a governed handler with a durable
// run store plus the network-class approval policy. The returned store
// is shared so a second handler can simulate a daemon restart on the
// same durable state.
func newToolApprovalTestHandler(t *testing.T) (http.Handler, runtime.RunStore, *tool.MemoryAuditStore) {
	t.Helper()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"isOk":true}`)
	}))
	t.Cleanup(target.Close)

	workflowStore := newTestSQLiteStore(t)
	runStore := runtime.NewMemoryRunStore()

	toolStore := newTestToolStore(t)
	if built, ok := tool.BuiltinRegistration("http_fetch"); ok {
		built.Config = map[string]string{"allow_private_network": "true"}
		if err := toolStore.Upsert(context.Background(), built); err != nil {
			t.Fatalf("seed http_fetch config: %v", err)
		}
	}

	auditStore := tool.NewMemoryAuditStore()
	workflowServer := NewServer(ServerConfig{
		Store: workflowStore, ScheduleStore: workflowStore,
		ToolStore: toolStore,
		Providers: hydrate.ProviderMap{},
		RunStore:  runStore, AuditStore: auditStore,
		DefaultTenantPolicy: &toolpkg.ToolPolicy{
			Allow:                  []string{"http_fetch", "template_render"},
			RequireApprovalClasses: []string{toolpkg.PolicyClassNetwork},
		},
	})
	t.Cleanup(func() { _ = workflowStore.Close() })

	createGraphWorkflow(t, workflowServer.Handler(), approvedFetchWorkflowPayload("tool_approval_wf", target.URL))
	return workflowServer.Handler(), runStore, auditStore
}

func approvalTestRun(t *testing.T, handler http.Handler) RunResponse {
	t.Helper()
	body := mustJSON(t, RunRequest{Input: map[string]any{"name": "x"}, Options: RunReqOptions{Timeout: "60s"}})
	req := httptest.NewRequest(http.MethodPost, "/api/workflows/tool_approval_wf/run", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("run status=%d body=%s, want 202 accepted (paused)", w.Code, w.Body.String())
	}
	var resp RunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal run response: %v", err)
	}
	if resp.RunID == "" || resp.Pending == nil {
		t.Fatalf("paused response = %#v", resp)
	}
	if resp.Pending.Type != runtime.PendingActionTypeToolApproval {
		t.Fatalf("pending type = %q, want %q", resp.Pending.Type, runtime.PendingActionTypeToolApproval)
	}
	return resp
}

func approvePending(t *testing.T, handler http.Handler, resp RunResponse, decision string, overrideID string) {
	t.Helper()
	actionID := resp.Pending.ID
	if overrideID != "" {
		actionID = overrideID
	}
	body := mustJSON(t, map[string]any{"response": map[string]any{
		"decision":   decision,
		"decided_by": "operator",
		"note":       "e2e",
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+resp.RunID+"/pending-actions/"+actionID, bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	want := http.StatusOK
	if decision == "" {
		want = http.StatusInternalServerError
	}
	if w.Code != want {
		t.Fatalf("%s pending action status=%d body=%s, want %d", decision, w.Code, w.Body.String(), want)
	}
}

func resumeRun(t *testing.T, handler http.Handler, resp RunResponse, wantStatus int) RunResponse {
	t.Helper()
	body := mustJSON(t, map[string]any{"resume_token": resp.ResumeToken})
	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+resp.RunID+"/resume", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if wantStatus >= 0 && w.Code != wantStatus {
		t.Fatalf("resume status=%d body=%s, want %d", w.Code, w.Body.String(), wantStatus)
	}
	var result RunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal resume response: %v", err)
	}
	return result
}

func TestDurableToolApproval_LifecycleAndRestartSurvival(t *testing.T) {
	handler, runStore, _ := newToolApprovalTestHandler(t)
	paused := approvalTestRun(t, handler)

	// Restart simulation: a fresh server over the same run store sees
	// the paused run and its approval request intact.
	inspect := NewServer(ServerConfig{RunStore: runStore}).Handler()
	statusBody := durableJSONRequest(t, inspect, http.MethodGet, "/api/runs/"+paused.RunID, nil)
	if statusBody.Code != http.StatusOK {
		t.Fatalf("restart inspect status=%d, want record to survive", statusBody.Code)
	}
	if !contains(statusBody.Body.String(), runtime.PendingActionTypeToolApproval) {
		t.Fatalf("restart run record lost the approval: %s", statusBody.Body.String())
	}

	// Approve exactly once; further completions are rejected.
	approvePending(t, handler, paused, nodes.ToolApprovalDecisionApproved, "")
	second := durableJSONRequest(t, handler, http.MethodPost, "/api/runs/"+paused.RunID+"/pending-actions/"+paused.Pending.ID, map[string]any{
		"response": map[string]any{"decision": "rejected"},
	})
	if second.Code != http.StatusConflict {
		t.Fatalf("second completion status=%d, want 409 ALREADY_COMPLETED", second.Code)
	}

	result := resumeRun(t, handler, paused, http.StatusOK)
	if result.Status != "completed" {
		t.Fatalf("approved resume = %#v, want completed", result)
	}

	record, err := runStore.Get(context.Background(), paused.RunID)
	if err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if len(record.ApprovalRecords) != 1 {
		t.Fatalf("ledger = %#v, want exactly one decision", record.ApprovalRecords)
	}
	entry := record.ApprovalRecords[0]
	if entry.Decision != runtime.ToolApprovalApproved || entry.DecisionBy != "operator" {
		t.Fatalf("ledger entry = %#v", entry)
	}

	// A second resume of the same approval cannot re-execute the tool:
	// the pending action is consumed and the run is settled.
	resumeRun(t, handler, paused, http.StatusConflict)
}

func TestDurableToolApproval_RejectionFailsClosed(t *testing.T) {
	handler, runStore, _ := newToolApprovalTestHandler(t)
	paused := approvalTestRun(t, handler)

	approvePending(t, handler, paused, nodes.ToolApprovalDecisionRejected, "")
	resumeRun(t, handler, paused, http.StatusInternalServerError)

	record, err := runStore.Get(context.Background(), paused.RunID)
	if err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if len(record.ApprovalRecords) != 1 {
		t.Fatalf("ledger = %#v, want one recorded rejection", record.ApprovalRecords)
	}
	if record.ApprovalRecords[0].Decision != runtime.ToolApprovalRejected {
		t.Fatalf("ledger decision = %q, want rejected", record.ApprovalRecords[0].Decision)
	}
}

func TestDurableToolApproval_ExpiredRequestFailsClosed(t *testing.T) {
	workflowStore := newTestSQLiteStore(t)
	runStore := runtime.NewMemoryRunStore()
	srv := NewServer(ServerConfig{
		Store: workflowStore, RunStore: runStore,
		Providers: hydrate.ProviderMap{},
	})
	t.Cleanup(func() { _ = workflowStore.Close() })

	request := nodes.ToolApprovalRequest{
		ID:           "appr-expired",
		NodeID:       "node-1",
		RunID:        "run-1",
		ToolName:     "email-send",
		Reference:    "email-send",
		PolicySource: nodes.ToolApprovalSourcePolicy,
		CreatedAt:    time.Now().UTC().Add(-48 * time.Hour),
		ExpiresAt:    time.Now().UTC().Add(-24 * time.Hour),
	}
	pending := &runtime.PendingAction{
		ID: request.ID, RunID: request.RunID, NodeID: request.NodeID,
		Type: runtime.PendingActionTypeToolApproval, Data: request,
		CreatedAt: request.CreatedAt,
	}
	record := &runtime.RunRecord{
		ID: "run-1", WorkflowID: "wf-1", Status: runtime.RunStatusPaused,
		StartedAt: request.CreatedAt, UpdatedAt: time.Now().UTC(),
		PendingAction: pending,
	}
	if err := runStore.Create(context.Background(), record); err != nil {
		t.Fatalf("seed record: %v", err)
	}

	bridge := srv.durableToolApprovalHandler()
	if _, err := bridge(context.Background(), &request); err == nil {
		t.Fatal("expired approval must fail closed")
	}

	updated, err := runStore.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("ledger read: %v", err)
	}
	if len(updated.ApprovalRecords) != 1 || updated.ApprovalRecords[0].Decision != runtime.ToolApprovalExpired {
		t.Fatalf("expired ledger = %#v, want one expired record", updated.ApprovalRecords)
	}
}

func TestToolApprovalGuardianGrantClaim(t *testing.T) {
	workflowStore := newTestSQLiteStore(t)
	runStore := runtime.NewMemoryRunStore()
	srv := NewServer(ServerConfig{
		Store: workflowStore, RunStore: runStore, Providers: hydrate.ProviderMap{},
	})
	t.Cleanup(func() { _ = workflowStore.Close() })
	verifier := srv.toolApprovalVerifier()

	seed := func(t *testing.T, runID, approvalID, toolName, decision string, executed bool) {
		t.Helper()
		record := &runtime.RunRecord{
			ID: runID, WorkflowID: "wf", Status: runtime.RunStatusRunning,
			StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		if err := runStore.Create(context.Background(), record); err != nil {
			t.Fatalf("seed run: %v", err)
		}
		if approvalID == "" {
			return
		}
		record, err := runStore.Get(context.Background(), runID)
		if err != nil {
			t.Fatalf("seed read: %v", err)
		}
		record.ApprovalRecords = append(record.ApprovalRecords, runtime.ApprovalRecord{
			ID: approvalID, RunID: runID, NodeID: "n", ToolName: toolName, Reference: toolName + ".act",
			Decision: runtime.ToolApprovalDecision(decision), DecisionBy: "operator",
			CreatedAt: time.Now().UTC(), Executed: executed, ExpiresAt: time.Now().UTC().Add(time.Hour),
		})
		if err := runStore.Update(context.Background(), record); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
	}

	// Happy path: approved, unconsumed grant for the expected tool.
	seed(t, "run-ok", "appr-ok", "http_fetch", "approved", false)
	if err := verifier(context.Background(), "run-ok", "appr-ok", "http_fetch"); err != nil {
		t.Fatalf("valid grant rejected: %v", err)
	}
	// The claim is single-use.
	if err := verifier(context.Background(), "run-ok", "appr-ok", "http_fetch"); err == nil {
		t.Fatal("grant must be consumed exactly once")
	}

	// Tool mismatch.
	seed(t, "run-mismatch", "appr-mismatch", "http_fetch", "approved", false)
	if err := verifier(context.Background(), "run-mismatch", "appr-mismatch", "email-send"); err == nil {
		t.Fatal("grant for another tool must not verify")
	}

	// Rejected grants never verify.
	seed(t, "run-rejected", "appr-rejected", "http_fetch", "rejected", false)
	if err := verifier(context.Background(), "run-rejected", "appr-rejected", "http_fetch"); err == nil {
		t.Fatal("rejected grants must not verify")
	}

	// Claim-by-tool with an empty approval ID picks the tool's approval.
	seed(t, "run-claimtool", "appr-claimtool", "http_fetch", "approved", false)
	if err := verifier(context.Background(), "run-claimtool", "", "http_fetch"); err != nil {
		t.Fatalf("claim by tool failed: %v", err)
	}
}

func TestApprovalLedgerStoreMergeAndReconcile(t *testing.T) {
	base := runtime.NewMemoryRunStore()
	store, ok := newApprovalLedgerStore(base)
	if !ok {
		t.Fatal("wrapper build must succeed for non-nil store")
	}
	ctx := context.Background()

	record := &runtime.RunRecord{
		ID: "ledger-run", WorkflowID: "wf", Status: runtime.RunStatusRunning,
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, record); err != nil {
		t.Fatalf("create: %v", err)
	}

	// The bridge records a ledger entry + clears the pending slot; a
	// stale runtime copy (no ledger, stale pending) must reconcile.
	stored, err := base.Get(ctx, "ledger-run")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	stored.ApprovalRecords = append(stored.ApprovalRecords, runtime.ApprovalRecord{
		ID: "appr-1", RunID: "ledger-run", ToolName: "email-send",
		Decision: runtime.ToolApprovalApproved, DecisionBy: "operator",
		CreatedAt: time.Now().UTC(),
	})
	stored.PendingAction = nil
	stored.UpdatedAt = time.Now().UTC()
	if err := base.Update(ctx, stored); err != nil {
		t.Fatalf("store decision: %v", err)
	}

	stale := *stored
	stale.ApprovalRecords = nil // runtime copy predates the ledger
	pending := &runtime.PendingAction{
		ID: "appr-1", RunID: "ledger-run", NodeID: "n",
		Type: runtime.PendingActionTypeToolApproval,
	}
	stale.PendingAction = pending
	if err := store.Update(ctx, &stale); err != nil {
		t.Fatalf("stale update: %v", err)
	}

	check, err := base.Get(ctx, "ledger-run")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(check.ApprovalRecords) != 1 || check.ApprovalRecords[0].ID != "appr-1" {
		t.Fatalf("ledger clobbered: %#v", check.ApprovalRecords)
	}
	if check.PendingAction != nil {
		t.Fatalf("pending slot resurrected: %#v", check.PendingAction)
	}

	// Late-recorded response reconciles to the runtime copy without
	// deleting it: after the bridge clears the pending, the wrapper
	// drops it. The storage semantics stay visible to callers via Get.
	nilEntry, _ := newApprovalLedgerStore(nil)
	if nilEntry != nil {
		t.Fatal("nil store wrapper must build nil")
	}
	if err := store.Cancel(ctx, "ledger-run"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

// failingGetRunStore fails Reads underneath the wrapper for the
// reconcile error paths.
type failingGetRunStore struct {
	runtime.RunStore
}

func (f *failingGetRunStore) Get(ctx context.Context, runID string) (*runtime.RunRecord, error) {
	return nil, fmt.Errorf("store outage")
}

func TestApprovalLedgerStoreErrorPaths(t *testing.T) {
	base := &failingGetRunStore{RunStore: runtime.NewMemoryRunStore()}
	store, ok := newApprovalLedgerStore(base)
	if !ok {
		t.Fatal("wrapper build must succeed")
	}
	ctx := context.Background()

	withLedger := &runtime.RunRecord{
		ID: "e1", Status: runtime.RunStatusRunning,
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		ApprovalRecords: []runtime.ApprovalRecord{{ID: "l", ToolName: "t", Decision: runtime.ToolApprovalApproved}},
		PendingAction:   &runtime.PendingAction{ID: "l", Type: runtime.PendingActionTypeToolApproval},
	}
	// Reads fail underneath; the delegating store itself returns the
	// store's failure for a record that was never stored.
	if err := store.Update(ctx, withLedger); err == nil {
		t.Fatal("failing store must surface its own error")
	}

	// Non-approval pending actions skip reconciliation untouched.
	fresh := runtime.NewMemoryRunStore()
	wrapper, _ := newApprovalLedgerStore(fresh)
	human := &runtime.RunRecord{
		ID: "e2", Status: runtime.RunStatusRunning,
		StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		PendingAction: &runtime.PendingAction{ID: "p", Type: "human"},
	}
	if err := fresh.Create(ctx, human); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := wrapper.Update(ctx, human); err != nil {
		t.Fatalf("update human pending: %v", err)
	}
	check, err := fresh.Get(ctx, "e2")
	if err != nil || check.PendingAction == nil || check.PendingAction.ID != "p" {
		t.Fatalf("human pending must survive wrapper writes: %#v err=%v", check.PendingAction, err)
	}
}
