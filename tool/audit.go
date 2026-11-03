package tool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/petal-labs/petalflow/security"
)

// ToolAuditKind classifies one lifecycle observation in the tool audit
// trail.
type ToolAuditKind string

const (
	// ToolAuditDiscovery records a tool telling the runtime about its
	// capabilities: registration, MCP discovery/refresh, listing.
	ToolAuditDiscovery ToolAuditKind = "discovery"
	// ToolAuditInvocation records an invocation that succeeded.
	ToolAuditInvocation ToolAuditKind = "invocation"
	// ToolAuditApproval records a durable approval decision
	// (approved/rejected/expired).
	ToolAuditApproval ToolAuditKind = "approval"
	// ToolAuditRejection records a rejected invocation that never
	// reached the tool: policy denials, missing approval.
	ToolAuditRejection ToolAuditKind = "rejection"
	// ToolAuditFailure records an invocation attempt that failed at or
	// after dispatch: invalid payloads, budget limits, timeouts,
	// adapter failures.
	ToolAuditFailure ToolAuditKind = "failure"
)

// Stable machine reasons for audit decisions (values are safe: they
// never contain payload or secret material).
const (
	// ToolAuditAllowed marks an invocation admitted by the gate.
	ToolAuditAllowed = "allow"
	// ToolAuditDeniedPolicy marks a policy denial.
	ToolAuditDeniedPolicy = "deny_policy"
	// ToolAuditNeedsApproval marks an approval requirement.
	ToolAuditNeedsApproval = "needs_approval"
	// ToolAuditApproved marks an approved decision.
	ToolAuditApproved = "approved"
	// ToolAuditRejectedDecision marks an operator rejection.
	ToolAuditRejectedDecision = "rejected"
	// ToolAuditExpiredDecision marks an expired approval window.
	ToolAuditExpiredDecision = "expired"
	// ToolAuditInvalidPayload marks a schema-invalid payload.
	ToolAuditInvalidPayload = "invalid_payload"
	// ToolAuditPayloadTooLarge marks an oversized payload.
	ToolAuditPayloadTooLarge = "payload_too_large"
	// ToolAuditRateLimited marks a rate-budget rejection.
	ToolAuditRateLimited = "rate_limited"
	// ToolAuditConcurrencyLimited marks a concurrency-budget rejection.
	ToolAuditConcurrencyLimited = "concurrency_limited"
	// ToolAuditActionMissing marks an unknown action reference.
	ToolAuditActionMissing = "action_not_found"
	// ToolAuditTimeout marks a deadline overrun.
	ToolAuditTimeout = "timeout"
	// ToolAuditError marks a generic adapter failure.
	ToolAuditError = "error"
	// ToolAuditRegistered marks tool registration/discovery.
	ToolAuditRegistered = "registered"
	// ToolAuditRefreshed marks an MCP re-discovery.
	ToolAuditRefreshed = "refreshed"
	// ToolAuditListed marks registry listing (discovery by callers).
	ToolAuditListed = "listed"
	// ToolAuditDisabled marks a disabled tool.
	ToolAuditDisabled = "disabled"
	// ToolAuditNotFound marks a missing registration.
	ToolAuditNotFound = "not_found"
)

// ToolAuditRecord is one tenant-scoped lifecycle observation. Reasons
// are machine-readable codes plus field names, never payload or secret
// material (RedactAuditText strips secret-looking material as a
// backstop).
type ToolAuditRecord struct {
	ID string `json:"id"`
	// TenantID scopes access; empty means the shared default scope of
	// an unauthenticated deployment.
	TenantID string `json:"tenant_id"`
	// WorkflowID and RunID tie the record to its run (run audit trail);
	// NodeID names the graph node when known.
	WorkflowID string `json:"workflow_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	// Actor records who performed the action ("user:<subject>",
	// "operator:<name>", "workflow:<id>", "system").
	Actor string `json:"actor,omitempty"`
	// Tool and Action name the lifecycle subject.
	Tool   string `json:"tool"`
	Action string `json:"action,omitempty"`
	// Kind and Decision classify the observation.
	Kind     ToolAuditKind `json:"kind"`
	Decision string        `json:"decision"`
	// Reason is a machine-readable explanation (typed codes/fields).
	Reason string `json:"reason,omitempty"`
	// CreatedAt is the recording timestamp (UTC).
	CreatedAt time.Time `json:"created_at"`
	// PayloadSummary optionally carries a tiny redacted summary of the
	// invocation metadata (keys only, no values) for operators.
	PayloadSummary map[string]string `json:"payload_summary,omitempty"`
}

// ToolAuditFilter selects audit records for the query path.
type ToolAuditFilter struct {
	// TenantID matches exactly; empty means all tenants (operator scope
	// on unauthenticated deployments).
	TenantID   string
	WorkflowID string
	RunID      string
	Tool       string
	Kind       ToolAuditKind
	// Limit bounds the page; Cursor continues a previous page
	// (created_at|id order).
	Limit  int
	Cursor string
	// Before bounds Prune (and List) cutoffs; nil means unbounded.
	Before *time.Time
}

// ToolAuditStore persists tool lifecycle audit records for one backend.
// All List calls must scope results by tenant (records of other tenants
// are never visible).
type ToolAuditStore interface {
	// Append records one lifecycle observation.
	Append(ctx context.Context, record ToolAuditRecord) error
	// List returns a page of records matching the filter, ordered
	// oldest-first within the cursor guarantee. The returned cursor is
	// empty on the last page.
	List(ctx context.Context, filter ToolAuditFilter) ([]ToolAuditRecord, string, error)
	// Prune removes records created strictly before the cutoff and
	// returns the number removed. It honors the filter's tenant when
	// set.
	Prune(ctx context.Context, filter ToolAuditFilter, before time.Time) (int64, error)
}

// ToolAuditContext carries run/actor identity into audit records from
// context (set by the HTTP layers and runtime bridges).
type ToolAuditContext struct {
	Actor      string
	TenantID   string
	Tool       string
	RunID      string
	NodeID     string
	WorkflowID string
}

type toolAuditContextKey struct{}

// ContextWithAuditContext attaches audit identity to an invocation.
func ContextWithAuditContext(ctx context.Context, meta ToolAuditContext) context.Context {
	return context.WithValue(ctx, toolAuditContextKey{}, meta)
}

// AuditContextFromContext returns the attached audit identity, if any.
func AuditContextFromContext(ctx context.Context) (ToolAuditContext, bool) {
	meta, ok := ctx.Value(toolAuditContextKey{}).(ToolAuditContext)
	return meta, ok
}

// DefaultToolAuditReasonClassifies maps typed gate errors to stable
// audit reasons for the invocation path.
func DefaultToolAuditReason(err error) string {
	switch {
	case errors.Is(err, ErrToolPolicyDenied):
		return ToolAuditDeniedPolicy
	case IsApprovalRequired(err):
		return ToolAuditNeedsApproval
	case errors.Is(err, ErrToolRateLimited):
		return ToolAuditRateLimited
	case errors.Is(err, ErrToolConcurrencyLimit):
		return ToolAuditConcurrencyLimited
	case errors.Is(err, ErrInvalidPayload):
		return ToolAuditInvalidPayload
	case errors.Is(err, ErrPayloadTooLarge):
		return ToolAuditPayloadTooLarge
	case errors.Is(err, ErrToolDisabled):
		return ToolAuditDisabled
	case errors.Is(err, ErrToolNotFound):
		return ToolAuditNotFound
	case errors.Is(err, ErrActionNotFound):
		return ToolAuditActionMissing
	}
	return ToolAuditError
}

// RedactAuditText scrubs obvious secret material from free-form audit
// strings. It delegates to the canonical shared scrubber
// (security.ScrubSecrets).
func RedactAuditText(text string) string {
	return security.ScrubSecrets(text)
}

// RedactAuditRecord redacts reason fields on a record copy.
func RedactAuditRecord(record ToolAuditRecord) ToolAuditRecord {
	out := record
	out.Reason = RedactAuditText(out.Reason)
	if keys := out.PayloadSummary; len(keys) > 0 {
		summary := make(map[string]string, len(keys))
		for key, value := range keys {
			summary[RedactAuditText(key)] = RedactAuditText(value)
		}
		out.PayloadSummary = summary
	}
	return out
}

// AuditPayloadSummary renders a redacted key-only summary of an
// invocation payload for operators (never raw values; nested keys walk
// out flattened, capped at 16 keys and 64 chars each).
func AuditPayloadSummary(payload any) map[string]string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	summary := make(map[string]string, min(len(obj), 16))
	count := 0
	for _, key := range sortedGenericKeys(obj) {
		if count >= 16 {
			break
		}
		name := key
		if len(name) > 64 {
			name = name[:64]
		}
		// Key names only: never the values.
		typ := payloadValueTypeOf(obj[key])
		summary[RedactAuditText(name)] = RedactAuditText(typ)
		count++
	}
	if len(summary) == 0 {
		return nil
	}
	return summary
}

func payloadValueTypeOf(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// sortedGenericKeys returns map keys in deterministic order so audit
// summaries stay stable across replays.
func sortedGenericKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ToolAuditRecorder records audit events through a store, silently
// dropping failing writes so audit failures cannot break invocation
// flows (the store is always deployed within the same database as the
// registrations and the runtime remains functional on outages).
type ToolAuditRecorder struct {
	store ToolAuditStore
}

// NewToolAuditRecorder returns the recorder for a store; nil stores are
// no-ops so embedded use stays unhinged.
func NewToolAuditRecorder(store ToolAuditStore) *ToolAuditRecorder {
	if store == nil {
		return nil
	}
	return &ToolAuditRecorder{store: store}
}

// Store exposes the backing audit store for transport decisions
// (nil-safe).
func (r *ToolAuditRecorder) Store() ToolAuditStore {
	if r == nil {
		return nil
	}
	return r.store
}

// Record audits one lifecycle observation with context identity merged
// in. Tenant is always taken from the attached audit context when the
// caller did not supply one explicitly; redaction runs as a final
// backstop.
func (r *ToolAuditRecorder) Record(ctx context.Context, record ToolAuditRecord) {
	if r == nil || r.store == nil {
		return
	}
	if meta, ok := AuditContextFromContext(ctx); ok {
		if record.TenantID == "" {
			record.TenantID = meta.TenantID
		}
		if record.Actor == "" {
			record.Actor = meta.Actor
		}
		if record.RunID == "" {
			record.RunID = meta.RunID
			if record.RunID == "" && strings.HasPrefix(InvocationScopeFromContext(ctx), "run:") {
				record.RunID = strings.TrimPrefix(InvocationScopeFromContext(ctx), "run:")
			}
		}
		if record.NodeID == "" {
			record.NodeID = meta.NodeID
		}
		if record.WorkflowID == "" {
			record.WorkflowID = meta.WorkflowID
		}
	}
	if record.ID == "" {
		record.ID = newAuditRecordID()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	sanitized := RedactAuditRecord(record)
	if err := r.store.Append(ctx, sanitized); err != nil {
		_ = err // audit is best-effort by contract; never propagate into flows
	}
}

func newAuditRecordID() string {
	return uuid.NewString()
}

// MemoryAuditStore is the in-memory audit store for embedded runtimes
// and tests (durable deployments use the SQLite/Postgres stores).
type MemoryAuditStore struct {
	mu      sync.Mutex
	records []ToolAuditRecord
}

func NewMemoryAuditStore() *MemoryAuditStore {
	return &MemoryAuditStore{}
}

func (s *MemoryAuditStore) Append(_ context.Context, record ToolAuditRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func (s *MemoryAuditStore) List(_ context.Context, filter ToolAuditFilter) ([]ToolAuditRecord, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matched := make([]ToolAuditRecord, 0, len(s.records))
	for _, record := range s.records {
		if filter.TenantID != "" && record.TenantID != filter.TenantID {
			continue
		}
		if filter.WorkflowID != "" && record.WorkflowID != filter.WorkflowID {
			continue
		}
		if filter.RunID != "" && record.RunID != filter.RunID {
			continue
		}
		if filter.Tool != "" && record.Tool != filter.Tool {
			continue
		}
		if filter.Kind != "" && record.Kind != filter.Kind {
			continue
		}
		if filter.Before != nil && !record.CreatedAt.Before(*filter.Before) {
			continue
		}
		matched = append(matched, record)
	}
	next := ""
	if filter.Limit > 0 && len(matched) > filter.Limit {
		pos := 0
		if cursorAt, cursorID, valid := decodeToolAuditCursor(filter.Cursor); valid {
			for i, record := range matched {
				if record.CreatedAt.After(cursorAt) || (record.CreatedAt.Equal(cursorAt) && record.ID > cursorID) {
					pos = i
					break
				}
			}
		}
		matched = matched[pos:]
		if len(matched) > filter.Limit {
			matched = matched[:filter.Limit]
			last := matched[len(matched)-1]
			next = toolAuditCursor(last.CreatedAt, last.ID)
		}
	}
	return matched, next, nil
}

func (s *MemoryAuditStore) Prune(_ context.Context, filter ToolAuditFilter, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []ToolAuditRecord
	var removed int64
	for _, record := range s.records {
		if filter.TenantID == "" || filter.TenantID == record.TenantID {
			if record.CreatedAt.Before(before) {
				removed++
				continue
			}
		}
		kept = append(kept, record)
	}
	s.records = kept
	return removed, nil
}
