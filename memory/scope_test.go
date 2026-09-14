package memory

import (
	"context"
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
		{name: "valid minimal", scope: Scope{Namespace: "support", SessionID: "sess-1"}},
		{name: "valid full", scope: Scope{TenantID: "acme", Namespace: "support", SessionID: "sess-1", ThreadID: "t-1", RunID: "run-1"}},
		{name: "missing namespace", scope: Scope{SessionID: "sess-1"}, wantErr: true},
		{name: "session optional for base validation", scope: Scope{Namespace: "support"}},
		{name: "whitespace only namespace", scope: Scope{Namespace: "  ", SessionID: "sess-1"}, wantErr: true},
		{name: "surrounding whitespace", scope: Scope{Namespace: "support", SessionID: " sess-1"}, wantErr: true},
		{name: "control character", scope: Scope{Namespace: "support", SessionID: "sess\n1"}, wantErr: true},
		{name: "too long", scope: Scope{Namespace: "support", SessionID: strings.Repeat("a", MaxIdentifierLength+1)}, wantErr: true},
		{name: "unicode allowed", scope: Scope{Namespace: "サポート", SessionID: "sess-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.scope.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !errors.Is(err, ErrInvalidScope) {
					t.Fatalf("error %v should wrap ErrInvalidScope", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestScopeValidateConversation(t *testing.T) {
	if err := (Scope{Namespace: "support"}).ValidateConversation(); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("missing session = %v, want ErrInvalidScope", err)
	}
	if err := (Scope{SessionID: "s"}).ValidateConversation(); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("missing namespace = %v, want ErrInvalidScope", err)
	}
	if err := (Scope{Namespace: "support", SessionID: "s"}).ValidateConversation(); err != nil {
		t.Fatalf("valid = %v", err)
	}
}

func TestScopeHelpers(t *testing.T) {
	s := Scope{TenantID: " acme ", Namespace: "support", SessionID: "sess-1"}
	if s.IsZero() {
		t.Fatal("scope should not be zero")
	}
	if !(Scope{}).IsZero() {
		t.Fatal("empty scope should be zero")
	}
	if got := s.Normalized().TenantID; got != "acme" {
		t.Fatalf("Normalized tenant = %q", got)
	}
	if got := s.ConversationKey(); got != "sess-1" {
		t.Fatalf("ConversationKey without thread = %q", got)
	}
	s.ThreadID = "thread-9"
	if got := s.ConversationKey(); got != "thread-9" {
		t.Fatalf("ConversationKey with thread = %q", got)
	}
	if got := s.WithNamespace("docs").Namespace; got != "docs" {
		t.Fatalf("WithNamespace = %q", got)
	}
	if got := s.WithNamespace("  ").Namespace; got != "support" {
		t.Fatalf("WithNamespace blank should keep original, got %q", got)
	}
	attrs := s.Attributes()
	if attrs["namespace"] != "support" || attrs["thread_id"] != "thread-9" {
		t.Fatalf("Attributes = %v", attrs)
	}
	if _, ok := attrs["run_id"]; ok {
		t.Fatal("unset run_id should be omitted from attributes")
	}
}

func TestScopeContext(t *testing.T) {
	if _, ok := ScopeFromContext(context.Background()); ok {
		t.Fatal("empty context should not carry a scope")
	}
	want := Scope{Namespace: "ns", SessionID: "s", RunID: "r"}
	ctx := ContextWithScope(context.Background(), want)
	got, ok := ScopeFromContext(ctx)
	if !ok || got != want {
		t.Fatalf("ScopeFromContext = %+v, %v", got, ok)
	}
}

func TestUnavailableClassification(t *testing.T) {
	if Unavailable(nil) != nil {
		t.Fatal("Unavailable(nil) should be nil")
	}
	base := errors.New("dial tcp: connection refused")
	wrapped := Unavailable(base)
	if !errors.Is(wrapped, ErrUnavailable) || !errors.Is(wrapped, base) {
		t.Fatalf("wrapped error should match both sentinel and cause: %v", wrapped)
	}
	if Unavailable(wrapped) != wrapped {
		t.Fatal("double wrapping should be a no-op")
	}
	if !IsUnavailable(context.DeadlineExceeded) || !IsUnavailable(context.Canceled) {
		t.Fatal("context errors should be unavailable")
	}
	if IsUnavailable(errors.New("bad request")) || IsUnavailable(nil) {
		t.Fatal("plain errors and nil are not unavailable")
	}
}

func TestParseFailurePolicy(t *testing.T) {
	if p, err := ParseFailurePolicy(""); err != nil || p != FailurePolicyFail {
		t.Fatalf("empty policy = %q, %v", p, err)
	}
	if p, err := ParseFailurePolicy("continue"); err != nil || p != FailurePolicyContinue {
		t.Fatalf("continue policy = %q, %v", p, err)
	}
	if _, err := ParseFailurePolicy("retry"); err == nil {
		t.Fatal("unknown policy should error")
	}
}

func TestProviderName(t *testing.T) {
	if got := ProviderName(NewInMemoryProvider()); got != "inmemory" {
		t.Fatalf("ProviderName = %q", got)
	}
	if got := ProviderName(struct{}{}); got != "unknown" {
		t.Fatalf("ProviderName for unnamed = %q", got)
	}
}
