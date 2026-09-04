package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/petal-labs/petalflow/nodes"
	"github.com/petal-labs/petalflow/security"
)

func TestSecurity_WebhookHMACRejectsReplay(t *testing.T) {
	srv := NewServer(ServerConfig{Security: SecurityConfig{WebhookReplayWindow: time.Minute}})
	body := []byte(`{"event":"created"}`)
	timestamp := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(fmt.Sprintf("%d.", timestamp)))
	_, _ = mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("X-PetalFlow-Timestamp", fmt.Sprintf("%d", timestamp))
	req.Header.Set("X-PetalFlow-Signature", hex.EncodeToString(mac.Sum(nil)))
	cfg := nodes.WebhookTriggerNodeConfig{Auth: nodes.WebhookTriggerAuthConfig{
		Type: nodes.WebhookAuthTypeHMACSHA256, Token: "secret",
		SignatureHeader: "X-PetalFlow-Signature", TimestampHeader: "X-PetalFlow-Timestamp", ReplayWindow: time.Minute,
	}}
	if err := srv.authorizeWebhookRequest(req, cfg, body); err != nil {
		t.Fatalf("first HMAC request rejected: %v", err)
	}
	if err := srv.authorizeWebhookRequest(req, cfg, body); err == nil {
		t.Fatal("replayed HMAC request accepted")
	}
}

type errorWorkflowStore struct{ err error }

func (s errorWorkflowStore) List(context.Context) ([]WorkflowRecord, error) { return nil, s.err }
func (s errorWorkflowStore) Get(context.Context, string) (WorkflowRecord, bool, error) {
	return WorkflowRecord{}, false, s.err
}
func (s errorWorkflowStore) Create(context.Context, WorkflowRecord) error { return s.err }
func (s errorWorkflowStore) Update(context.Context, WorkflowRecord) error { return s.err }
func (s errorWorkflowStore) Delete(context.Context, string) error         { return s.err }

func TestSecurity_RequiresBearerAuthAndKeepsHealthPublic(t *testing.T) {
	srv := NewServer(ServerConfig{
		Store: newTestWorkflowStore(t),
		Security: SecurityConfig{
			RequireAuth:   true,
			Authenticator: security.BearerTokenAuthenticator("test-token", security.Identity{Subject: "user", TenantID: "tenant-a"}),
		},
	})

	health := httptest.NewRecorder()
	srv.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}

	unauthenticated := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/workflows", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthenticated.Code)
	}

	authenticatedReq := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	authenticatedReq.Header.Set("Authorization", "Bearer test-token")
	authenticated := httptest.NewRecorder()
	srv.Handler().ServeHTTP(authenticated, authenticatedReq)
	if authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", authenticated.Code)
	}
}

func TestSecurity_DefaultCORSIsDisabledAndAllowlistIsExact(t *testing.T) {
	withoutCORS := NewServer(ServerConfig{Store: newTestWorkflowStore(t)})
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp := httptest.NewRecorder()
	withoutCORS.Handler().ServeHTTP(resp, req)
	if got := resp.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("default CORS origin = %q, want empty", got)
	}

	allowlisted := NewServer(ServerConfig{
		Store:    newTestWorkflowStore(t),
		Security: SecurityConfig{CORSOrigins: []string{"https://app.example"}},
	})
	allowedReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	allowedReq.Header.Set("Origin", "https://app.example")
	allowedResp := httptest.NewRecorder()
	allowlisted.Handler().ServeHTTP(allowedResp, allowedReq)
	if got := allowedResp.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("allowlisted CORS origin = %q", got)
	}
	blockedReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	blockedReq.Header.Set("Origin", "https://evil.example")
	blockedResp := httptest.NewRecorder()
	allowlisted.Handler().ServeHTTP(blockedResp, blockedReq)
	if got := blockedResp.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unallowlisted CORS origin = %q", got)
	}
}

func TestSecurity_CrossTenantWorkflowIsHidden(t *testing.T) {
	store := newTestWorkflowStore(t)
	auth := security.BearerTokenAuthenticator("token-a", security.Identity{Subject: "a", TenantID: "tenant-a"})
	srv := NewServer(ServerConfig{Store: store, Security: SecurityConfig{RequireAuth: true, Authenticator: auth}})
	handler := srv.Handler()
	body := []byte(`{"id":"tenant-workflow","version":"1.0","nodes":[{"id":"start","type":"func"}],"edges":[],"entry":"start"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/workflows/graph", bytes.NewReader(body))
	createReq.Header.Set("Authorization", "Bearer token-a")
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", createResp.Code, createResp.Body.String())
	}

	otherAuth := security.BearerTokenAuthenticator("token-b", security.Identity{Subject: "b", TenantID: "tenant-b"})
	other := NewServer(ServerConfig{Store: store, Security: SecurityConfig{RequireAuth: true, Authenticator: otherAuth}})
	getReq := httptest.NewRequest(http.MethodGet, "/api/workflows/tenant-workflow", nil)
	getReq.Header.Set("Authorization", "Bearer token-b")
	getResp := httptest.NewRecorder()
	other.Handler().ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant status = %d, want 404", getResp.Code)
	}
}

func TestSecurity_InternalErrorsAreRedacted(t *testing.T) {
	secret := "database-password=super-secret"
	srv := NewServer(ServerConfig{Store: errorWorkflowStore{err: errors.New(secret)}})
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/workflows", nil))
	if strings.Contains(resp.Body.String(), secret) {
		t.Fatalf("internal error leaked: %s", resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
}
