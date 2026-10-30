package tool

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResolveEffectivePolicyPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		tenant   *ToolPolicy
		workflow *ToolPolicy
		tool     string
		want     PolicyDecision
	}{
		{
			name: "no policies denies everything",
			tool: "email-send",
			want: PolicyDeny,
		},
		{
			name: "tenant allow allows listed tool only",
			tenant: &ToolPolicy{
				Allow: []string{"email-send", "web-search"},
			},
			tool: "email-send",
			want: PolicyAllow,
		},
		{
			name: "tenant allow denies unlisted tool",
			tenant: &ToolPolicy{
				Allow: []string{"email-send"},
			},
			tool: "fs-delete",
			want: PolicyDeny,
		},
		{
			name: "tenant deny wins over tenant allow",
			tenant: &ToolPolicy{
				Allow: []string{"email-send"},
				Deny:  []string{"email-send"},
			},
			tool: "email-send",
			want: PolicyDeny,
		},
		{
			name: "no workflow policy falls back to tenant default",
			tenant: &ToolPolicy{
				Allow: []string{"email-send"},
			},
			workflow: nil,
			tool:     "email-send",
			want:     PolicyAllow,
		},
		{
			name: "workflow allow replaces tenant allow",
			tenant: &ToolPolicy{
				Allow: []string{"email-send", "web-search"},
			},
			workflow: &ToolPolicy{
				Allow: []string{"web-search"},
			},
			tool: "email-send",
			want: PolicyDeny,
		},
		{
			name: "empty workflow allow denies everything",
			tenant: &ToolPolicy{
				Allow: []string{"email-send"},
			},
			workflow: &ToolPolicy{
				Allow: []string{},
			},
			tool: "email-send",
			want: PolicyDeny,
		},
		{
			name: "workflow deny extends tenant deny",
			tenant: &ToolPolicy{
				Allow: []string{"email-send", "web-search"},
				Deny:  []string{"email-send"},
			},
			workflow: &ToolPolicy{
				Allow: []string{"email-send", "web-search"},
			},
			tool: "email-send",
			want: PolicyDeny,
		},
		{
			name: "workflow deny cannot be bypassed by workflow allow",
			tenant: &ToolPolicy{
				Allow: []string{"email-send"},
			},
			workflow: &ToolPolicy{
				Allow: []string{"email-send"},
				Deny:  []string{"email-send"},
			},
			tool: "email-send",
			want: PolicyDeny,
		},
		{
			name: "require approval gates allowed tool",
			tenant: &ToolPolicy{
				Allow:           []string{"email-send"},
				RequireApproval: []string{"email-send"},
			},
			tool: "email-send",
			want: PolicyNeedsApproval,
		},
		{
			name: "tenant approval gate survives workflow allow replacement",
			tenant: &ToolPolicy{
				Allow:           []string{"email-send"},
				RequireApproval: []string{"email-send"},
			},
			workflow: &ToolPolicy{
				Allow: []string{"email-send"},
			},
			tool: "email-send",
			want: PolicyNeedsApproval,
		},
		{
			name: "workflow can add approval gate",
			tenant: &ToolPolicy{
				Allow: []string{"email-send"},
			},
			workflow: &ToolPolicy{
				Allow:           []string{"email-send"},
				RequireApproval: []string{"email-send"},
			},
			tool: "email-send",
			want: PolicyNeedsApproval,
		},
		{
			name: "deny wins over require approval",
			tenant: &ToolPolicy{
				Allow:           []string{"email-send"},
				RequireApproval: []string{"email-send"},
				Deny:            []string{"email-send"},
			},
			tool: "email-send",
			want: PolicyDeny,
		},
		{
			name: "empty tool name always denies",
			tenant: &ToolPolicy{
				Allow: []string{"*"},
			},
			tool: "",
			want: PolicyDeny,
		},
		{
			name: "wildcard allow matches any tool",
			tenant: &ToolPolicy{
				Allow: []string{"*"},
			},
			tool: "anything",
			want: PolicyAllow,
		},
		{
			name: "namespace pattern allow matches nested tools",
			tenant: &ToolPolicy{
				Allow: []string{"mcp/*"},
			},
			tool: "mcp/grep",
			want: PolicyAllow,
		},
		{
			name: "namespace pattern allow does not match prefix siblings",
			tenant: &ToolPolicy{
				Allow: []string{"mcp/*"},
			},
			tool: "mcpx/grep",
			want: PolicyDeny,
		},
		{
			name: "namespace pattern deny wins over namespace allow",
			tenant: &ToolPolicy{
				Allow: []string{"fs/*"},
				Deny:  []string{"fs/write"},
			},
			tool: "fs/write",
			want: PolicyDeny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effective := ResolveEffectivePolicy(tt.tenant, tt.workflow)
			if got := effective.Evaluate(tt.tool); got != tt.want {
				t.Fatalf("Evaluate(%q) = %q, want %q", tt.tool, got, tt.want)
			}
		})
	}
}

func TestResolveEffectivePolicyDeterministic(t *testing.T) {
	tenant := &ToolPolicy{Allow: []string{"b", "a", "a", "mcp/*"}, Deny: []string{"z", "z"}}
	workflow := &ToolPolicy{Allow: []string{"c", "b"}, Deny: []string{"y"}}

	first := ResolveEffectivePolicy(tenant, workflow)
	second := ResolveEffectivePolicy(tenant, workflow)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("resolution is not deterministic: %+v vs %+v", first, second)
	}
	if got, want := first.Allow, []string{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow allow did not replace tenant allow and normalize: %v, want %v", got, want)
	}
	if got, want := first.Deny, []string{"y", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deny union did not normalize: %v, want %v", got, want)
	}
}

func TestResolveEffectivePolicyRequireApprovalUnion(t *testing.T) {
	tenant := &ToolPolicy{
		Allow:           []string{"a", "b"},
		RequireApproval: []string{"b"},
	}
	workflow := &ToolPolicy{
		Allow: []string{"b"},
	}

	effective := ResolveEffectivePolicy(tenant, workflow)
	if got := effective.Evaluate("b"); got != PolicyNeedsApproval {
		t.Fatalf("workflow cannot loosen tenant approval gate, got %q", got)
	}
	if got := effective.Evaluate("a"); got != PolicyDeny {
		t.Fatalf("workflow allow list should narrow scope, got %q", got)
	}
}

func TestToolPolicyValidation(t *testing.T) {
	tests := []struct {
		name    string
		policy  ToolPolicy
		wantErr string
	}{
		{
			name:   "valid policy",
			policy: ToolPolicy{Allow: []string{"a", "mcp/*", "*"}, Deny: []string{"b"}, RequireApproval: []string{"c"}},
		},
		{
			name:    "empty rule",
			policy:  ToolPolicy{Allow: []string{"catch", ""}},
			wantErr: "allow",
		},
		{
			name:    "whitespace rule",
			policy:  ToolPolicy{Deny: []string{"fs read"}},
			wantErr: "deny",
		},
		{
			name:    "double star",
			policy:  ToolPolicy{RequireApproval: []string{"fs/**"}},
			wantErr: "require_approval",
		},
		{
			name:    "negative version",
			policy:  ToolPolicy{Version: -1},
			wantErr: "version",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestToolPolicyNormalized(t *testing.T) {
	policy := ToolPolicy{Version: 3, Allow: []string{"b", "a", "b"}}

	got := policy.Normalized()
	if !reflect.DeepEqual(got.Allow, []string{"a", "b"}) {
		t.Fatalf("Normalized() allow = %v", got.Allow)
	}
	if got.Version != 3 {
		t.Fatalf("Normalized() version = %d, want 3", got.Version)
	}

	// nil stays nil; explicit empty stays explicit empty
	empty := ToolPolicy{Allow: []string{}}.Normalized()
	if empty.Allow == nil || len(empty.Allow) != 0 {
		t.Fatalf("Normalized() should preserve explicit empty allow list: %v", empty.Allow)
	}
}

func TestToolPolicyJSONRoundTrip(t *testing.T) {
	updated := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	policy := ToolPolicy{
		Version:         5,
		Allow:           []string{"mcp/*"},
		Deny:            []string{"fs/write"},
		RequireApproval: []string{"email-send"},
		UpdatedAt:       updated,
	}
	wantJSON := `{"version":5,"allow":["mcp/*"],"deny":["fs/write"],"require_approval":["email-send"],"updated_at":"2026-10-29T00:00:00Z"}`

	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != string(wantJSON) {
		t.Fatalf("marshal = %s, want %s", encoded, wantJSON)
	}

	var decoded ToolPolicy
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decoded, policy) {
		t.Fatalf("round trip mismatch: %+v vs %+v", decoded, policy)
	}
}

func TestToolPolicyJSONRoundTripExplicitEmpty(t *testing.T) {
	var decoded ToolPolicy
	if err := json.Unmarshal([]byte(`{"allow":[]}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Allow == nil {
		t.Fatalf("explicit empty allow must survive JSON round trip")
	}
	effective := ResolveEffectivePolicy(nil, &decoded)
	if got := effective.Evaluate("anything"); got != PolicyDeny {
		t.Fatalf("explicit empty allow must deny everything, got %q", got)
	}
}
