package memory

import (
	"errors"
	"strings"
	"testing"
)

func TestScopeValidate(t *testing.T) {
	tests := []struct {
		name    string
		scope   Scope
		wantErr bool
	}{
		{
			name:  "valid minimal scope",
			scope: Scope{Namespace: "support", SessionID: "sess-123"},
		},
		{
			name:  "valid full scope",
			scope: Scope{TenantID: "acme.corp", Namespace: "support", SessionID: "sess-123", ThreadID: "t.1", RunID: "run_01"},
		},
		{
			name:    "missing namespace",
			scope:   Scope{SessionID: "sess-123"},
			wantErr: true,
		},
		{
			name:    "missing session",
			scope:   Scope{Namespace: "support"},
			wantErr: true,
		},
		{
			name:    "namespace with slash",
			scope:   Scope{Namespace: "ns/evil", SessionID: "sess"},
			wantErr: true,
		},
		{
			name:    "session with space",
			scope:   Scope{Namespace: "ns", SessionID: "has space"},
			wantErr: true,
		},
		{
			name:    "oversized identifier",
			scope:   Scope{Namespace: "ns", SessionID: strings.Repeat("a", 257)},
			wantErr: true,
		},
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

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{strings.Repeat("a", 400), 100},
	}
	for _, tt := range tests {
		if got := EstimateTokens(tt.text); got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

func TestErrUnavailableWrapping(t *testing.T) {
	wrapped := error(errors.New("connection refused"))
	joined := errors.Join(ErrUnavailable, wrapped)
	if !errors.Is(joined, ErrUnavailable) {
		t.Fatal("errors.Is should detect wrapped ErrUnavailable")
	}
}
