package artifact_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/petal-labs/petalflow/artifact"
)

func TestScopeValidate(t *testing.T) {
	tests := []struct {
		name    string
		scope   artifact.Scope
		wantErr bool
	}{
		{name: "valid", scope: artifact.Scope{TenantID: "tenant-1", RunID: "run_1"}},
		{name: "missing tenant", scope: artifact.Scope{RunID: "run-1"}, wantErr: true},
		{name: "missing run", scope: artifact.Scope{TenantID: "tenant-1"}, wantErr: true},
		{name: "unsafe tenant", scope: artifact.Scope{TenantID: "../tenant", RunID: "run-1"}, wantErr: true},
		{name: "oversized run", scope: artifact.Scope{TenantID: "tenant-1", RunID: strings.Repeat("r", 257)}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scope.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReferenceValidate(t *testing.T) {
	validDigest := strings.Repeat("a", artifact.SHA256HexLength)
	tests := []struct {
		name    string
		ref     artifact.Reference
		wantErr bool
	}{
		{name: "valid", ref: artifact.Reference{Digest: validDigest, Size: 3, ContentType: "text/plain"}},
		{name: "bad digest", ref: artifact.Reference{Digest: "abc", Size: 3, ContentType: "text/plain"}, wantErr: true},
		{name: "non-hex digest", ref: artifact.Reference{Digest: strings.Repeat("z", artifact.SHA256HexLength), Size: 3, ContentType: "text/plain"}, wantErr: true},
		{name: "negative size", ref: artifact.Reference{Digest: validDigest, Size: -1, ContentType: "text/plain"}, wantErr: true},
		{name: "bad content type", ref: artifact.Reference{Digest: validDigest, Size: 3, ContentType: "not a mime type"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPutRequestValidate(t *testing.T) {
	validScope := artifact.Scope{TenantID: "tenant-1", RunID: "run-1"}
	tests := []struct {
		name       string
		request    artifact.PutRequest
		wantPolicy bool
	}{
		{name: "valid", request: artifact.PutRequest{Scope: validScope, WorkflowID: "workflow-1", NodeID: "node-1", ContentType: "text/plain"}},
		{name: "invalid scope", request: artifact.PutRequest{Scope: artifact.Scope{RunID: "run-1"}, ContentType: "text/plain"}},
		{name: "invalid workflow", request: artifact.PutRequest{Scope: validScope, WorkflowID: "bad/workflow", ContentType: "text/plain"}},
		{name: "invalid node", request: artifact.PutRequest{Scope: validScope, NodeID: "bad/node", ContentType: "text/plain"}},
		{name: "missing content type", request: artifact.PutRequest{Scope: validScope}, wantPolicy: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()
			if tt.name == "valid" && err != nil {
				t.Fatalf("Validate() error = %v, want nil", err)
			}
			if tt.name != "valid" && err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
			if tt.wantPolicy && !errors.Is(err, artifact.ErrPolicyRejected) {
				t.Fatalf("Validate() error = %v, want ErrPolicyRejected", err)
			}
		})
	}
}

func TestSentinelErrorsSupportWrapping(t *testing.T) {
	for _, sentinel := range []error{
		artifact.ErrNotFound,
		artifact.ErrUnavailable,
		artifact.ErrPolicyRejected,
	} {
		if !errors.Is(errors.Join(sentinel, errors.New("detail")), sentinel) {
			t.Fatalf("errors.Is should detect %v", sentinel)
		}
	}
}
