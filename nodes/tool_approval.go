package nodes

import (
	"fmt"
	"strings"
	"time"

	"github.com/petal-labs/petalflow/runtime"
)

// Tool approval decisions (mirrors runtime.ToolApprovalDecision values
// so pending-action responses use a portable wire shape).
const (
	ToolApprovalDecisionApproved = "approved"
	ToolApprovalDecisionRejected = "rejected"
)

// ToolApprovalPolicySource describes where an approval requirement came
// from.
const (
	ToolApprovalSourcePolicy   = "policy"
	ToolApprovalSourceGuardian = "guardian"
)

// ToolApprovalRequest is the durable request to approve one tool
// invocation. It is persisted as a run pending action and, once decided,
// as a run approval-record ledger entry that survives restarts.
type ToolApprovalRequest struct {
	ID string `json:"id"`

	// NodeID is the node that requested the approval.
	NodeID string `json:"node_id"`
	// RunID scopes the request to its durable run.
	RunID string `json:"run_id"`
	// WorkflowID is the owning workflow, if known.
	WorkflowID string `json:"workflow_id,omitempty"`
	// TenantID scopes exposure (copied into the ledger when known).
	TenantID string `json:"tenant_id,omitempty"`

	// ToolName is the registered tool name (base reference).
	ToolName string `json:"tool_name"`
	// Action is the invoked action, when known.
	Action string `json:"action,omitempty"`
	// Reference is the name as invoked (e.g. "tool.action").
	Reference string `json:"reference,omitempty"`
	// PolicySource records where the approval requirement came from
	// ("policy" for config-driven rules, "guardian" for graph-declared
	// checks).
	PolicySource string `json:"policy_source,omitempty"`
	// ApproverHint is an optional pointer for notification routing
	// ("who is asked to approve"); binding it to delivery systems is up
	// to the hosting deployment.
	ApproverHint string `json:"approver_hint,omitempty"`

	// CreatedAt records request creation.
	CreatedAt time.Time `json:"created_at"`
	// ExpiresAt is the fail-closed decision deadline; a zero value is
	// filled with the runtime bridge default.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// ToolApprovalResponse is the operator decision for a pending approval.
type ToolApprovalResponse struct {
	// Decision is "approved" or "rejected".
	Decision string `json:"decision"`
	// DecisionBy names the approving operator or role.
	DecisionBy string `json:"decided_by,omitempty"`
	// DecisionAt records when the decision was made.
	DecisionAt time.Time `json:"decision_at,omitempty"`
	// Note carries optional operator context.
	Note string `json:"note,omitempty"`
}

// Approved reports whether the response approves the request.
func (r ToolApprovalResponse) Approved() bool {
	return r.Decision == ToolApprovalDecisionApproved
}

// ToolApprovalPendingError pauses a run while an approval decision is
// outstanding. It implements runtime.PendingError so the sequential
// executor checkpoints and surfaces the pending action.
type ToolApprovalPendingError struct {
	Request *ToolApprovalRequest
}

func (e *ToolApprovalPendingError) Error() string {
	if e == nil || e.Request == nil {
		return "tool approval is pending"
	}
	return fmt.Sprintf("tool approval %q for %q is pending", e.Request.ID, e.Request.ToolName)
}

func (e *ToolApprovalPendingError) Pending() bool { return true }

// ErrToolApprovalRejected is returned when an operator rejected the
// pending approval. The pending invocation fails deterministically.
var ErrToolApprovalRejected = fmt.Errorf("tool approval rejected by operator")

// ErrToolApprovalExpired marks a request whose decision window passed:
// approvals fail closed.
var ErrToolApprovalExpired = fmt.Errorf("tool approval expired")

var _ runtime.PendingError = (*ToolApprovalPendingError)(nil)

// parseApprovalReference splits an action reference into the registered
// tool name and the invoked action ("tool.action" -> "tool", "action"),
// mirroring tool.ReferenceNames so approval records use consistent
// naming.
func parseApprovalReference(reference string) (base, action string) {
	base, action = reference, ""
	if idx := strings.LastIndexByte(reference, '.'); idx > 0 && strings.IndexByte(reference[:idx], '.') < 0 {
		base = reference[:idx]
		action = reference[idx+1:]
	}
	return base, action
}
