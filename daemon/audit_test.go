package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/security"
	"github.com/petal-labs/petalflow/tool"
)

func newAuditTestServer(t *testing.T, auditStore tool.ToolAuditStore) *Server {
	t.Helper()
	server, err := NewServer(ServerConfig{
		Store:      newSQLiteToolStore(t),
		AuditStore: auditStore,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

func seedAuditRecords(t *testing.T, store tool.ToolAuditStore) time.Time {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	records := []tool.ToolAuditRecord{
		{ID: "a-1", TenantID: "t1", WorkflowID: "wf-1", RunID: "run-1", Tool: "http_fetch", Kind: tool.ToolAuditInvocation, Decision: tool.ToolAuditAllowed, CreatedAt: now},
		{ID: "a-2", TenantID: "t1", WorkflowID: "wf-1", RunID: "run-2", Tool: "email-send", Kind: tool.ToolAuditRejection, Decision: tool.ToolAuditDeniedPolicy, CreatedAt: now.Add(time.Second)},
		{ID: "a-3", TenantID: "t2", Tool: "http_fetch", Kind: tool.ToolAuditFailure, Decision: tool.ToolAuditInvalidPayload, CreatedAt: now.Add(2 * time.Second)},
	}
	for _, record := range records {
		if err := store.Append(ctx, record); err != nil {
			t.Fatalf("seed %s: %v", record.ID, err)
		}
	}
	return now
}

func TestAuditListEndpoint(t *testing.T) {
	// Unconfigured audit stores answer 501.
	noAudit := newAuditTestServer(t, nil)
	rec := httptest.NewRecorder()
	noAudit.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unconfigured status = %d body=%s", rec.Code, rec.Body.String())
	}

	memory := tool.NewMemoryAuditStore()
	seedAuditRecords(t, memory)
	server := newAuditTestServer(t, memory)
	handler := server.Handler()

	type page struct {
		Records    []tool.ToolAuditRecord `json:"records"`
		NextCursor string                 `json:"next_cursor"`
	}
	getPage := func(query string) page {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("list %q status = %d body=%s", query, rec.Code, rec.Body.String())
		}
		var p page
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return p
	}

	// Unauthenticated operator scope sees every record (documented
	// behavior for local deployments).
	if p := getPage("?limit=10"); len(p.Records) != 3 {
		t.Fatalf("unfiltered page = %d records", len(p.Records))
	}

	// Filters narrow by tool, kind, workflow, and run.
	if p := getPage("?tool=http_fetch&kind=failure"); len(p.Records) != 1 || p.Records[0].ID != "a-3" {
		t.Fatalf("tool+kind filter = %+v", p.Records)
	}
	if p := getPage("?workflow_id=wf-1&run_id=run-2"); len(p.Records) != 1 || p.Records[0].ID != "a-2" {
		t.Fatalf("workflow+run filter = %+v", p.Records)
	}

	// Cursor pagination walks pages without duplicates.
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 5; i++ {
		p := getPage(fmt.Sprintf("?limit=2&cursor=%s", cursor))
		for _, record := range p.Records {
			if seen[record.ID] {
				t.Fatalf("record %s repeated across pages", record.ID)
			}
			seen[record.ID] = true
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("paged records = %d, want 3", len(seen))
	}

	// Invalid limits are rejected.
	for _, bad := range []string{"0", "501", "abc", "-3"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit?limit="+bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("limit %q status = %d, want 400", bad, rec.Code)
		}
	}

	// Bogus cursors fall back to the first page rather than erroring.
	if p := getPage("?cursor=not-a-cursor"); len(p.Records) != 3 {
		t.Fatalf("bogus cursor page = %d records", len(p.Records))
	}
}

func TestAuditListAuthWithoutTenantSeesNothing(t *testing.T) {
	memory := tool.NewMemoryAuditStore()
	seedAuditRecords(t, memory)
	server, err := NewServer(ServerConfig{
		Store:      newSQLiteToolStore(t),
		AuditStore: memory,
		Security:   SecurityConfig{RequireAuth: true},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// A request that reaches the handler without tenant identity (the
	// middleware normally rejects these; the store-side guard stays
	// defensive) receives an empty page, never cross-tenant rows.
	rec := httptest.NewRecorder()
	server.handleListAudit(rec, httptest.NewRequest(http.MethodGet, "/api/audit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var page struct {
		Records []tool.ToolAuditRecord `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Records) != 0 {
		t.Fatalf("auth-without-tenant leaked %d records", len(page.Records))
	}
}

func TestAuditPruneEndpoint(t *testing.T) {
	noAudit := newAuditTestServer(t, nil)
	rec := httptest.NewRecorder()
	noAudit.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/audit?before="+time.Now().UTC().Format(time.RFC3339), nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("unconfigured prune status = %d", rec.Code)
	}

	memory := tool.NewMemoryAuditStore()
	seededAt := seedAuditRecords(t, memory)
	server := newAuditTestServer(t, memory)
	handler := server.Handler()

	// Missing and malformed cutoffs are rejected.
	for _, query := range []string{"", "?before=not-a-time", "?before="} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/audit"+query, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("prune %q status = %d, want 400", query, rec.Code)
		}
	}

	// A cutoff between the second and third seed removes exactly two.
	cutoff := seededAt.Add(1500 * time.Millisecond)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/audit?before="+cutoff.Format(time.RFC3339Nano), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("prune status = %d body=%s", rec.Code, rec.Body.String())
	}
	var pruned struct {
		Removed int64 `json:"removed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pruned); err != nil {
		t.Fatalf("unmarshal prune: %v", err)
	}
	if pruned.Removed != 2 {
		t.Fatalf("removed = %d, want 2", pruned.Removed)
	}
}

func TestAuditMiddlewareInjectsAuditIdentity(t *testing.T) {
	server, err := NewServer(ServerConfig{
		Store: newSQLiteToolStore(t),
		Security: SecurityConfig{
			RequireAuth: true,
			Authenticator: func(*http.Request) (security.Identity, error) {
				return security.Identity{Subject: "api", TenantID: "tenant-9"}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	probe := server.middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		meta, ok := tool.AuditContextFromContext(r.Context())
		if !ok {
			t.Error("audit context missing from request context")
			return
		}
		if meta.Actor != "user:api" || meta.TenantID != "tenant-9" {
			t.Errorf("audit identity = %+v", meta)
		}
	}))
	rec := httptest.NewRecorder()
	probe.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/tools", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("probe status = %d", rec.Code)
	}
}

func TestWriteServiceErrorAuditMappings(t *testing.T) {
	server := newAuditTestServer(t, nil)

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"rate limited", fmt.Errorf("wrap: %w", tool.ErrToolRateLimited), http.StatusTooManyRequests, tool.ToolErrorCodeRateLimited},
		{"concurrency limited", fmt.Errorf("wrap: %w", tool.ErrToolConcurrencyLimit), http.StatusTooManyRequests, tool.ToolErrorCodeConcurrencyLimit},
		{"invalid payload", fmt.Errorf("wrap: %w", tool.ErrInvalidPayload), http.StatusBadRequest, tool.ToolErrorCodeInvalidPayload},
		{"payload too large", fmt.Errorf("wrap: %w", tool.ErrPayloadTooLarge), http.StatusUnprocessableEntity, tool.ToolErrorCodePayloadTooLarge},
		{"action missing", fmt.Errorf("wrap: %w", tool.ErrActionNotFound), http.StatusUnprocessableEntity, tool.ToolErrorCodeActionNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			server.writeServiceError(rec, tc.err)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if body.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tc.wantCode)
			}
		})
	}

	// Unrelated errors keep the generic mapping.
	rec := httptest.NewRecorder()
	server.writeServiceError(rec, errors.New("plain failure"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("plain error status = %d", rec.Code)
	}
}

// failingAuditStore rejects every operation so the endpoint error paths
// stay observable.
type failingAuditStore struct{}

func (failingAuditStore) Append(context.Context, tool.ToolAuditRecord) error {
	return errors.New("audit store outage")
}
func (failingAuditStore) List(context.Context, tool.ToolAuditFilter) ([]tool.ToolAuditRecord, string, error) {
	return nil, "", errors.New("audit store outage")
}
func (failingAuditStore) Prune(context.Context, tool.ToolAuditFilter, time.Time) (int64, error) {
	return 0, errors.New("audit store outage")
}

func TestAuditEndpointStoreFailures(t *testing.T) {
	server := newAuditTestServer(t, failingAuditStore{})
	handler := server.Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/audit?limit=5", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list on failing store status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !jsonContains(rec.Body.String(), "AUDIT_QUERY_FAILED") {
		t.Fatalf("list error code missing: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/audit?before="+time.Now().UTC().Format(time.RFC3339), nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("prune on failing store status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !jsonContains(rec.Body.String(), "AUDIT_PRUNE_FAILED") {
		t.Fatalf("prune error code missing: %s", rec.Body.String())
	}
}

func jsonContains(body, code string) bool {
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return false
	}
	return parsed.Error.Code == code
}
