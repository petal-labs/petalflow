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
