package tool

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditRecorderMergeContextAndRedact(t *testing.T) {
	store := NewMemoryAuditStore()
	recorder := NewToolAuditRecorder(store)
	if recorder.Store() == nil {
		t.Fatalf("Store under recorder must remain available")
	}

	ctx := ContextWithAuditContext(context.Background(), ToolAuditContext{
		Actor:      "user:api",
		TenantID:   "tenant-a",
		WorkflowID: "wf-1",
		RunID:      "run-1",
		NodeID:     "node-1",
	})

	recorder.Record(ctx, ToolAuditRecord{
		ID: "r1", Tool: "email-send", Kind: ToolAuditInvocation,
		Decision: ToolAuditAllowed,
		PayloadSummary: map[string]string{
			"api-key": "value-in", "url": "h",
		},
	})

	records, _, err := store.List(ctx, ToolAuditFilter{TenantID: "tenant-a", Cursor: ""})
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %v err = %v", records, err)
	}
	record := records[0]
	if record.TenantID != "tenant-a" || record.Actor != "user:api" || record.WorkflowID != "wf-1" || record.RunID != "run-1" || record.NodeID != "node-1" {
		t.Fatalf("context merge missing: %+v", record)
	}
	if record.PayloadSummary == nil {
		t.Fatal("payload summary should be redacted but present")
	}
	if _, ok := record.PayloadSummary["api-key"]; !ok {
		t.Fatalf("payload summary should keep keys: %+v", record.PayloadSummary)
	}
	if record.ID == "" {
		t.Fatal("record gets a default id")
	}
}

func TestAuditRedaction(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "bearer", in: "header: Bearer abcdefghijklmno", want: "header: **********"},
		{name: "openai key", in: "sk-proj1234567890abcd", want: "**********"},
		{name: "github token", in: "ghp_1234567890abcdefghijklmn", want: "**********"},
		{name: "password", in: "password: hunter2", want: "password: **********"},
		{name: "clean", in: "required input \"q\" is missing", want: `required input "q" is missing`},
		{name: "payload value", in: "value with no secrets", want: "value with no secrets"},
		{name: "key", in: "api_key=sk-proj12345678", want: "api_key=**********"},
		{name: "empty", in: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactAuditText(tc.in); got != tc.want {
				t.Fatalf("redact = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuditPayloadSummaryShapes(t *testing.T) {
	summary := AuditPayloadSummary(map[string]any{
		"email": "ada@example.invalid",
		"count": 4,
		"meta":  map[string]any{"deep": true},
	})
	if len(summary) != 3 {
		t.Fatalf("summary = %+v, want 3 keys", summary)
	}
	for key, value := range summary {
		if strings.Contains(key, "@") || strings.Contains(value, "@") {
			t.Fatalf("summary must never carry values: %q -> %q", key, value)
		}
	}
	if summary["email"] != "string" || summary["count"] != "number" || summary["meta"] != "object" {
		t.Fatalf("value kinds = %+v", summary)
	}
}

func TestAuditStoreTenantIsolationAndPagination(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "audit.db")
	store, err := NewSQLiteAuditStore(SQLiteStoreConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("NewSQLiteAuditStore: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()

	seed := func(id, tenant string, toolIndex int) ToolAuditRecord {
		created := now.Add(time.Duration(toolIndex) * time.Second)
		return ToolAuditRecord{
			ID: id, TenantID: tenant, Tool: "http_fetch", Kind: ToolAuditInvocation,
			Decision: ToolAuditAllowed, CreatedAt: created,
		}
	}
	records := []ToolAuditRecord{
		seed("t-a-1", "tenant-a", 0),
		seed("t-a-2", "tenant-a", 1),
		seed("t-b-1", "tenant-b", 2),
		seed("t-a-3", "tenant-a", 3),
	}
	for _, record := range records {
		if err := store.Append(ctx, record); err != nil {
			t.Fatalf("Append %s: %v", record.ID, err)
		}
	}

	// Cross-tenant reads are impossible by construction.
	if records, _, err := store.List(ctx, ToolAuditFilter{TenantID: "tenant-a", Limit: 100, Cursor: ""}); err != nil || len(records) != 3 {
		t.Fatalf("tenant-a got %d records err %v, want 3", len(records), err)
	}
	if records, _, err := store.List(ctx, ToolAuditFilter{TenantID: "tenant-b", Limit: 100, Cursor: ""}); err != nil || len(records) != 1 {
		t.Fatalf("tenant-b got %d records err %v, want 1", len(records), err)
	}

	// Pagination stays deterministic and duplicate-free.
	first, firstNext, err := store.List(ctx, ToolAuditFilter{TenantID: "tenant-a", Limit: 2, Cursor: ""})
	if err != nil || len(first) != 2 || firstNext == "" {
		t.Fatalf("first page = %v next %q err %v", first, firstNext, err)
	}
	second, secondNext, err := store.List(ctx, ToolAuditFilter{TenantID: "tenant-a", Limit: 2, Cursor: firstNext})
	if err != nil || len(second) != 1 || secondNext != "" {
		t.Fatalf("second page = %v next %q err %v", second, secondNext, err)
	}
	if second[0].ID != "t-a-3" {
		t.Fatalf("second page = %v, want only the last record", second)
	}

	// Prune is scoped and returns the removed count.
	removed, err := store.Prune(ctx, ToolAuditFilter{TenantID: "tenant-a"}, now.Add(2*time.Second))
	if err != nil || removed != 2 {
		t.Fatalf("pruned %d err %v, want 2", removed, err)
	}
	if records, _, err := store.List(ctx, ToolAuditFilter{TenantID: "tenant-a", Limit: 100, Cursor: ""}); err != nil || len(records) != 1 {
		t.Fatalf("tenant-a after prune = %d records err %v", len(records), err)
	}
}

func TestAuditStorePruneUnboundedForbidden(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "audit.db")
	store, err := NewSQLiteAuditStore(SQLiteStoreConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("NewSQLiteAuditStore: %v", err)
	}
	if _, err := store.Prune(context.Background(), ToolAuditFilter{}, time.Time{}); err == nil {
		t.Fatal("a prune without a cutoff must be refused")
	}
	ctx := context.Background()
	// A cutoff bounds every delete; tenant scoping keeps multi-tenant
	// records intact.
	if _, err := store.Prune(ctx, ToolAuditFilter{}, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("cutoff-bounded prune must succeed: %v", err)
	}
}

func TestToolAuditDecisionsClassify(t *testing.T) {
	classified := []struct {
		name string
		err  error
		want string
	}{
		{name: "policy denied", err: ErrToolPolicyDenied, want: ToolAuditDeniedPolicy},
		{name: "needs approval", err: ErrToolApprovalRequired, want: ToolAuditNeedsApproval},
		{name: "rate limited", err: ErrToolRateLimited, want: ToolAuditRateLimited},
		{name: "concurrency limited", err: ErrToolConcurrencyLimit, want: ToolAuditConcurrencyLimited},
		{name: "invalid payload", err: ErrInvalidPayload, want: ToolAuditInvalidPayload},
		{name: "payload too large", err: ErrPayloadTooLarge, want: ToolAuditPayloadTooLarge},
		{name: "tool disabled", err: ErrToolDisabled, want: ToolAuditDisabled},
		{name: "tool not found", err: ErrToolNotFound, want: ToolAuditNotFound},
		{name: "action missing", err: ErrActionNotFound, want: ToolAuditActionMissing},
		{name: "wrap chain", err: errors.Join(ErrInvalidPayload, ErrToolNotFound), want: ToolAuditInvalidPayload},
	}
	for _, tc := range classified {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultToolAuditReason(tc.err); got != tc.want {
				t.Fatalf("DefaultToolAuditReason = %q, want %q", got, tc.want)
			}
		})
	}
}
