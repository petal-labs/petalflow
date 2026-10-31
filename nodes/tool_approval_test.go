package nodes

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	toolpolicy "github.com/petal-labs/petalflow/tool"

	"github.com/petal-labs/petalflow/core"
	"github.com/petal-labs/petalflow/runtime"
)

func approvalBridgeContext(ctx context.Context, handler func(context.Context, any) (any, error)) context.Context {
	return runtime.ContextWithToolApprovalHandler(ctx, handler)
}

func TestToolNode_ApprovalHardBlocksWithoutBridge(t *testing.T) {
	tool := &mockPetalTool{name: "email-send", result: map[string]any{"ok": true}}
	node := NewToolNode("email_node", tool, ToolNodeConfig{OutputKey: "out"})

	var events []runtime.Event
	policyCtx := toolpolicy.ContextWithPolicyEvaluator(context.Background(),
		toolpolicy.PolicyEvaluatorFunc(func(string) toolpolicy.PolicyDecision {
			return toolpolicy.PolicyNeedsApproval
		}))
	runCtx := runtime.ContextWithEmitter(policyCtx, func(e runtime.Event) { events = append(events, e) })
	runCtx = approvalBridgeContext(runCtx, nil) // nil: no bridge

	env := core.NewEnvelope()
	_, err := node.Run(runCtx, env)
	if err == nil {
		t.Fatal("no-bridge approval requirement must hard-block")
	}
	if !toolpolicy.IsApprovalRequired(err) {
		t.Fatalf("hard-block error = %v, want an approval-required wrap", err)
	}
	if got := len(tool.calls); got != 0 {
		t.Fatalf("tool invoked %d times, want 0", got)
	}
}

func TestToolNode_ApprovalPendingPausesRun(t *testing.T) {
	tool := &mockPetalTool{name: "email-send", result: map[string]any{"ok": true}}
	node := NewToolNode("email_node", tool, ToolNodeConfig{OutputKey: "out"})

	var events []runtime.Event
	policyCtx := toolpolicy.ContextWithPolicyEvaluator(context.Background(),
		toolpolicy.PolicyEvaluatorFunc(func(string) toolpolicy.PolicyDecision {
			return toolpolicy.PolicyNeedsApproval
		}))
	runCtx := runtime.ContextWithEmitter(policyCtx, func(e runtime.Event) { events = append(events, e) })
	runCtx = approvalBridgeContext(runCtx, func(_ context.Context, _ any) (any, error) {
		return nil, &ToolApprovalPendingError{Request: &ToolApprovalRequest{ID: "appr-1", ToolName: "email-send"}}
	})

	env := core.NewEnvelope()
	_, err := node.Run(runCtx, env)
	var pendingErr *ToolApprovalPendingError
	if !errors.As(err, &pendingErr) {
		t.Fatalf("pending pause error = %v, want ToolApprovalPendingError", err)
	}
	if got := len(tool.calls); got != 0 {
		t.Fatalf("tool invoked %d times, want 0", got)
	}
	var requested bool
	for _, e := range events {
		if e.Kind == runtime.EventApprovalRequested {
			requested = true
		}
	}
	if !requested {
		t.Fatalf("approval.requested event missing, got %d events", len(events))
	}
}

func TestToolNode_ApprovalBridgeFailurePaths(t *testing.T) {
	tool := &mockPetalTool{name: "email-send", result: map[string]any{"ok": true}}
	node := NewToolNode("email_node", tool, ToolNodeConfig{OutputKey: "out"})
	policyCtx := toolpolicy.ContextWithPolicyEvaluator(context.Background(),
		toolpolicy.PolicyEvaluatorFunc(func(string) toolpolicy.PolicyDecision {
			return toolpolicy.PolicyNeedsApproval
		}))

	cases := []struct {
		name         string
		bridgeResult func() (any, error)
		wantErr      string
	}{
		{
			name:         "plain failure",
			bridgeResult: func() (any, error) { return nil, errors.New("store down") },
			wantErr:      "approval flow failed",
		},
		{
			name:         "wrong payload type",
			bridgeResult: func() (any, error) { return "not a response", nil },
			wantErr:      "want *ToolApprovalResponse",
		},
		{
			name: "unrecognized decision fails closed",
			bridgeResult: func() (any, error) {
				return &ToolApprovalResponse{Decision: "maybe", DecisionBy: "op"}, nil
			},
			wantErr: "blocked: approval",
		},
		{
			name:         "nil payload",
			bridgeResult: func() (any, error) { return nil, nil },
			wantErr:      "want *ToolApprovalResponse",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var events []runtime.Event
			runCtx := runtime.ContextWithEmitter(policyCtx, func(e runtime.Event) { events = append(events, e) })
			runCtx = approvalBridgeContext(runCtx, func(_ context.Context, _ any) (any, error) {
				return tc.bridgeResult()
			})
			env := core.NewEnvelope()
			_, err := node.Run(runCtx, env)
			if err == nil || !contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
			if got := len(tool.calls); got != 0 {
				t.Fatalf("tool invoked %d times, want 0", got)
			}
		})
	}
}

func TestToolNode_ApprovalRejectedEvent(t *testing.T) {
	tool := &mockPetalTool{name: "email-send", result: map[string]any{"ok": true}}
	node := NewToolNode("email_node", tool, ToolNodeConfig{OutputKey: "out"})

	var events []runtime.Event
	policyCtx := toolpolicy.ContextWithPolicyEvaluator(context.Background(),
		toolpolicy.PolicyEvaluatorFunc(func(string) toolpolicy.PolicyDecision {
			return toolpolicy.PolicyNeedsApproval
		}))
	runCtx := runtime.ContextWithEmitter(policyCtx, func(e runtime.Event) { events = append(events, e) })
	runCtx = approvalBridgeContext(runCtx, func(_ context.Context, _ any) (any, error) {
		return &ToolApprovalResponse{Decision: ToolApprovalDecisionRejected, DecisionBy: "reop"}, nil
	})

	env := core.NewEnvelope()
	_, err := node.Run(runCtx, env)
	if !errors.Is(err, ErrToolApprovalRejected) {
		t.Fatalf("rejection error = %v, want ErrToolApprovalRejected", err)
	}
	decided := 0
	for _, e := range events {
		if e.Kind == runtime.EventApprovalDecided {
			decided++
			if e.Payload["decided_by"] != "reop" {
				t.Fatalf("decided payload = %v", e.Payload)
			}
		}
	}
	if decided != 1 {
		t.Fatalf("decided events = %d, want 1", decided)
	}
}

func TestToolApprovalTypes(t *testing.T) {
	pending := &ToolApprovalPendingError{Request: &ToolApprovalRequest{ID: "a", ToolName: "b"}}
	if !pending.Pending() || pending.Error() == "" {
		t.Fatal("pending error interface")
	}
	if (&ToolApprovalPendingError{}).Error() != "tool approval is pending" {
		t.Fatalf("empty pending error = %q", (&ToolApprovalPendingError{}).Error())
	}

	resp := ToolApprovalResponse{Decision: ToolApprovalDecisionApproved}
	if !resp.Approved() {
		t.Fatal("approved response")
	}
	resp.Decision = ToolApprovalDecisionRejected
	if resp.Approved() {
		t.Fatal("rejected response must not be approved")
	}

	base, action := parseApprovalReference("email-send.send")
	if base != "email-send" || action != "send" {
		t.Fatalf("reference parse = %q/%q", base, action)
	}
	base, action = parseApprovalReference("email-send")
	if base != "email-send" || action != "" {
		t.Fatalf("plain reference parse = %q/%q", base, action)
	}
}

func TestGuardianApprovalCheck(t *testing.T) {
	node := NewGuardianNode("guardian", GuardianNodeConfig{
		OnFail: GuardianActionFail,
		Checks: []GuardianCheck{{Name: "grant", Type: GuardianCheckApproval, ApprovalTool: "http_fetch"}},
	})

	grantEnv := func(value any) *core.Envelope {
		env := core.NewEnvelope()
		env.SetVar("approval", value)
		return env
	}

	verifierErr := func(err error) runtime.ToolApprovalVerifier {
		return func(_ context.Context, _, _, _ string) error { return err }
	}

	cases := []struct {
		name      string
		verifier  runtime.ToolApprovalVerifier
		value     any
		wantCheck bool
	}{
		{name: "valid grant", verifier: verifierErr(nil), value: "grant-id", wantCheck: false},
		{name: "invalid id", verifier: verifierErr(errors.New("not found")), value: "wrong-id", wantCheck: true},
		{name: "no verifier", value: "grant-id", wantCheck: true},
		{name: "empty value with verifier", verifier: verifierErr(nil), value: "", wantCheck: true},
		{name: "grant object", verifier: verifierErr(nil), value: map[string]any{"approval_id": "obj", "tool": "http_fetch"}, wantCheck: false},
		{name: "grant object missing id", verifier: verifierErr(nil), value: map[string]any{"tool": "http_fetch"}, wantCheck: true},
		{name: "non-map grant object", verifier: verifierErr(nil), value: 42, wantCheck: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.verifier != nil {
				ctx = runtime.ContextWithToolApprovalVerifier(ctx, tc.verifier)
			}
			failures, err := node.checkApproval(ctx, node.Config().Checks[0], grantEnv(tc.value).Vars["approval"], grantEnv(tc.value))
			if err != nil {
				t.Fatalf("checkApproval(): %v", err)
			}
			if (len(failures) > 0) != tc.wantCheck {
				t.Fatalf("failures = %+v, wantCheck=%v", failures, tc.wantCheck)
			}
		})
	}

	// Tool mismatch: verifier error surfaces with its message.
	ctx := runtime.ContextWithToolApprovalVerifier(context.Background(), verifierErr(fmt.Errorf("consumed")))
	failures, err := node.checkApproval(ctx, node.Config().Checks[0], "grant-id", grantEnv("grant-id"))
	if err != nil || len(failures) == 0 || failures[0].Message == "" {
		t.Fatalf("mismatch failures = %+v err=%v", failures, err)
	}
	_ = time.Now
}
