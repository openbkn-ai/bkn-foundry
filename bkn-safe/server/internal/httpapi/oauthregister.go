// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package httpapi

// MCP clients (WorkBuddy, Claude Code, Cursor, …) sign in to the Context Loader
// MCP endpoint by the MCP authorization flow: on a 401 they look up the
// protected-resource metadata (RFC 9728), then the authorization server's
// metadata (RFC 8414), register themselves (RFC 7591) and open the browser on
// Hydra's authorize endpoint. Hydra has no protected-resource document, and its
// own dynamic registration would accept any client — client_credentials
// included, whose token agent-retrieval treats as an application account
// without anyone signing in. So bkn-safe serves the two documents and takes
// registrations itself: only public, sign-in-required clients with loopback
// callbacks, always allowed to refresh, created through Hydra's admin API.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
)

const (
	oauthRegisterPath = "/api/safe/v1/oauth/register"
	// mcpResourcePrefix covers Context Loader's /mcp and /mcp-compact endpoints.
	mcpResourcePrefix = "/api/agent-retrieval/v1/mcp"
	// dynamicClientScope is granted to every self-registered client. "offline"
	// is what makes Hydra issue a refresh token, so the agent can renew alone.
	dynamicClientScope    = "openid offline offline_access"
	dynamicClientNameMax  = 64
	registrationBodyLimit = 16 << 10
	issuerMetadataTTL     = 5 * time.Minute
)

// mcpScopes are the scopes an MCP client should request. The MCP SDK takes
// them from the protected-resource document; without "offline" Hydra issues
// no refresh token and the agent has to sign in again every hour.
var mcpScopes = []string{"openid", "offline"}

// DynamicClientRegistrar creates the Hydra client behind a registration.
type DynamicClientRegistrar interface {
	CreateDynamicClient(ctx context.Context, c auth.DynamicClient) (auth.RegisteredClient, error)
}

// OAuthDynamicRegistration configures the MCP sign-in surface.
type OAuthDynamicRegistration struct {
	Registrar DynamicClientRegistrar
	// HydraPublicURL is Hydra's in-cluster public base, read for its discovery document.
	HydraPublicURL string
	// AllowedRedirectOrigins admits callback origins beyond loopback.
	AllowedRedirectOrigins []string
	// RatePerMinute caps registrations per client IP; 0 disables the cap.
	RatePerMinute int
	// HTTPClient fetches Hydra's discovery document; nil uses a 5s client.
	HTTPClient *http.Client
}

type oauthRegistration struct {
	cfg     OAuthDynamicRegistration
	origins map[string]bool
	limiter *registrationLimiter

	mu        sync.Mutex
	metadata  map[string]any
	fetchedAt time.Time
}

func registerOAuthDynamicRegistration(r *gin.Engine, cfg OAuthDynamicRegistration) {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	h := &oauthRegistration{
		cfg:     cfg,
		origins: make(map[string]bool, len(cfg.AllowedRedirectOrigins)),
		limiter: newRegistrationLimiter(cfg.RatePerMinute, time.Minute),
	}
	for _, origin := range cfg.AllowedRedirectOrigins {
		if normalized, ok := normalizedOrigin(origin); ok {
			h.origins[normalized] = true
		}
	}
	r.GET("/.well-known/oauth-authorization-server", h.authorizationServer)
	r.GET("/.well-known/oauth-protected-resource", h.protectedResource)
	r.GET("/.well-known/oauth-protected-resource/*resource", h.protectedResource)
	r.POST(oauthRegisterPath, h.register)
}

// authorizationServer is Hydra's discovery document plus the registration
// endpoint, which Hydra leaves out because its own registration is off.
func (h *oauthRegistration) authorizationServer(c *gin.Context) {
	meta, err := h.hydraMetadata(c.Request.Context())
	if err != nil {
		slog.Error("oauth metadata: hydra discovery failed", "err", err)
		c.JSON(http.StatusBadGateway, oauthError("server_error", "authorization server metadata is unavailable"))
		return
	}
	issuer, _ := meta["issuer"].(string)
	out := make(map[string]any, len(meta)+2)
	for k, v := range meta {
		out[k] = v
	}
	out["registration_endpoint"] = strings.TrimSuffix(issuer, "/") + oauthRegisterPath
	out["code_challenge_methods_supported"] = []string{"S256"}
	c.Header("Cache-Control", "public, max-age=300")
	c.JSON(http.StatusOK, out)
}

// protectedResource describes the Context Loader MCP endpoint. Clients check
// that `resource` matches the URL they connected to, so it is built from the
// request's own origin; the document grants nothing by itself.
func (h *oauthRegistration) protectedResource(c *gin.Context) {
	path := c.Param("resource")
	if path != "" && path != "/" && !strings.HasPrefix(path, mcpResourcePrefix) {
		c.Status(http.StatusNotFound)
		return
	}
	meta, err := h.hydraMetadata(c.Request.Context())
	if err != nil {
		slog.Error("oauth resource metadata: hydra discovery failed", "err", err)
		c.JSON(http.StatusBadGateway, oauthError("server_error", "authorization server metadata is unavailable"))
		return
	}
	issuer, _ := meta["issuer"].(string)
	if path == "" {
		path = "/"
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.JSON(http.StatusOK, gin.H{
		"resource":                 requestOrigin(c) + path,
		"authorization_servers":    []string{strings.TrimSuffix(issuer, "/")},
		"scopes_supported":         mcpScopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "OpenBKN Context Loader",
	})
}

type registrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (h *oauthRegistration) register(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	ip := c.ClientIP()
	if !h.limiter.allow(ip, time.Now()) {
		c.JSON(http.StatusTooManyRequests, oauthError("too_many_requests", "too many client registrations from this address; retry in a minute"))
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, registrationBodyLimit+1))
	if err != nil || len(raw) > registrationBodyLimit {
		c.JSON(http.StatusBadRequest, oauthError("invalid_client_metadata", "request body is missing or too large"))
		return
	}
	var req registrationRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		c.JSON(http.StatusBadRequest, oauthError("invalid_client_metadata", "request body is not valid JSON client metadata"))
		return
	}
	client, code, reason := h.validate(req)
	if code != "" {
		slog.Info("oauth registration rejected", "client_ip", ip, "reason", reason)
		c.JSON(http.StatusBadRequest, oauthError(code, reason))
		return
	}
	client.Metadata = map[string]any{"openbkn_kind": "dynamic", "registered_from": ip}
	created, err := h.cfg.Registrar.CreateDynamicClient(c.Request.Context(), client)
	if err != nil {
		slog.Error("oauth registration: create client failed", "client_ip", ip, "err", err)
		c.JSON(http.StatusInternalServerError, oauthError("server_error", "the client could not be registered"))
		return
	}
	slog.Info("oauth client registered", "client_id", created.ClientID, "client_name", client.Name, "client_ip", ip)
	c.JSON(http.StatusCreated, gin.H{
		"client_id":                  created.ClientID,
		"client_id_issued_at":        created.IssuedAt.Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                client.GrantTypes,
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"scope":                      client.Scope,
	})
}

// validate turns a registration request into the only client shape bkn-safe
// creates, or names what is wrong with it.
func (h *oauthRegistration) validate(req registrationRequest) (auth.DynamicClient, string, string) {
	for _, grant := range req.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return auth.DynamicClient{}, "invalid_client_metadata",
				fmt.Sprintf("grant_type %q is not allowed; clients must sign a user in with authorization_code", grant)
		}
	}
	for _, rt := range req.ResponseTypes {
		if rt != "code" {
			return auth.DynamicClient{}, "invalid_client_metadata", fmt.Sprintf("response_type %q is not allowed; use code", rt)
		}
	}
	if m := req.TokenEndpointAuthMethod; m != "" && m != "none" {
		return auth.DynamicClient{}, "invalid_client_metadata",
			fmt.Sprintf("token_endpoint_auth_method %q is not allowed; only public clients (none) may register", m)
	}
	if len(req.RedirectURIs) == 0 {
		return auth.DynamicClient{}, "invalid_redirect_uri", "redirect_uris is required"
	}
	for _, uri := range req.RedirectURIs {
		if !h.redirectAllowed(uri) {
			return auth.DynamicClient{}, "invalid_redirect_uri",
				fmt.Sprintf("redirect_uri %q is not allowed; use a loopback address (http://127.0.0.1, http://localhost, http://[::1])", uri)
		}
	}
	return auth.DynamicClient{
		Name:         clientName(req.ClientName),
		RedirectURIs: req.RedirectURIs,
		// Always allowed to refresh, even when the client asked only for
		// authorization_code: without it the agent must sign in every hour.
		GrantTypes: []string{"authorization_code", "refresh_token"},
		Scope:      dynamicClientScope,
	}, "", ""
}

// redirectAllowed admits a loopback callback on any port (RFC 8252 §7.3), or
// one whose origin an operator listed. A loopback callback lands on the
// signed-in user's own machine, so a link someone else sends cannot deliver
// the authorization code to them.
func (h *oauthRegistration) redirectAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil || u.Host == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return false
	}
	switch strings.ToLower(u.Hostname()) {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	origin, ok := normalizedOrigin(raw)
	return ok && h.origins[origin]
}

func normalizedOrigin(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, true
}

func clientName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "MCP client"
	}
	if utf8.RuneCountInString(name) > dynamicClientNameMax {
		name = string([]rune(name)[:dynamicClientNameMax])
	}
	return name
}

// requestOrigin is the origin the caller used, as the gateway forwarded it.
func requestOrigin(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(proto, ",")[0]))
	}
	host := c.Request.Host
	if fwd := c.GetHeader("X-Forwarded-Host"); fwd != "" {
		host = strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	return scheme + "://" + host
}

// hydraMetadata reads Hydra's OIDC discovery document, cached for a few minutes.
func (h *oauthRegistration) hydraMetadata(ctx context.Context) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.metadata != nil && time.Since(h.fetchedAt) < issuerMetadataTTL {
		return h.metadata, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimSuffix(h.cfg.HydraPublicURL, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	res, err := h.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hydra discovery: HTTP %d", res.StatusCode)
	}
	var meta map[string]any
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&meta); err != nil {
		return nil, fmt.Errorf("hydra discovery: %w", err)
	}
	if issuer, _ := meta["issuer"].(string); issuer == "" {
		return nil, fmt.Errorf("hydra discovery: no issuer")
	}
	h.metadata, h.fetchedAt = meta, time.Now()
	return meta, nil
}

func oauthError(code, description string) gin.H {
	return gin.H{"error": code, "error_description": description}
}

// registrationLimiter is a fixed-window count per client IP. In memory: one
// bkn-safe replica's view, which is enough to stop a script from filling Hydra.
type registrationLimiter struct {
	limit  int
	window time.Duration

	mu      sync.Mutex
	started time.Time
	counts  map[string]int
}

func newRegistrationLimiter(limit int, window time.Duration) *registrationLimiter {
	return &registrationLimiter{limit: limit, window: window, counts: map[string]int{}}
}

func (l *registrationLimiter) allow(ip string, now time.Time) bool {
	if l.limit <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.started) >= l.window {
		l.started, l.counts = now, map[string]int{}
	}
	if l.counts[ip] >= l.limit {
		return false
	}
	l.counts[ip]++
	return true
}
