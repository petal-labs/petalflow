package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/hydrate"
	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/tool"
)

func newApprovalBindingServer(t *testing.T) (*Server, runtime.RunStore) {
	t.Helper()
	workflowStore := newTestSQLiteStore(t)
	runStore := runtime.NewMemoryRunStore()
	srv := NewServer(ServerConfig{Store: workflowStore, RunStore: runStore, Providers: hydrate.ProviderMap{}})
	t.Cleanup(func() { _ = workflowStore.Close() })
	return srv, runStore
}

// seedDecidedApproval stores a paused run whose tool-approval pending
// action was already decided by an operator, as it is just before the
// run resumes and the tool node replays the approval.
func seedDecidedApproval(t *testing.T, runStore runtime.RunStore, runID string, request nodes.ToolApprovalRequest, decision string) {
	t.Helper()
	now := time.Now().UTC()
	request.RunID = runID
	request.CreatedAt = now
	request.ExpiresAt = now.Add(time.Hour)
	record := &runtime.RunRecord{
		ID: runID, WorkflowID: "wf", Status: runtime.RunStatusPaused,
		StartedAt: now, UpdatedAt: now,
		PendingAction: &runtime.PendingAction{
			ID: request.ID, RunID: runID, NodeID: request.NodeID,
			Type: runtime.PendingActionTypeToolApproval, Data: request,
			CreatedAt: now, Completed: true, RespondedAt: now,
			Response: nodes.ToolApprovalResponse{Decision: decision, DecisionBy: "operator", DecisionAt: now},
		},
	}
	if err := runStore.Create(context.Background(), record); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

// replayRequest is the request a resumed tool node rebuilds: a fresh ID
// and the digest of whatever arguments it built this time.
func replayRequest(reference string, args map[string]any) *nodes.ToolApprovalRequest {
	return &nodes.ToolApprovalRequest{
		ID: "rebuilt-id", RunID: "run-1", NodeID: "exec_node", ToolName: "computer",
		Reference: reference, ArgumentsDigest: tool.ArgumentsDigest(reference, args),
		PolicySource: nodes.ToolApprovalSourcePolicy, CreatedAt: time.Now().UTC(),
	}
}

func approvedRequest(args map[string]any) nodes.ToolApprovalRequest {
	return nodes.ToolApprovalRequest{
		ID: "appr-1", NodeID: "exec_node", ToolName: "computer", Action: "exec",
		Reference: "computer.exec", ArgumentsDigest: tool.ArgumentsDigest("computer.exec", args),
		PolicySource: nodes.ToolApprovalSourcePolicy,
	}
}

func TestDurableToolApproval_ResumeBindsApprovedInvocation(t *testing.T) {
	approvedArgs := map[string]any{"cmd": "ls"}

	cases := []struct {
		name      string
		reference string
		args      map[string]any
	}{
		{name: "different arguments", reference: "computer.exec", args: map[string]any{"cmd": "rm -rf /"}},
		{name: "different action", reference: "computer.write_file", args: approvedArgs},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, runStore := newApprovalBindingServer(t)
			seedDecidedApproval(t, runStore, "run-1", approvedRequest(approvedArgs), nodes.ToolApprovalDecisionApproved)

			_, err := srv.durableToolApprovalHandler()(context.Background(), replayRequest(tc.reference, tc.args))
			if !errors.Is(err, nodes.ErrToolApprovalMismatch) {
				t.Fatalf("mismatched replay error = %v, want ErrToolApprovalMismatch", err)
			}

			record, err := runStore.Get(context.Background(), "run-1")
			if err != nil {
				t.Fatalf("ledger read: %v", err)
			}
			if record.PendingAction != nil || len(record.ApprovalRecords) != 1 {
				t.Fatalf("record = pending %#v, ledger %#v; want consumed decision", record.PendingAction, record.ApprovalRecords)
			}
			entry := record.ApprovalRecords[0]
			if entry.Decision != runtime.ToolApprovalApproved || !entry.Executed {
				t.Fatalf("ledger entry = %#v, want the operator decision recorded and burned", entry)
			}
			// The burned approval cannot be spent afterwards, not even on
			// the invocation it was granted for.
			claim := runtime.ToolApprovalInvocation{
				RunID: "run-1", ApprovalID: "appr-1", ToolName: "computer",
				Reference: "computer.exec", ArgumentsDigest: tool.ArgumentsDigest("computer.exec", approvedArgs),
			}
			if err := srv.toolApprovalInvocationVerifier()(context.Background(), claim); err == nil {
				t.Fatal("burned approval must not be claimable")
			}
		})
	}

	t.Run("matching invocation", func(t *testing.T) {
		srv, runStore := newApprovalBindingServer(t)
		seedDecidedApproval(t, runStore, "run-1", approvedRequest(approvedArgs), nodes.ToolApprovalDecisionApproved)

		req := replayRequest("computer.exec", map[string]any{"cmd": "ls"})
		value, err := srv.durableToolApprovalHandler()(context.Background(), req)
		if err != nil {
			t.Fatalf("matching replay: %v", err)
		}
		if response, ok := value.(*nodes.ToolApprovalResponse); !ok || !response.Approved() {
			t.Fatalf("matching replay response = %#v", value)
		}
		if req.ID != "appr-1" {
			t.Fatalf("replayed request ID = %q, want the stored approval ID", req.ID)
		}
		record, err := runStore.Get(context.Background(), "run-1")
		if err != nil {
			t.Fatalf("ledger read: %v", err)
		}
		if len(record.ApprovalRecords) != 1 || record.ApprovalRecords[0].Executed ||
			record.ApprovalRecords[0].ArgumentsDigest != req.ArgumentsDigest {
			t.Fatalf("ledger = %#v, want one unclaimed entry bound to the digest", record.ApprovalRecords)
		}
	})

	t.Run("legacy request without digest", func(t *testing.T) {
		srv, runStore := newApprovalBindingServer(t)
		legacy := approvedRequest(approvedArgs)
		legacy.ArgumentsDigest = ""
		seedDecidedApproval(t, runStore, "run-1", legacy, nodes.ToolApprovalDecisionApproved)

		if _, err := srv.durableToolApprovalHandler()(context.Background(), replayRequest("computer.exec", approvedArgs)); err != nil {
			t.Fatalf("legacy replay with the same reference: %v", err)
		}
	})

	t.Run("rejected decision stays a rejection", func(t *testing.T) {
		srv, runStore := newApprovalBindingServer(t)
		seedDecidedApproval(t, runStore, "run-1", approvedRequest(approvedArgs), nodes.ToolApprovalDecisionRejected)

		value, err := srv.durableToolApprovalHandler()(context.Background(), replayRequest("computer.exec", map[string]any{"cmd": "rm"}))
		if err != nil {
			t.Fatalf("rejected replay error = %v, want the rejection response", err)
		}
		if response, ok := value.(*nodes.ToolApprovalResponse); !ok || response.Approved() {
			t.Fatalf("rejected replay response = %#v", value)
		}
	})
}

func TestToolApprovalInvocationVerifier(t *testing.T) {
	approvedArgs := map[string]any{"cmd": "ls"}
	digest := tool.ArgumentsDigest("computer.exec", approvedArgs)

	seed := func(t *testing.T, srv *Server, runStore runtime.RunStore, entryDigest string) runtime.ToolApprovalInvocationVerifier {
		t.Helper()
		now := time.Now().UTC()
		if err := runStore.Create(context.Background(), &runtime.RunRecord{
			ID: "run-1", WorkflowID: "wf", Status: runtime.RunStatusRunning, StartedAt: now, UpdatedAt: now,
			ApprovalRecords: []runtime.ApprovalRecord{{
				ID: "appr-1", RunID: "run-1", NodeID: "exec_node", ToolName: "computer", Action: "exec",
				Reference: "computer.exec", ArgumentsDigest: entryDigest,
				Decision: runtime.ToolApprovalApproved, DecisionBy: "operator",
				CreatedAt: now, ExpiresAt: now.Add(time.Hour),
			}},
		}); err != nil {
			t.Fatalf("seed run: %v", err)
		}
		return srv.toolApprovalInvocationVerifier()
	}
	claim := func(reference string, args map[string]any) runtime.ToolApprovalInvocation {
		return runtime.ToolApprovalInvocation{
			RunID: "run-1", ApprovalID: "appr-1", ToolName: "computer",
			Reference: reference, ArgumentsDigest: tool.ArgumentsDigest(reference, args),
		}
	}

	t.Run("exactly once", func(t *testing.T) {
		srv, runStore := newApprovalBindingServer(t)
		verify := seed(t, srv, runStore, digest)
		if err := verify(context.Background(), claim("computer.exec", approvedArgs)); err != nil {
			t.Fatalf("first claim: %v", err)
		}
		if err := verify(context.Background(), claim("computer.exec", approvedArgs)); err == nil {
			t.Fatal("second claim of the same approval must fail")
		}
	})

	mismatches := []struct {
		name  string
		claim runtime.ToolApprovalInvocation
	}{
		{name: "other arguments", claim: claim("computer.exec", map[string]any{"cmd": "rm"})},
		{name: "other action", claim: claim("computer.write_file", approvedArgs)},
	}
	for _, tc := range mismatches {
		t.Run(tc.name, func(t *testing.T) {
			srv, runStore := newApprovalBindingServer(t)
			verify := seed(t, srv, runStore, digest)
			if err := verify(context.Background(), tc.claim); !errors.Is(err, nodes.ErrToolApprovalMismatch) {
				t.Fatalf("mismatched claim error = %v, want ErrToolApprovalMismatch", err)
			}
			if err := verify(context.Background(), claim("computer.exec", approvedArgs)); err == nil {
				t.Fatal("a refused mismatch must burn the approval")
			}
		})
	}

	t.Run("legacy record matches by reference", func(t *testing.T) {
		srv, runStore := newApprovalBindingServer(t)
		verify := seed(t, srv, runStore, "")
		if err := verify(context.Background(), claim("computer.exec", map[string]any{"cmd": "anything"})); err != nil {
			t.Fatalf("legacy claim: %v", err)
		}
	})

	t.Run("requires an approval ID", func(t *testing.T) {
		srv, runStore := newApprovalBindingServer(t)
		verify := seed(t, srv, runStore, digest)
		noID := claim("computer.exec", approvedArgs)
		noID.ApprovalID = ""
		if err := verify(context.Background(), noID); err == nil {
			t.Fatal("claim without an approval ID must fail")
		}
	})
}

func TestApprovalLedgerStoreKeepsConsumptionMonotonic(t *testing.T) {
	base := runtime.NewMemoryRunStore()
	store, _ := newApprovalLedgerStore(base)
	now := time.Now().UTC()
	entry := runtime.ApprovalRecord{ID: "appr-1", RunID: "run-1", Decision: runtime.ToolApprovalApproved, CreatedAt: now}
	if err := base.Create(context.Background(), &runtime.RunRecord{
		ID: "run-1", Status: runtime.RunStatusRunning, StartedAt: now, UpdatedAt: now,
		ApprovalRecords: []runtime.ApprovalRecord{entry},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The runtime worker holds a copy taken before the approval was spent.
	stale, err := base.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("stale read: %v", err)
	}

	spent, _ := base.Get(context.Background(), "run-1")
	spent.ApprovalRecords[0].Executed = true
	if err := base.Update(context.Background(), spent); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := store.Update(context.Background(), stale); err != nil {
		t.Fatalf("stale update: %v", err)
	}
	record, err := base.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(record.ApprovalRecords) != 1 || !record.ApprovalRecords[0].Executed {
		t.Fatalf("ledger = %#v, want the spent approval to stay consumed", record.ApprovalRecords)
	}
}
