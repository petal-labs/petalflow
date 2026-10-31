package tool

import (
	"context"
	"errors"
	"testing"
)

func TestCheckPolicyWithoutEvaluatorAllows(t *testing.T) {
	// Enforcement is opt-in: no evaluator in context proceeds.
	if err := CheckPolicy(context.Background(), "email-send"); err != nil {
		t.Fatalf("CheckPolicy without evaluator = %v, want nil", err)
	}
}

func TestCheckPolicyDecisions(t *testing.T) {
	policy := ToolPolicy{
		Allow:           []string{"email-send", "mcp/*"},
		Deny:            []string{"fs/write"},
		RequireApproval: []string{"email-send"},
	}
	ctx := ContextWithPolicyEvaluator(context.Background(), PolicyEvaluatorFunc(policy.Evaluate))

	if err := CheckPolicy(ctx, "web-search"); !errors.Is(err, ErrToolPolicyDenied) {
		t.Fatalf("web-search = %v, want ErrToolPolicyDenied", err)
	}
	if err := CheckPolicy(ctx, "fs/write"); !errors.Is(err, ErrToolPolicyDenied) {
		t.Fatalf("fs/write = %v, want ErrToolPolicyDenied", err)
	}
	if err := CheckPolicy(ctx, "mcp/grep"); err != nil {
		t.Fatalf("mcp/grep = %v, want nil", err)
	}
	err := CheckPolicy(ctx, "email-send")
	if !errors.Is(err, ErrToolApprovalRequired) {
		t.Fatalf("email-send = %v, want ErrToolApprovalRequired", err)
	}
	if got, want := PolicyErrorCode(err), ToolErrorCodePolicyApprovalRequired; got != want {
		t.Fatalf("PolicyErrorCode = %q, want %q", got, want)
	}
	if !IsToolPolicyError(err) {
		t.Fatal("approval error should be a policy error")
	}
	if PolicyErrorCode(errors.New("unrelated")) != "" {
		t.Fatal("non-policy errors should have an empty code")
	}
}

func TestEvaluatePolicyReferences(t *testing.T) {
	// Action references use "tool.action" dotted names; policies may
	// address either the base tool or a specific action reference.
	effective := EffectiveToolPolicy{Allow: []string{"fs.read"}}
	if got := EvaluatePolicyReferences(effective, "fs", "fs.read"); got != PolicyAllow {
		t.Fatalf("fs refs = %v, want %v (reference matches the allow rule)", got, PolicyAllow)
	}
	if got := EvaluatePolicyReferences(effective, "fs", "fs.write"); got != PolicyDeny {
		t.Fatalf("unmatched reference = %v, want %v (no allow match)", got, PolicyDeny)
	}
	allowed := EffectiveToolPolicy{Allow: []string{"fs/*"}}
	if got := EvaluatePolicyReferences(allowed, "fs", "fs/read"); got != PolicyAllow {
		t.Fatalf("namespace allow refs = %v, want %v", got, PolicyAllow)
	}

	mixed := EffectiveToolPolicy{Allow: []string{"fs/*"}, RequireApproval: []string{"fs/write"}}
	if got := EvaluatePolicyReferences(mixed, "fs", "fs/write"); got != PolicyNeedsApproval {
		t.Fatalf("referenced approval = %v, want %v", got, PolicyNeedsApproval)
	}
	if got := EvaluatePolicyReferences(mixed, "fs", "fs/read"); got != PolicyAllow {
		t.Fatalf("plain reference = %v, want %v", got, PolicyAllow)
	}
}

func TestEffectivePolicyEvaluatorReferences(t *testing.T) {
	evaluator := FromEffectivePolicy(EffectiveToolPolicy{
		Allow: []string{"pdf_extract"},
	})
	if got := evaluator.EvaluatePolicy("pdf_extract.extract"); got != PolicyAllow {
		t.Fatalf("action reference should resolve to base name: %v, want %v", got, PolicyAllow)
	}
	if got := evaluator.EvaluatePolicy("pdf_extract_other.extract"); got != PolicyDeny {
		t.Fatalf("unrelated action reference = %v, want %v", got, PolicyDeny)
	}

	denyEvaluator := FromEffectivePolicy(EffectiveToolPolicy{
		Allow: []string{"*"},
		Deny:  []string{"pdf_extract"},
	})
	if got := denyEvaluator.EvaluatePolicy("pdf_extract.extract"); got != PolicyDeny {
		t.Fatalf("deny should win across references: %v, want %v", got, PolicyDeny)
	}
}

func TestDaemonToolServiceTestActionPolicyGate(t *testing.T) {
	// Every adapter origin funnels through TestAction; the gate must
	// reject before the adapter factory constructs or engages anything.
	origins := []struct {
		name               string
		toolName           string
		origin             ToolOrigin
		transport          func(*ToolManifest)
		reachabilityNeeded bool
	}{
		{
			name:     "http",
			toolName: "http_probe",
			origin:   OriginHTTP,
			transport: func(m *ToolManifest) {
				m.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
			},
		},
		{
			name:     "mcp",
			toolName: "s3_fetch",
			origin:   OriginMCP,
			transport: func(m *ToolManifest) {
				m.Transport = NewMCPTransport(MCPTransport{Mode: MCPModeStdio, Command: "s3-mcp"})
			},
		},
		{
			name:      "stdio",
			toolName:  "sys_probe",
			origin:    OriginStdio,
			transport: func(m *ToolManifest) { m.Transport = NewStdioTransport(StdioTransport{Command: "probe"}) },
		},
	}

	for _, originCase := range origins {
		t.Run(originCase.name, func(t *testing.T) {
			store := NewDaemonStore(newFakeDaemonBackend())
			ctx := context.Background()

			manifest := NewManifest(originCase.toolName)
			originCase.transport(&manifest)
			manifest.Actions["execute"] = ActionSpec{}
			if err := store.Upsert(ctx, ToolRegistration{
				Name:     originCase.toolName,
				Origin:   originCase.origin,
				Manifest: manifest,
				Status:   StatusReady,
				Enabled:  true,
			}); err != nil {
				t.Fatalf("store.Upsert() error = %v", err)
			}

			var invoked bool
			factory := stubAdapterFactory{
				newFn: func(reg Registration) (Adapter, error) {
					invoked = true
					return &stubAdapter{response: InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}}, nil
				},
			}
			var service *DaemonToolService
			var err error
			if originCase.reachabilityNeeded {
				service, err = NewDaemonToolService(DaemonToolServiceConfig{
					Store:               store,
					AdapterFactory:      factory,
					ReachabilityChecker: stubReachabilityChecker{},
				})
			} else {
				service, err = NewDaemonToolService(DaemonToolServiceConfig{
					Store:          store,
					AdapterFactory: factory,
				})
			}
			if err != nil {
				t.Fatalf("NewDaemonToolService() error = %v", err)
			}

			// Explicitly empty allowlist denies everything before any side effect.
			denyCtx := ContextWithPolicyEvaluator(ctx, PolicyEvaluatorFunc(ToolPolicy{Allow: []string{}}.Evaluate))
			result, err := service.TestAction(denyCtx, originCase.toolName, "execute", map[string]any{})
			if !errors.Is(err, ErrToolPolicyDenied) {
				t.Fatalf("denied TestAction error = %v, want ErrToolPolicyDenied", err)
			}
			if result.Success {
				t.Fatal("denied invocation must not succeed")
			}
			if invoked {
				t.Fatal("adapter factory must not run when the gate rejects")
			}

			// Allowed tool proceeds through the adapter.
			allowCtx := ContextWithPolicyEvaluator(ctx, PolicyEvaluatorFunc(ToolPolicy{Allow: []string{originCase.toolName}}.Evaluate))
			if _, err := service.TestAction(allowCtx, originCase.toolName, "execute", map[string]any{}); err != nil {
				t.Fatalf("allowlisted TestAction = %v, want nil", err)
			}
			if !invoked {
				t.Fatal("allowed invocation should reach the adapter")
			}
		})
	}
}

func TestDaemonToolServiceTestActionReregisteredName(t *testing.T) {
	store := NewDaemonStore(newFakeDaemonBackend())
	ctx := context.Background()

	newHTTP := func(name string) ToolRegistration {
		manifest := NewManifest(name)
		manifest.Transport = NewHTTPTransport(HTTPTransport{Endpoint: "http://localhost:9801"})
		manifest.Actions["extract"] = ActionSpec{}
		return ToolRegistration{
			Name:     name,
			Origin:   OriginHTTP,
			Manifest: manifest,
			Config:   map[string]string{},
			Status:   StatusReady,
			Enabled:  true,
		}
	}
	if err := store.Upsert(ctx, newHTTP("pdf_extract")); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	// A re-registered clone of the same manifest under a new name: the
	// gate evaluates whichever name is invoked.
	if err := store.Upsert(ctx, newHTTP("pdf_extract_v2")); err != nil {
		t.Fatalf("Upsert clone() error = %v", err)
	}

	var invoked bool
	service, err := NewDaemonToolService(DaemonToolServiceConfig{
		Store: store,
		AdapterFactory: stubAdapterFactory{
			newFn: func(reg Registration) (Adapter, error) {
				invoked = true
				return &stubAdapter{response: InvokeResponse{DurationMS: 1, Outputs: map[string]any{"ok": true}}}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewDaemonToolService() error = %v", err)
	}

	denying := ToolPolicy{Deny: []string{"pdf_extract", "pdf_extract_v2"}}
	denyCtx := ContextWithPolicyEvaluator(ctx, PolicyEvaluatorFunc(denying.Evaluate))
	if _, err := service.TestAction(denyCtx, "pdf_extract", "extract", map[string]any{}); !errors.Is(err, ErrToolPolicyDenied) {
		t.Fatalf("original TestAction = %v, want ErrToolPolicyDenied", err)
	}
	if _, err := service.TestAction(denyCtx, "pdf_extract_v2", "extract", map[string]any{}); !errors.Is(err, ErrToolPolicyDenied) {
		t.Fatalf("clone TestAction = %v, want ErrToolPolicyDenied", err)
	}
	if invoked {
		t.Fatal("adapter factory must not run when the gate rejects")
	}

	// Under allowlist semantics, names absent from the allowlist are
	// denied even when the sibling is allowed: re-registration cannot
	// ride an allowlist for the original name.
	allowOnly := ToolPolicy{Allow: []string{"pdf_extract"}}
	allowCtx := ContextWithPolicyEvaluator(ctx, PolicyEvaluatorFunc(allowOnly.Evaluate))
	if _, err := service.TestAction(allowCtx, "pdf_extract", "extract", map[string]any{}); err != nil {
		t.Fatalf("allowlisted TestAction = %v, want nil", err)
	}
	if _, err := service.TestAction(allowCtx, "pdf_extract_v2", "extract", map[string]any{}); !errors.Is(err, ErrToolPolicyDenied) {
		t.Fatalf("unlisted clone TestAction = %v, want ErrToolPolicyDenied", err)
	}
}
