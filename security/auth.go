// Package security contains shared authentication and outbound-request
// controls used by PetalFlow's HTTP-facing components.
package security

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrMissingIdentity    = errors.New("request identity is incomplete")
)

// Identity is the authenticated caller and its resource-ownership scope.
type Identity struct {
	Subject  string
	TenantID string
	Roles    []string
}

// Authenticator resolves a request into an identity. Implementations should
// validate credentials without returning secret material in errors.
type Authenticator func(*http.Request) (Identity, error)

type identityContextKey struct{}

// BearerTokenAuthenticator creates an authenticator for a token supplied by a
// secret manager or environment variable. The token is never included in an
// error or response.
func BearerTokenAuthenticator(token string, identity Identity) Authenticator {
	return func(r *http.Request) (Identity, error) {
		if r == nil {
			return Identity{}, ErrInvalidCredentials
		}
		provided := strings.TrimSpace(r.Header.Get("Authorization"))
		const prefix = "Bearer "
		if len(provided) < len(prefix) || !strings.EqualFold(provided[:len(prefix)], prefix) {
			return Identity{}, ErrInvalidCredentials
		}
		providedToken := strings.TrimSpace(provided[len(prefix):])
		if token == "" || subtle.ConstantTimeCompare([]byte(providedToken), []byte(token)) != 1 {
			return Identity{}, ErrInvalidCredentials
		}
		if strings.TrimSpace(identity.Subject) == "" || strings.TrimSpace(identity.TenantID) == "" {
			return Identity{}, ErrMissingIdentity
		}
		return cloneIdentity(identity), nil
	}
}

// ContextWithIdentity attaches a verified request identity to a context.
func ContextWithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, cloneIdentity(identity))
}

// IdentityFromContext retrieves a verified request identity.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	if !ok {
		return Identity{}, false
	}
	return cloneIdentity(identity), true
}

func cloneIdentity(identity Identity) Identity {
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.TenantID = strings.TrimSpace(identity.TenantID)
	identity.Roles = append([]string(nil), identity.Roles...)
	return identity
}

func (identity Identity) HasRole(role string) bool {
	for _, candidate := range identity.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func (identity Identity) String() string {
	return fmt.Sprintf("%s/%s", identity.TenantID, identity.Subject)
}
