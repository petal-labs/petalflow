package server

import (
	"context"

	"github.com/petal-labs/petalflow/runtime"
)

// approvalLedgerStore wraps a RunStore so the tool-approval ledger
// survives updates written from a runtime worker's stale in-memory
// copy. The runtime mutates records frequently (checkpoints, node
// statuses); the durable approval bridge appends ledger entries between
// those updates. On every Update this wrapper re-reads the stored
// record and merges any ledger entries the caller's copy lost by ID
// (the engine never mutates ledger entries), then delegates.
//
// The wrapper also prevents resurrecting a consumed approval: when the
// stored record has no pending action but the runtime copy still
// carries one (the bridge cleared it mid-update), the pending slot is
// dropped instead of being written back.
type approvalLedgerStore struct {
	store runtime.RunStore
}

// newApprovalLedgerStore builds the wrapper; it returns false when the
// underlying store is nil so callers keep the original wiring.
func newApprovalLedgerStore(store runtime.RunStore) (*approvalLedgerStore, bool) {
	if store == nil {
		return nil, false
	}
	return &approvalLedgerStore{store: store}, true
}

// mergeApprovalLedger unions ledger entries by ID: entries recorded by
// the approval bridge that the caller's stale copy is missing are
// restored; the stored entry wins per ID.
func (w *approvalLedgerStore) mergeApprovalLedger(ctx context.Context, incoming *runtime.RunRecord) {
	if w == nil || incoming == nil {
		return
	}
	stored, err := w.store.Get(ctx, incoming.ID)
	if err != nil || stored == nil || len(stored.ApprovalRecords) == 0 {
		return
	}

	existing := make(map[string]struct{}, len(incoming.ApprovalRecords))
	for _, entry := range incoming.ApprovalRecords {
		existing[entry.ID] = struct{}{}
	}
	for _, entry := range stored.ApprovalRecords {
		if _, present := existing[entry.ID]; !present {
			incoming.ApprovalRecords = append(incoming.ApprovalRecords, entry)
			existing[entry.ID] = struct{}{}
		}
	}
}

// reconcileApprovalPending prevents resurrection of a consumed
// tool-approval pending action and reconciles late-recorded responses.
func (w *approvalLedgerStore) reconcileApprovalPending(ctx context.Context, incoming *runtime.RunRecord) {
	if w == nil || incoming == nil || incoming.PendingAction == nil {
		return
	}
	if incoming.PendingAction.Type != runtime.PendingActionTypeToolApproval {
		return
	}
	stored, err := w.store.Get(ctx, incoming.ID)
	if err != nil || stored == nil {
		return
	}
	if stored.PendingAction == nil {
		// The approval was consumed after the caller read its copy;
		// do not resurrect it.
		incoming.PendingAction = nil
		return
	}
	if stored.PendingAction.Response != nil && incoming.PendingAction.Response == nil {
		pending := *stored.PendingAction
		incoming.PendingAction = &pending
	}
}

func (w *approvalLedgerStore) Create(ctx context.Context, record *runtime.RunRecord) error {
	return w.store.Create(ctx, record)
}

func (w *approvalLedgerStore) Get(ctx context.Context, runID string) (*runtime.RunRecord, error) {
	return w.store.Get(ctx, runID)
}

func (w *approvalLedgerStore) Update(ctx context.Context, record *runtime.RunRecord) error {
	w.mergeApprovalLedger(ctx, record)
	w.reconcileApprovalPending(ctx, record)
	return w.store.Update(ctx, record)
}

func (w *approvalLedgerStore) Cancel(ctx context.Context, runID string) error {
	return w.store.Cancel(ctx, runID)
}

var _ runtime.RunStore = (*approvalLedgerStore)(nil)
