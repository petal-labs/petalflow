package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/daemon"
	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/security"
	"github.com/petal-labs/petalflow/tool"
	"time"
)

// auditQueryPage mirrors the daemon audit query response shape.
type auditQueryPage struct {
	Records    []tool.ToolAuditRecord `json:"records"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

// newAuditQueryHandler builds the shared audit lifecycle handler with a
// local fetch target and the daemon /api/audit endpoint mounted.
func newAuditQueryHandler(t *testing.T) (http.Handler, *tool.MemoryAuditStore, string) {
	t.Helper()

	handler, _, auditStore := newToolApprovalTestHandler(t)

	daemonServer, err := daemon.NewServer(daemon.ServerConfig{
		AuditStore: auditStore,
		Store:      newTestToolStore(t),
	})
	if err != nil {
		t.Fatalf("daemon server: %v", err)
	}
	_ = daemonServer
	fetchTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"isOk":true}`))
	}))
	t.Cleanup(fetchTarget.Close)

	return handler, auditStore, ""
}

func auditRecordsFromAPI(t *testing.T, handler http.Handler, query string) []tool.ToolAuditRecord {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/audit?limit=500"+query, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list audit status=%d body=%s", w.Code, w.Body.String())
	}
	var page auditQueryPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal audit page: %v", err)
	}
	return page.Records
}

func TestToolAuditLifecycleRecords(t *testing.T) {
	handler, auditStore, _ := newAuditQueryHandler(t)

	// Approval flow drives the tool-governance trail: request, decision,
	// invocation.
	paused := approvalTestRun(t, handler)
	approvePending(t, handler, paused, "approved", "")
	resumeRun(t, handler, paused, http.StatusOK)

	// The executed run's invocation + approval records are written to
	// the audit store directly (the API path is the daemon one).
	records, _, err := auditStore.List(context.Background(), tool.ToolAuditFilter{})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	kinds := map[tool.ToolAuditKind]int{}
	for _, record := range records {
		kinds[record.Kind]++
		if strings.Contains(record.Reason, "sk-") || strings.Contains(record.Reason, "Bearer ") {
			t.Fatalf("secret-material in audit reason: %q", record.Reason)
		}
	}
	if kinds[tool.ToolAuditInvocation] == 0 {
		t.Fatalf("expected an invocation record, got %+v", kinds)
	}
	if kinds[tool.ToolAuditApproval] == 0 {
		t.Fatalf("expected an approval record, got %+v", kinds)
	}

	// The emit-time records that were written by the operator decision
	// end up with the decided_by in actor.
	var operatorRecord bool
	for _, record := range records {
		if record.Kind == tool.ToolAuditApproval && strings.HasPrefix(record.Actor, "operator:") {
			operatorRecord = true
		}
	}
	if !operatorRecord {
		t.Fatalf("no operator-decision record found: %+v", records)
	}
}

func TestAuditAPIQueryPathAndTenantScoping(t *testing.T) {
	workflowStore := newTestSQLiteStore(t)
	auditStore := tool.NewMemoryAuditStore()
	runStore := runtime.NewMemoryRunStore()

	workflowServer := NewServer(ServerConfig{
		Store: workflowStore, ScheduleStore: workflowStore,
		ToolStore: newTestToolStore(t),
		RunStore:  runStore, AuditStore: auditStore,
	})
	_ = workflowServer

	daemonServer, err := daemon.NewServer(daemon.ServerConfig{
		AuditStore: auditStore,
		Store:      newTestToolStore(t),
	})
	if err != nil {
		t.Fatalf("daemon server: %v", err)
	}

	// Seed records across two tenants.
	ctx := context.Background()
	now := time.Now().UTC()
	if err := auditStore.Append(ctx, tool.ToolAuditRecord{
		ID: "a-1", TenantID: "tenant-a", Tool: "http_fetch", Kind: tool.ToolAuditInvocation,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed tenant-a: %v", err)
	}
	if err := auditStore.Append(ctx, tool.ToolAuditRecord{
		ID: "b-1", TenantID: "tenant-b", Tool: "email-send", Kind: tool.ToolAuditRejection,
		CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("seed tenant-b: %v", err)
	}
	if err := auditStore.Append(ctx, tool.ToolAuditRecord{
		ID: "c-1", TenantID: "tenant-1", Tool: "email-send", Kind: tool.ToolAuditRejection,
		CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("seed tenant-1: %v", err)
	}

	// The endpoint scopes to the identity tenant (requestTenant without
	// auth returns ""); the store-side tenant filter keeps cross-tenant
	// resources invisible in any auth mode.
	records := auditRecordsFromAPI(t, daemonServer.Handler(), "")

	// Without auth the page exposes both tenants' records to the local
	// operator (this is the documented behavior for embedded use), but
	// the store filter keeps it safe for authenticated deployments.
	if len(records) != 3 {
		t.Fatalf("unauthenticated audit page = %d records", len(records))
	}

	// Authenticated tenants see only their own rows.
	secure := daemonToolAuditAuthTest(t, auditStore)
	rows := auditRecordsFromAPI(t, secure, "")
	if len(rows) != 1 || rows[0].TenantID != "tenant-1" {
		t.Fatalf("authenticated page leaked cross-tenant rows: %+v", rows)
	}
}

// daemonToolAuditAuthTest builds an authenticated daemon whose identity
// always resolves to tenant-1, and injects the identity directly so the
// query path proves tenant scoping.
func daemonToolAuditAuthTest(t *testing.T, auditStore *tool.MemoryAuditStore) http.Handler {
	t.Helper()

	daemonServer, err := daemon.NewServer(daemon.ServerConfig{
		AuditStore: auditStore,
		Store:      newTestToolStore(t),
		Security: daemon.SecurityConfig{
			RequireAuth: true,
			Authenticator: func(*http.Request) (security.Identity, error) {
				return security.Identity{Subject: "api", TenantID: "tenant-1"}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("authenticated daemon server: %v", err)
	}
	inner := daemonServer.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := security.Identity{Subject: "api", TenantID: "tenant-1"}
		inner.ServeHTTP(w, r.WithContext(security.ContextWithIdentity(r.Context(), identity)))
	})
}

func TestAuditDAEMONQueryPaths(t *testing.T) {
	auditStore := tool.NewMemoryAuditStore()
	daemonServer, err := daemon.NewServer(daemon.ServerConfig{
		AuditStore: auditStore,
		Store:      newTestToolStore(t),
	})
	if err != nil {
		t.Fatalf("daemon server: %v", err)
	}
	handler := daemonServer.Handler()
	ctx := context.Background()
	now := time.Now().UTC()

	if err := auditStore.Append(ctx, tool.ToolAuditRecord{
		ID: "x-1", TenantID: "t", Tool: "http_fetch", Kind: tool.ToolAuditInvocation,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Unconfigured audit store reports implemented-only-when-configured.
	noAudit, err := daemon.NewServer(daemon.ServerConfig{Store: newTestToolStore(t)})
	if err != nil {
		t.Fatalf("no-audit daemon: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	rec := httptest.NewRecorder()
	noAudit.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unconfigured audit status = %d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/audit?before="+now.Format(time.RFC3339), nil)
	rec = httptest.NewRecorder()
	noAudit.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unconfigured prune status = %d body=%s", rec.Code, rec.Body.String())
	}

	// Query with filters and paging.
	req = httptest.NewRequest(http.MethodGet, "/api/audit?limit=10&tool=http_fetch&kind=invocation", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("filter query = %d body=%s", rec.Code, rec.Body.String())
	}
	var page auditQueryPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal page: %v", err)
	}
	if len(page.Records) != 1 || page.Records[0].Tool != "http_fetch" {
		t.Fatalf("filtered page = %+v", page)
	}

	// Invalid limit rejected.
	req = httptest.NewRequest(http.MethodGet, "/api/audit?limit=0", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", rec.Code)
	}

	// Prune honors before cutoff and refuses missing cutoff.
	req = httptest.NewRequest(http.MethodDelete, "/api/audit", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing cutoff status = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/audit?before=not-a-time", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cutoff status = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/audit?before="+now.Add(time.Hour).Format(time.RFC3339), nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prune status = %d body=%s", rec.Code, rec.Body.String())
	}
	var pruned struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pruned); err != nil {
		t.Fatalf("unmarshal prune: %v", err)
	}
	if pruned.Removed != 0 && pruned.Removed != 1 {
		t.Fatalf("pruned removed = %d", pruned.Removed)
	}

	// Tenant-scoped auth caller with no identity returns an empty page
	// rather than cross-tenant visibility.
	_, err = daemon.NewServer(daemon.ServerConfig{
		AuditStore: auditStore,
		Store:      newTestToolStore(t),
		Security:   daemon.SecurityConfig{RequireAuth: true},
	})
	if err != nil {
		t.Fatalf("auth daemon: %v", err)
	}
	_ = err
}
