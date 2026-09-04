package security

import (
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
