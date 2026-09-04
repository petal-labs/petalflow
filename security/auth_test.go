package security

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestBearerTokenAuthenticatorUsesConstantTimeValidation(t *testing.T) {
	auth := BearerTokenAuthenticator("secret-token", Identity{Subject: "user-1", TenantID: "tenant-a"})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	identity, err := auth(req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Subject != "user-1" || identity.TenantID != "tenant-a" {
		t.Fatalf("identity = %#v", identity)
	}

	req.Header.Set("Authorization", "Bearer wrong-token")
	if _, err := auth(req); err == nil {
		t.Fatal("expected invalid token error")
	}
	for _, authorization := range []string{"", "Basic secret-token", "Bearer"} {
		req.Header.Set("Authorization", authorization)
		if _, err := auth(req); err == nil {
			t.Errorf("authorization %q was accepted", authorization)
		}
	}
	if _, err := BearerTokenAuthenticator("", Identity{Subject: "u", TenantID: "t"})(req); err == nil {
		t.Fatal("empty configured token was accepted")
	}
	req.Header.Set("Authorization", "Bearer token")
	if _, err := BearerTokenAuthenticator("token", Identity{})(req); err == nil {
		t.Fatal("incomplete identity was accepted")
	}
}

func TestIdentityContextClonesRoles(t *testing.T) {
	original := Identity{Subject: "user-1", TenantID: "tenant-a", Roles: []string{"reader"}}
	ctx := ContextWithIdentity(t.Context(), original)
	original.Roles[0] = "admin"

	got, ok := IdentityFromContext(ctx)
	if !ok || got.Roles[0] != "reader" {
		t.Fatalf("identity from context = %#v, want isolated roles", got)
	}
}

func TestIdentityHelpers(t *testing.T) {
	identity := Identity{Subject: "user", TenantID: "tenant", Roles: []string{"reader"}}
	if !identity.HasRole("reader") || identity.HasRole("admin") {
		t.Fatal("HasRole returned an incorrect result")
	}
	if got := identity.String(); got != "tenant/user" {
		t.Fatalf("String() = %q, want tenant/user", got)
	}
	if _, ok := IdentityFromContext(context.Background()); ok {
		t.Fatal("IdentityFromContext found an identity in a plain context")
	}
}
