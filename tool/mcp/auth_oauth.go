package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	oauthDefaultHeader   = "Authorization"
	oauthDefaultScheme   = "Bearer"
	oauthTokenExpirySkew = 30 * time.Second
	oauthRequestTimeout  = 15 * time.Second
)

// StaticTokenAuthorizer attaches a fixed bearer-style token from the
// process configuration. It never refreshes; tokens are expected to come
// from encrypted registration config, not URLs or logs.
type StaticTokenAuthorizer struct {
	Header string // defaults to Authorization
	Scheme string // defaults to Bearer
	Token  string
}

// Authorize attaches the static token to the request.
func (a *StaticTokenAuthorizer) Authorize(ctx context.Context, req *http.Request) error {
	if req == nil {
		return errors.New("mcp: request is nil")
	}
	header := a.Header
	if strings.TrimSpace(header) == "" {
		header = oauthDefaultHeader
	}
	scheme := a.Scheme
	if strings.TrimSpace(scheme) == "" {
		scheme = oauthDefaultScheme
	}
	if strings.TrimSpace(a.Token) == "" {
		return errors.New("mcp: authorization token is empty")
	}
	req.Header.Set(header, scheme+" "+a.Token)
	return nil
}

// Refresh is a no-op for static tokens.
func (a *StaticTokenAuthorizer) Refresh(ctx context.Context) error {
	return errors.New("mcp: static authorization token cannot be refreshed")
}

// OAuth2Config configures an OAuth 2.1-compatible authorization flow for a
// protected HTTP MCP server. Client credentials are held only in process
// memory and encrypted registration configuration; they are never placed in
// URLs, logs, or error text.
type OAuth2Config struct {
	// TokenURL is the authorization server token endpoint. When empty it
	// is discovered through protected resource / authorization server
	// metadata (RFC 9728 / RFC 8414).
	TokenURL string
	ClientID string
	// ClientSecret is the confidential client secret.
	ClientSecret string
	// Scopes requested from the authorization server.
	Scopes []string
	// Resource is the RFC 8707 resource indicator for the MCP server.
	Resource string
	// Endpoint is the MCP server URL, used as the metadata discovery base
	// when TokenURL is not provided.
	Endpoint string
	Client   *http.Client
}

// OAuth2Authorizer implements an OAuth 2.1-compatible client credentials
// flow with token caching, expiry-aware refresh, and metadata discovery.
// It suits headless machine-to-machine deployments such as the PetalFlow
// daemon; interactive authorization code + PKCE flows belong to users of
// the CLI, not the daemon runtime.
type OAuth2Authorizer struct {
	cfg OAuth2Config

	mu            sync.Mutex
	cachedToken   *oauthToken
	discoveryDone bool
	tokenURL      string
}

type oauthToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	expiresAt    time.Time
}

type protectedResourceMetadata struct {
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type authorizationServerMetadata struct {
	TokenEndpoint            string   `json:"token_endpoint"`
	GrantTypesSupported      []string `json:"grant_types_supported"`
	TokenEndpointAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
}

// NewOAuth2Authorizer creates an OAuth 2.1 authorizer. Metadata discovery
// and token retrieval are deferred until first use.
func NewOAuth2Authorizer(cfg OAuth2Config) (*OAuth2Authorizer, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" && strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, errors.New("mcp: oauth2 requires an endpoint or token url")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, errors.New("mcp: oauth2 client_id is required")
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: oauthRequestTimeout}
	}
	return &OAuth2Authorizer{cfg: cfg}, nil
}

// Authorize attaches a valid access token to the request, obtaining one via
// the OAuth 2.1 client credentials grant when needed.
func (a *OAuth2Authorizer) Authorize(ctx context.Context, req *http.Request) error {
	if req == nil {
		return errors.New("mcp: request is nil")
	}
	token, err := a.validToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set(oauthDefaultHeader, oauthDefaultScheme+" "+token.AccessToken)
	return nil
}

// Refresh invalidates the cached token and fetches a new one.
func (a *OAuth2Authorizer) Refresh(ctx context.Context) error {
	a.mu.Lock()
	a.cachedToken = nil
	a.mu.Unlock()
	_, err := a.fetchToken(ctx)
	return err
}

// validToken returns a cached token or fetches a new one.
func (a *OAuth2Authorizer) validToken(ctx context.Context) (*oauthToken, error) {
	a.mu.Lock()
	token := a.cachedToken
	a.mu.Unlock()
	if token != nil && time.Now().Before(token.expiresAt) {
		return token, nil
	}
	return a.fetchToken(ctx)
}

func (a *OAuth2Authorizer) fetchToken(ctx context.Context) (*oauthToken, error) {
	tokenURL, err := a.resolveTokenURL(ctx)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	if len(a.cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(a.cfg.Scopes, " "))
	}
	if strings.TrimSpace(a.cfg.Resource) != "" {
		form.Set("resource", a.cfg.Resource)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("mcp: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", contentTypeJSON)
	req.SetBasicAuth(a.cfg.ClientID, a.cfg.ClientSecret)

	resp, err := a.cfg.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: token request failed: %w", err)
	}
	defer drainAndClose(resp.Body)

	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("mcp: read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mcp: token endpoint returned status %d", resp.StatusCode)
	}

	var token oauthToken
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("mcp: decode token response: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("mcp: token response missing access_token")
	}

	expiresIn := time.Duration(token.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = time.Minute
	}
	token.expiresAt = time.Now().Add(expiresIn - oauthTokenExpirySkew)

	a.mu.Lock()
	a.cachedToken = &token
	a.mu.Unlock()
	return &token, nil
}

// resolveTokenURL returns the token endpoint, discovering it through
// protected resource and authorization server metadata when not configured.
func (a *OAuth2Authorizer) resolveTokenURL(ctx context.Context) (string, error) {
	a.mu.Lock()
	if a.tokenURL != "" {
		url := a.tokenURL
		a.mu.Unlock()
		return url, nil
	}
	if a.discoveryDone {
		a.mu.Unlock()
		return "", errors.New("mcp: oauth2 token endpoint unavailable after discovery")
	}
	a.discoveryDone = true
	a.mu.Unlock()

	tokenURL := strings.TrimSpace(a.cfg.TokenURL)
	if tokenURL == "" {
		discovered, err := a.discoverTokenURL(ctx)
		if err != nil {
			return "", err
		}
		tokenURL = discovered
	}

	a.mu.Lock()
	a.tokenURL = tokenURL
	a.mu.Unlock()
	return tokenURL, nil
}

// discoverTokenURL follows the MCP authorization discovery chain:
//  1. RFC 9728 protected resource metadata under the endpoint
//  2. RFC 8414 authorization server metadata for each listed AS
func (a *OAuth2Authorizer) discoverTokenURL(ctx context.Context) (string, error) {
	base := strings.TrimSpace(a.cfg.Endpoint)
	if base == "" {
		return "", errors.New("mcp: oauth2 discovery requires an endpoint")
	}

	if servers, err := a.discoverAuthorizationServers(ctx, base); err == nil {
		for _, server := range servers {
			if tokenURL := a.fetchAuthorizationServerTokenEndpoint(ctx, server); tokenURL != "" {
				return tokenURL, nil
			}
		}
	}

	// Fall back to treating the endpoint host itself as the authorization
	// server, which is the common single-origin deployment.
	if tokenURL := a.fetchAuthorizationServerTokenEndpoint(ctx, base); tokenURL != "" {
		return tokenURL, nil
	}
	return "", errors.New("mcp: oauth2 metadata discovery did not yield a token endpoint")
}

func (a *OAuth2Authorizer) discoverAuthorizationServers(ctx context.Context, base string) ([]string, error) {
	for _, wellKnown := range wellKnownPaths(base, "oauth-protected-resource") {
		var metadata protectedResourceMetadata
		if err := a.fetchMetadata(ctx, wellKnown, &metadata); err != nil {
			continue
		}
		if len(metadata.AuthorizationServers) > 0 {
			return metadata.AuthorizationServers, nil
		}
	}
	return nil, errors.New("mcp: no protected resource metadata")
}

func (a *OAuth2Authorizer) fetchAuthorizationServerTokenEndpoint(ctx context.Context, server string) string {
	for _, wellKnown := range wellKnownPaths(server, "oauth-authorization-server") {
		var metadata authorizationServerMetadata
		if err := a.fetchMetadata(ctx, wellKnown, &metadata); err != nil {
			continue
		}
		if strings.TrimSpace(metadata.TokenEndpoint) != "" {
			return metadata.TokenEndpoint
		}
	}
	return ""
}

// wellKnownPaths returns the root and path-scoped well-known URLs for a
// metadata type, per RFC 8414 section 3.1.
func wellKnownPaths(base, name string) []string {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil
	}
	root := parsed.Scheme + "://" + parsed.Host
	paths := []string{
		root + "/.well-known/" + name,
		root + "/.well-known/" + name + parsed.Path,
	}
	return paths
}

func (a *OAuth2Authorizer) fetchMetadata(ctx context.Context, wellKnownURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", contentTypeJSON)

	resp, err := a.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer drainAndClose(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mcp: metadata returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
