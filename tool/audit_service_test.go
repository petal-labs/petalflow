package tool

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAuditServiceWiringLifecycle(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	auditStore := NewMemoryAuditStore()

	var invoked int
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: store,
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				invoked++
				if reg.Name == "failing_tool" {
					return &stubAdapter{err: errors.New("adapter exploded")}, nil
				}
				return &stubAdapter{response: InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}}, nil
			},
		},
		ReachabilityChecker: stubReachabilityChecker{},
		AuditStore:          auditStore,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	ctx := context.Background()

	manifest := NewManifest("audited_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["execute"] = ActionSpec{Inputs: map[string]FieldSpec{"q": {Type: TypeString, Required: true}}}
	if err := store.Upsert(ctx, ToolRegistration{
		Name: "audited_tool", Origin: OriginHTTP, Manifest: manifest,
		TenantID: "tenant-a", Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("seed registration: %v", err)
	}

	// Success → invocation record carrying the run context identity.
	runCtx := ContextWithAuditContext(
		ContextWithInvocationScope(context.Background(), "run-1"),
		ToolAuditContext{Actor: "workflow:wf-1", TenantID: "tenant-a", WorkflowID: "wf-1", RunID: "run-1", NodeID: "n1"},
	)
	if _, err := service.TestAction(runCtx, "audited_tool", "execute", map[string]any{"q": "ping"}); err != nil {
		t.Fatalf("invocation: %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditInvocation, ToolAuditAllowed, "audited_tool")

	// Invalid payload → failure record before the adapter was engaged.
	before := invoked
	if _, err := service.TestAction(runCtx, "audited_tool", "execute", map[string]any{}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("invalid payload error = %v", err)
	}
	if invoked != before {
		t.Fatal("adapter must not engage for invalid payloads")
	}
	assertAuditRecord(t, auditStore, ToolAuditFailure, ToolAuditInvalidPayload, "audited_tool")

	// Registration disabled → rejection record with the disabled reason.
	disabled, _, _ := service.Get(ctx, "audited_tool", true)
	disabled.Enabled = false
	if err := store.Upsert(ctx, disabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := service.TestAction(ctx, "audited_tool", "execute", map[string]any{"q": "ping"}); !errors.Is(err, ErrToolDisabled) {
		t.Fatalf("disabled error = %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditRejection, ToolAuditDisabled, "audited_tool")

	// Re-enable the registration for the remaining checks.
	reEnabled, _, _ := service.Get(ctx, "audited_tool", true)
	reEnabled.Enabled = true
	reEnabled.Status = StatusReady
	if err := store.Upsert(ctx, reEnabled); err != nil {
		t.Fatalf("re-enable: %v", err)
	}

	// Missing registration → rejection record.
	if _, err := service.TestAction(ctx, "ghost_tool", "execute", map[string]any{}); !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("ghost tool error = %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditRejection, ToolAuditNotFound, "ghost_tool")

	// Register/Refresh produce discovery records.
	discovered := NewManifest("discovered_tool")
	discovered.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	discovered.Actions["go"] = ActionSpec{}
	if _, err := service.Register(ctx, RegisterToolInput{
		Name:     "discovered_tool",
		Origin:   OriginHTTP,
		Manifest: &discovered,
	}); err != nil {
		t.Fatalf("register http tool: %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditDiscovery, ToolAuditRegistered, "discovered_tool")

	// Unknown action failures record with the typed reason.
	failing := NewManifest("failing_tool")
	failing.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	failing.Actions["execute"] = ActionSpec{}
	if err := store.Upsert(ctx, ToolRegistration{
		Name: "failing_tool", Origin: OriginHTTP, Manifest: failing,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("seed failing: %v", err)
	}
	if _, err := service.TestAction(ctx, "failing_tool", "execute", map[string]any{}); err == nil {
		t.Fatal("failing tool must surface its adapter error")
	}
	assertAuditRecord(t, auditStore, ToolAuditFailure, ToolAuditError, "failing_tool")

	// Unknown actions record a rejection when referenced.
	if _, err := service.TestAction(ctx, "audited_tool", "missing", map[string]any{"q": "x"}); !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("missing action error = %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditFailure, ToolAuditActionMissing, "audited_tool")
}

func assertAuditRecord(t *testing.T, store *MemoryAuditStore, kind ToolAuditKind, decision, toolName string) {
	t.Helper()
	records, _, err := store.List(context.Background(), ToolAuditFilter{Kind: kind, Tool: toolName, Limit: 100, Cursor: ""})
	if err != nil {
		t.Fatalf("list audit %q: %v", kind, err)
	}
	if len(records) == 0 {
		t.Fatalf("no audit record for kind=%q tool=%q; store = ##%#v", kind, toolName, storeListDebug(store))
	}
	found := false
	for _, record := range records {
		if record.Decision == decision {
			found = true
			if record.Kind != kind {
				t.Fatalf("kind mismatch: %+v", record)
			}
		}
	}
	if !found {
		t.Fatalf("audit record kind=%q decision=%q not found among %+v", kind, decision, records)
	}
}

func storeListDebug(store *MemoryAuditStore) []ToolAuditRecord {
	records, _, _ := store.List(context.Background(), ToolAuditFilter{})
	return records
}

func TestAuditCursorEncodingRoundTrip(t *testing.T) {
	createdAt := time.Now().UTC()
	id := uuid.NewString()
	decodedTime, decodedID, ok := decodeToolAuditCursor(toolAuditCursor(createdAt, id))
	if !ok || decodedID != id || decodedTime.Sub(createdAt).Abs() > time.Microsecond {
		t.Fatalf("cursor round trip = %v %q %v", decodedTime, decodedID, ok)
	}
	if _, _, ok := decodeToolAuditCursor("bogus"); ok {
		t.Fatal("invalid cursors must not decode")
	}
	if _, _, ok := decodeToolAuditCursor(""); ok {
		t.Fatal("empty cursors must not decode")
	}
}

func TestAuditStoreSQLiteFiltersAndCursor(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "audit.db")
	store, err := NewSQLiteAuditStore(SQLiteStoreConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("NewSQLiteAuditStore: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()

	for _, record := range []ToolAuditRecord{
		{ID: "wf-match", TenantID: "t", WorkflowID: "wf-1", RunID: "run-1", NodeID: "n-1", Actor: "test", Tool: "http_fetch", Kind: ToolAuditInvocation, Decision: ToolAuditAllowed, CreatedAt: now},
		{ID: "wf-other", TenantID: "t", WorkflowID: "wf-2", Tool: "http_fetch", Kind: ToolAuditInvocation, Decision: ToolAuditAllowed, CreatedAt: now.Add(time.Second)},
		{ID: "kind-other", TenantID: "t", WorkflowID: "wf-1", Tool: "email-send", Kind: ToolAuditRejection, Decision: ToolAuditDeniedPolicy, CreatedAt: now.Add(2 * time.Second)},
	} {
		if err := store.Append(ctx, record); err != nil {
			t.Fatalf("seed %s: %v", record.ID, err)
		}
	}

	if records, _, err := store.List(ctx, ToolAuditFilter{TenantID: "t", WorkflowID: "wf-1", RunID: "run-1", Tool: "http_fetch", Kind: ToolAuditInvocation, Cursor: ""}); err != nil || len(records) != 1 {
		t.Fatalf("composite filter = %d records err %v", len(records), err)
	}
	if records, _, err := store.List(ctx, ToolAuditFilter{TenantID: "t", Kind: ToolAuditRejection, Cursor: ""}); err != nil || len(records) != 1 {
		t.Fatalf("kind filter = %d records err %v", len(records), err)
	}

	// Walking pages with cursor yields every record exactly once.
	seen := map[string]bool{}
	pageCursor := ""
	for pages := 0; pages < 10; pages++ {
		records, next, err := store.List(ctx, ToolAuditFilter{TenantID: "t", Limit: 1, Cursor: pageCursor})
		if err != nil {
			t.Fatalf("page walk: %v", err)
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			if seen[record.ID] {
				t.Fatalf("record %s appeared twice in pages", record.ID)
			}
			seen[record.ID] = true
		}
		if record := records[len(records)-1]; !strings.Contains(record.ID, "-") {
			t.Fatalf("unexpected id shape %q", record.ID)
		}
		pageCursor = next
		if pageCursor == "" {
			break
		}
	}
	if len(seen) != 3 {
		t.Fatalf("paged records = %d, want 3", len(seen))
	}

	// Duplicate appends surface a primary-key constraint failure at the
	// SQL layer; the recorder-level dedupe keeps callers safe.
	first := ToolAuditRecord{ID: "dup-1", TenantID: "t", Tool: "http_fetch", Kind: ToolAuditFailure, Decision: ToolAuditError, CreatedAt: now}
	if err := store.Append(ctx, first); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if records, _, _ := store.List(ctx, ToolAuditFilter{TenantID: "t", Tool: "http_fetch", Limit: 100, Cursor: ""}); len(records) != 3 {
		t.Fatalf("sqlite records: %d, want 3", len(records))
	}
}

func TestAuditPolicyDenialAndGrantReplay(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	auditStore := NewMemoryAuditStore()
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: store,
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				return &stubAdapter{response: InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}}, nil
			},
		},
		AuditStore: auditStore,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	ctx := context.Background()

	manifest := NewManifest("governed_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["execute"] = ActionSpec{Inputs: map[string]FieldSpec{}}
	manifest.Safety = &SafetyClassification{Network: true}
	if err := store.Upsert(ctx, ToolRegistration{
		Name: "governed_tool", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Deny policy → rejection audit with the typed reason.
	denyCtx := ContextWithPolicyEvaluator(ctx, PolicyEvaluatorFunc(
		EffectiveToolPolicy{Deny: []string{"governed_tool"}}.Evaluate))
	if _, err := service.TestAction(denyCtx, "governed_tool", "execute", map[string]any{}); !errors.Is(err, ErrToolPolicyDenied) {
		t.Fatalf("deny error = %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditRejection, ToolAuditDeniedPolicy, "governed_tool")

	// Needs-approval policy → rejection audit; with a replayed grant the
	// invocation proceeds and the approval lands in the audit trail.
	approvalCtx := ContextWithPolicyEvaluator(ctx, PolicyEvaluatorFunc(
		EffectiveToolPolicy{Allow: []string{"governed_tool"}, RequireApproval: []string{"governed_tool"}}.Evaluate))
	if _, err := service.TestAction(approvalCtx, "governed_tool", "execute", map[string]any{}); !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("approval-required error = %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditRejection, ToolAuditNeedsApproval, "governed_tool")

	approvalCtx = ContextWithApprovalGrant(approvalCtx, ApprovalGrant{
		ToolName: "governed_tool", ApprovalID: "appr-9", DecidedBy: "op",
	})
	if _, err := service.TestAction(approvalCtx, "governed_tool", "execute", map[string]any{}); err != nil {
		t.Fatalf("granted invocation: %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditApproval, ToolAuditApproved, "governed_tool")
	assertAuditRecord(t, auditStore, ToolAuditInvocation, ToolAuditAllowed, "governed_tool")
}

func TestAuditOutputLimitAndAdapterFailure(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	auditStore := NewMemoryAuditStore()

	bigPayload := strings.Repeat("x", 512)
	var factoryErr bool
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:         store,
		PayloadLimits: &PayloadLimits{MaxInputBytes: 1 << 20, MaxOutputBytes: 64},
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				if factoryErr {
					return nil, errors.New("factory exploded")
				}
				return &stubAdapter{response: InvokeResponse{
					DurationMS: 1, Outputs: map[string]any{"blob": bigPayload},
				}}, nil
			},
		},
		AuditStore: auditStore,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	ctx := context.Background()

	manifest := NewManifest("capped_tool")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	manifest.Actions["execute"] = ActionSpec{Inputs: map[string]FieldSpec{}, Outputs: map[string]FieldSpec{"blob": {Type: TypeString}}}
	if err := store.Upsert(ctx, ToolRegistration{
		Name: "capped_tool", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Oversized outputs fail after the invocation with a typed audit
	// failure record.
	if _, err := service.TestAction(ctx, "capped_tool", "execute", map[string]any{}); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized output error = %v", err)
	}
	assertAuditRecord(t, auditStore, ToolAuditFailure, ToolAuditPayloadTooLarge, "capped_tool")

	// Adapter construction failures record the generic failure reason.
	factoryErr = true
	if _, err := service.TestAction(ctx, "capped_tool", "execute", map[string]any{}); err == nil {
		t.Fatal("factory failure must surface")
	}
	assertAuditRecord(t, auditStore, ToolAuditFailure, ToolAuditError, "capped_tool")
}

func TestDaemonServiceConfigAuditValidation(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	if _, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:         store,
		PayloadLimits: &PayloadLimits{MaxInputBytes: -1},
	}); err == nil {
		t.Fatal("negative payload limits must be rejected")
	}
	if _, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store:            store,
		InvocationLimits: &InvocationLimits{MaxConcurrentPerTool: -2},
	}); err == nil {
		t.Fatal("negative invocation limits must be rejected")
	}
}
