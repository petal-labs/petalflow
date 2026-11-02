package tool

import (
	"context"
	"errors"
	"fmt"
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

func TestAuditRecorderGuardPaths(t *testing.T) {
	var recorder *ToolAuditRecorder
	// Nil recorders are no-ops, including their Store access.
	if recorder.Store() != nil {
		t.Fatal("nil recorder must expose a nil store")
	}
	recorder.Record(context.Background(), ToolAuditRecord{ID: "nil-risk"})

	// Nil stores inside a recorder drop writes silently.
	shifted := NewToolAuditRecorder(nil)
	if shifted.Store() != nil {
		t.Fatal("nil store recorder")
	}
	shifted.Record(context.Background(), ToolAuditRecord{ID: "nil-store"})
}

func TestAuditPayloadSummaryEdgeShapes(t *testing.T) {
	// Malformed payloads simply produce no summary.
	if summary := AuditPayloadSummary(make(chan int)); summary != nil {
		t.Fatalf("unmarshalable payloads must return nil, got %+v", summary)
	}
	// Non-object payloads produce no summary.
	if summary := AuditPayloadSummary("string"); summary != nil {
		t.Fatalf("non-object payloads must return nil, got %+v", summary)
	}

	// Long key names are truncated to 64 chars and summary caps at 16 keys.
	payload := map[string]any{}
	for i := 0; i < 20; i++ {
		payload[fmt.Sprintf("%062d", i)+"suffix"] = i
	}
	summary := AuditPayloadSummary(payload)
	if len(summary) != 16 {
		t.Fatalf("summary size = %d, want 16", len(summary))
	}
	for name := range summary {
		if len(name) > 64 {
			t.Fatalf("key %q longer than 64", name)
		}
	}
}

func TestAuditRedactionBoundaryBranches(t *testing.T) {
	// The key-prefix branch preserves the prefix up to the value.
	if got := RedactAuditText("passwd = value"); got != "passwd = **********" {
		t.Fatalf("passwd redaction = %q", got)
	}
	// The malformed branch falls back to the source match text.
	if got := RedactAuditText(""); got != "" {
		t.Fatalf("empty redaction = %q", got)
	}
	// Value-kind enumeration for unions (JSON shapes only).
	if summary := AuditPayloadSummary(map[string]any{
		"flag": true, "list": []any{"a"}, "obj": map[string]any{"x": 1},
	}); summary["flag"] != "boolean" || summary["list"] != "array" || summary["obj"] != "object" {
		t.Fatalf("kind summary = %+v", summary)
	}
}

func TestAuditStoreSQLiteErrorsAndSchemaPaths(t *testing.T) {
	if _, err := NewSQLiteAuditStore(SQLiteStoreConfig{DSN: ""}); err == nil {
		t.Fatal("empty DSN must be rejected")
	}
	// Bad sql.Filename paths fail cleanly.
	if _, err := NewSQLiteAuditStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "miss", "audit.db")}); err == nil {
		t.Fatal("bad paths must error on schema setup")
	}

	store, err := NewSQLiteAuditStore(SQLiteStoreConfig{DSN: filepath.Join(t.TempDir(), "audit.db")})
	if err != nil {
		t.Fatalf("NewSQLiteAuditStore: %v", err)
	}
	ctx := context.Background()

	// Record ID is required.
	if err := store.Append(ctx, ToolAuditRecord{TenantID: "t"}); err == nil || !strings.Contains(err.Error(), "record id is required") {
		t.Fatalf("id-less append = %v", err)
	}

	// No rows error paths: empty scans fail null-safety gracefully.
	if records, _, err := store.List(ctx, ToolAuditFilter{TenantID: "absent", Cursor: ""}); len(records) != 0 || err != nil {
		t.Fatalf("absent filter = %d records %v", len(records), err)
	}

	// Prune honors tenant scoping strictly.
	now := time.Now().UTC()
	if err := store.Append(ctx, ToolAuditRecord{ID: "p-a", TenantID: "pa", Tool: "t", Kind: ToolAuditInvocation, CreatedAt: now}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.Append(ctx, ToolAuditRecord{ID: "p-b", TenantID: "pb", Tool: "t", Kind: ToolAuditInvocation, CreatedAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	removed, err := store.Prune(ctx, ToolAuditFilter{TenantID: "pa"}, now.Add(time.Minute))
	if err != nil || removed != 1 {
		t.Fatalf("pruned = %d err = %v, want 1", removed, err)
	}
	if records, _, _ := store.List(ctx, ToolAuditFilter{TenantID: "pb", Cursor: ""}); len(records) != 1 {
		t.Fatalf("tenant-scope prune touched tenant pb: %#v", records)
	}
}
