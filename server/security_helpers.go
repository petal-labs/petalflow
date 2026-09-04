package server

import (
	"net/http"
	"strings"

	"github.com/petal-labs/petalflow/runtime"
	"github.com/petal-labs/petalflow/security"
)

func requestIdentity(r *http.Request) (security.Identity, bool) {
	if r == nil {
		return security.Identity{}, false
	}
	return security.IdentityFromContext(r.Context())
}

func (s *Server) tenantID(r *http.Request) string {
	identity, ok := requestIdentity(r)
	if !ok || !s.security.RequireAuth {
		return ""
	}
	return identity.TenantID
}

func (s *Server) ownsTenant(r *http.Request, resourceTenant string) bool {
	if !s.security.RequireAuth {
		return true
	}
	identity, ok := requestIdentity(r)
	return ok && strings.TrimSpace(resourceTenant) != "" && identity.TenantID == resourceTenant
}

func writeHiddenResource(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "NOT_FOUND", "resource not found")
}

func (s *Server) ownsRun(r *http.Request, record *runtime.RunRecord) bool {
	if record == nil {
		return false
	}
	return s.ownsTenant(r, record.TenantID)
}
