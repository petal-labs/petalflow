package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"testing"
)

func TestArgumentsDigestCanonicalForm(t *testing.T) {
	// The canonical encoding is pinned so other consumers (durable
	// verifiers, downstream governed tools) can compute the same value:
	// sorted keys, no whitespace, no HTML escaping.
	canonical := `{"arguments":{"cmd":"a && b <x>","cwd":"/tmp"},"reference":"computer.exec"}`
	sum := sha256.Sum256([]byte(canonical))
	want := "sha256:" + hex.EncodeToString(sum[:])

	got := ArgumentsDigest("computer.exec", map[string]any{"cwd": "/tmp", "cmd": "a && b <x>"})
	if got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
}

func TestArgumentsDigestStability(t *testing.T) {
	nestedA := map[string]any{"b": 2, "a": map[string]any{"y": []any{1, "two"}, "x": true}}
	nestedB := map[string]any{"a": map[string]any{"x": true, "y": []any{1, "two"}}, "b": 2}
	if ArgumentsDigest("t.a", nestedA) != ArgumentsDigest("t.a", nestedB) {
		t.Fatal("digest must not depend on map construction order")
	}

	// A JSON round trip turns ints into float64; the digest is unchanged.
	if ArgumentsDigest("t.a", map[string]any{"n": 5}) != ArgumentsDigest("t.a", map[string]any{"n": float64(5)}) {
		t.Fatal("int and float64 of the same value must digest identically")
	}
	if ArgumentsDigest("t.a", nil) != ArgumentsDigest("t.a", map[string]any{}) {
		t.Fatal("nil and empty arguments must digest identically")
	}

	base := ArgumentsDigest("t.a", map[string]any{"cmd": "ls"})
	if base == ArgumentsDigest("t.a", map[string]any{"cmd": "rm -rf /"}) {
		t.Fatal("different arguments must produce different digests")
	}
	if base == ArgumentsDigest("t.b", map[string]any{"cmd": "ls"}) {
		t.Fatal("different actions must produce different digests")
	}

	if got := ArgumentsDigest("t.a", map[string]any{"bad": math.NaN()}); got != "" {
		t.Fatalf("unencodable arguments digest = %q, want empty", got)
	}
}

func TestApprovalGrantForInvocation(t *testing.T) {
	args := map[string]any{"cmd": "ls"}
	digest := ArgumentsDigest("computer.exec", args)
	bound := ApprovalGrant{
		ToolName: "computer", ApprovalID: "appr-1", DecidedBy: "op",
		Reference: "computer.exec", ArgumentsDigest: digest,
	}
	ctx := ContextWithApprovalGrant(context.Background(), bound)

	if _, ok := ApprovalGrantForInvocation(context.Background(), "computer.exec", digest); ok {
		t.Fatal("no grant attached must not match")
	}
	got, ok := ApprovalGrantForInvocation(ctx, "computer.exec", digest)
	if !ok || got.ApprovalID != "appr-1" {
		t.Fatalf("exact invocation match = %#v, %v", got, ok)
	}
	if _, ok := ApprovalGrantForInvocation(ctx, "computer.write_file", ArgumentsDigest("computer.write_file", args)); ok {
		t.Fatal("grant for one action must not authorize another action")
	}
	if _, ok := ApprovalGrantForInvocation(ctx, "computer.exec", ArgumentsDigest("computer.exec", map[string]any{"cmd": "rm"})); ok {
		t.Fatal("grant for one set of arguments must not authorize other arguments")
	}
	if _, ok := ApprovalGrantForInvocation(ctx, "computer.exec", ""); ok {
		t.Fatal("empty digest must never match")
	}

	// Native tools can read the binding and compare it themselves.
	fromCtx, ok := ApprovalGrantFromContext(ctx)
	if !ok || !fromCtx.Bound() || fromCtx.ArgumentsDigest != ArgumentsDigest(fromCtx.Reference, args) {
		t.Fatalf("grant from context = %#v, %v", fromCtx, ok)
	}

	// Legacy grants keep the tool-name lookup but never match an
	// invocation.
	legacy := ContextWithApprovalGrant(context.Background(), ApprovalGrant{ToolName: "computer", ApprovalID: "appr-2"})
	if _, ok := ApprovalGrantForInvocation(legacy, "computer.exec", digest); ok {
		t.Fatal("legacy grant must not match an invocation")
	}
	if _, ok := ApprovalGrantForTool(legacy, "computer"); !ok {
		t.Fatal("legacy grant must still match by tool name")
	}
}

func TestDaemonGateBindsGrantToActionAndArguments(t *testing.T) {
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

	manifest := NewManifest("computer")
	manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
	cmdInput := map[string]FieldSpec{"cmd": {Type: TypeString}}
	manifest.Actions["exec"] = ActionSpec{Inputs: cmdInput}
	manifest.Actions["write_file"] = ActionSpec{Inputs: cmdInput}
	if err := store.Upsert(ctx, ToolRegistration{
		Name: "computer", Origin: OriginHTTP, Manifest: manifest,
		Status: StatusReady, Enabled: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	gated := ContextWithPolicyEvaluator(ctx, PolicyEvaluatorFunc(
		EffectiveToolPolicy{Allow: []string{"computer"}, RequireApproval: []string{"computer"}}.Evaluate))
	approved := map[string]any{"cmd": "ls"}
	grantCtx := ContextWithApprovalGrant(gated, ApprovalGrant{
		ToolName: "computer", ApprovalID: "appr-1", DecidedBy: "op",
		Reference: "computer.exec", ArgumentsDigest: ArgumentsDigest("computer.exec", approved),
	})

	if _, err := service.TestAction(grantCtx, "computer", "exec", map[string]any{"cmd": "ls"}); err != nil {
		t.Fatalf("approved invocation: %v", err)
	}
	if _, err := service.TestAction(grantCtx, "computer", "write_file", map[string]any{"cmd": "ls"}); !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("other action error = %v, want approval required", err)
	}
	if _, err := service.TestAction(grantCtx, "computer", "exec", map[string]any{"cmd": "rm -rf /"}); !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("other arguments error = %v, want approval required", err)
	}

	// Compatibility: a legacy grant without a digest keeps the
	// tool-name match.
	legacyCtx := ContextWithApprovalGrant(gated, ApprovalGrant{ToolName: "computer", ApprovalID: "appr-2"})
	if _, err := service.TestAction(legacyCtx, "computer", "write_file", map[string]any{"cmd": "ls"}); err != nil {
		t.Fatalf("legacy grant invocation: %v", err)
	}
}
