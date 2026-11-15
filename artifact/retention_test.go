package artifact_test

import (
	"testing"
	"time"

	"github.com/petal-labs/petalflow/artifact"
)

func TestRetentionPolicyResolvePrecedence(t *testing.T) {
	policy := artifact.RetentionPolicy{
		Default:   30 * 24 * time.Hour,
		Tenants:   map[string]time.Duration{"tenant-a": 14 * 24 * time.Hour},
		Workflows: map[string]time.Duration{"workflow-a": 24 * time.Hour},
	}

	tests := []struct {
		name, tenant, workflow string
		want                   time.Duration
	}{
		{name: "default", tenant: "tenant-b", workflow: "workflow-b", want: 30 * 24 * time.Hour},
		{name: "tenant", tenant: "tenant-a", workflow: "workflow-b", want: 14 * 24 * time.Hour},
		{name: "workflow", tenant: "tenant-a", workflow: "workflow-a", want: 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.Resolve(tt.tenant, tt.workflow); got != tt.want {
				t.Fatalf("Resolve(%q, %q) = %s, want %s", tt.tenant, tt.workflow, got, tt.want)
			}
		})
	}
}

func TestRetentionPolicyValidate(t *testing.T) {
	tests := []artifact.RetentionPolicy{
		{Default: -time.Second},
		{Tenants: map[string]time.Duration{"bad tenant": time.Hour}},
		{Tenants: map[string]time.Duration{"tenant": -time.Second}},
		{Workflows: map[string]time.Duration{"bad workflow": time.Hour}},
		{Workflows: map[string]time.Duration{"workflow": -time.Second}},
	}
	for _, policy := range tests {
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil", policy)
		}
	}
}

func TestRetentionPolicyCloneDoesNotAliasOverrides(t *testing.T) {
	original := artifact.RetentionPolicy{Tenants: map[string]time.Duration{"tenant": time.Hour}}
	cloned := original.Clone()
	original.Tenants["tenant"] = 2 * time.Hour
	if got := cloned.Resolve("tenant", ""); got != time.Hour {
		t.Fatalf("cloned retention changed to %s", got)
	}
}
