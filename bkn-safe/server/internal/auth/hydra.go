// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	hydra "github.com/ory/hydra-client-go/v2"
)

// HydraAdmin wraps hydra's admin OAuth2 API for the login & consent flow.
// Admin is internal-only; bkn-safe reaches it service-to-service.
type HydraAdmin struct {
	api      *hydra.APIClient
	adminURL string
	http     *http.Client
}

// OAuthClientURIs is the browser-facing URI subset of a Hydra OAuth2 client.
type OAuthClientURIs struct {
	RedirectURIs           []string
	PostLogoutRedirectURIs []string
}

// NewHydraAdmin builds an admin client pointed at hydra's admin base URL
// (e.g. http://hydra-admin:4445).
func NewHydraAdmin(adminURL string) *HydraAdmin {
	cfg := hydra.NewConfiguration()
	cfg.Servers = hydra.ServerConfigurations{{URL: adminURL}}
	return &HydraAdmin{api: hydra.NewAPIClient(cfg), adminURL: adminURL, http: http.DefaultClient}
}

// VerifyToken introspects an access token via hydra's admin API and returns the
// token subject (the bkn-safe user/accessor id). Returns an error when the
// token is inactive or introspection fails. Used by the admin-API middleware to
// resolve the caller identity before the casbin admin check.
func (h *HydraAdmin) VerifyToken(ctx context.Context, token string) (string, error) {
	info, _, err := h.api.OAuth2API.IntrospectOAuth2Token(ctx).Token(token).Execute()
	if err != nil {
		return "", fmt.Errorf("introspect token: %w", err)
	}
	if !info.GetActive() {
		return "", errors.New("token inactive")
	}
	return info.GetSub(), nil
}

// AcceptUserCode accepts a device-flow user_code for a device challenge and
// returns hydra's redirect target (to the login flow). The stable v2.2 typed
// client lacks device methods, so this calls the admin endpoint directly:
// PUT /admin/oauth2/auth/requests/device/accept?device_challenge=...
func (h *HydraAdmin) AcceptUserCode(ctx context.Context, deviceChallenge, userCode string) (string, error) {
	u := fmt.Sprintf("%s/admin/oauth2/auth/requests/device/accept?device_challenge=%s",
		h.adminURL, url.QueryEscape(deviceChallenge))
	payload, _ := json.Marshal(map[string]string{"user_code": userCode})
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("accept user code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("accept user code: hydra %d: %s", resp.StatusCode, body)
	}
	var out struct {
		RedirectTo string `json:"redirect_to"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("accept user code: decode: %w", err)
	}
	return out.RedirectTo, nil
}

// GetClientRedirectURIs returns the OAuth2 client's registered redirect_uris.
func (h *HydraAdmin) GetClientRedirectURIs(ctx context.Context, clientID string) ([]string, error) {
	cl, _, err := h.api.OAuth2API.GetOAuth2Client(ctx, clientID).Execute()
	if err != nil {
		return nil, fmt.Errorf("get oauth2 client %q: %w", clientID, err)
	}
	return cl.GetRedirectUris(), nil
}

// GetOAuthClientURIs returns both callback and post-logout URI sets so the
// access-origin reconciler can update them atomically from one client snapshot.
func (h *HydraAdmin) GetOAuthClientURIs(ctx context.Context, clientID string) (OAuthClientURIs, error) {
	cl, _, err := h.api.OAuth2API.GetOAuth2Client(ctx, clientID).Execute()
	if err != nil {
		return OAuthClientURIs{}, fmt.Errorf("get oauth2 client %q: %w", clientID, err)
	}
	return OAuthClientURIs{
		RedirectURIs:           append([]string(nil), cl.GetRedirectUris()...),
		PostLogoutRedirectURIs: append([]string(nil), cl.GetPostLogoutRedirectUris()...),
	}, nil
}

// SetOAuthClientURIs replaces the two managed URI arrays in one JSON Patch.
func (h *HydraAdmin) SetOAuthClientURIs(ctx context.Context, clientID string, uris OAuthClientURIs) error {
	patch := []hydra.JsonPatch{
		{Op: "add", Path: "/redirect_uris", Value: uris.RedirectURIs},
		{Op: "add", Path: "/post_logout_redirect_uris", Value: uris.PostLogoutRedirectURIs},
	}
	if _, _, err := h.api.OAuth2API.PatchOAuth2Client(ctx, clientID).JsonPatch(patch).Execute(); err != nil {
		return fmt.Errorf("patch oauth2 client %q uris: %w", clientID, err)
	}
	return nil
}

// AddClientRedirectURI registers uri on the client (idempotent: a duplicate is a
// no-op) and returns the resulting redirect_uris.
func (h *HydraAdmin) AddClientRedirectURI(ctx context.Context, clientID, uri string) ([]string, error) {
	return h.patchRedirectURIs(ctx, clientID, func(uris []string) []string {
		for _, u := range uris {
			if u == uri {
				return uris // already present
			}
		}
		return append(uris, uri)
	})
}

// RemoveClientRedirectURI drops uri from the client (a no-op when absent) and
// returns the resulting redirect_uris.
func (h *HydraAdmin) RemoveClientRedirectURI(ctx context.Context, clientID, uri string) ([]string, error) {
	return h.patchRedirectURIs(ctx, clientID, func(uris []string) []string {
		out := make([]string, 0, len(uris))
		for _, u := range uris {
			if u != uri {
				out = append(out, u)
			}
		}
		return out
	})
}

// patchRedirectURIs reads the client's current redirect_uris, applies mutate, and
// writes the result back with a JSON Patch. Read-modify-write keeps merge and
// idempotency logic here — hydra's patch has no add-unique-element operation. The
// "add" op replaces the member when present (RFC 6902) so it works whether or not
// the client already has any redirect_uris.
func (h *HydraAdmin) patchRedirectURIs(ctx context.Context, clientID string, mutate func([]string) []string) ([]string, error) {
	cl, _, err := h.api.OAuth2API.GetOAuth2Client(ctx, clientID).Execute()
	if err != nil {
		return nil, fmt.Errorf("get oauth2 client %q: %w", clientID, err)
	}
	next := mutate(cl.GetRedirectUris())
	patch := []hydra.JsonPatch{{Op: "add", Path: "/redirect_uris", Value: next}}
	out, _, err := h.api.OAuth2API.PatchOAuth2Client(ctx, clientID).JsonPatch(patch).Execute()
	if err != nil {
		return nil, fmt.Errorf("patch oauth2 client %q redirect_uris: %w", clientID, err)
	}
	return out.GetRedirectUris(), nil
}

// LoginRequest is the subset of hydra's login challenge bkn-safe needs.
type LoginRequest struct {
	Challenge  string
	Subject    string // set if hydra remembers a prior session (skip re-auth)
	Skip       bool   // hydra can skip showing the login UI
	ClientID   string
	RequestURL string // original OAuth2 authorization URL, including ui_locales
}

// GetLogin fetches the login request for a challenge.
func (h *HydraAdmin) GetLogin(ctx context.Context, challenge string) (*LoginRequest, error) {
	req, _, err := h.api.OAuth2API.GetOAuth2LoginRequest(ctx).LoginChallenge(challenge).Execute()
	if err != nil {
		return nil, fmt.Errorf("get login request: %w", err)
	}
	lr := &LoginRequest{Challenge: challenge, RequestURL: req.RequestUrl}
	if req.Subject != "" {
		lr.Subject = req.Subject
	}
	if req.Skip {
		lr.Skip = req.Skip
	}
	if req.Client.ClientId != nil {
		lr.ClientID = *req.Client.ClientId
	}
	return lr, nil
}

// AcceptLogin accepts a login for subject and returns hydra's redirect target.
func (h *HydraAdmin) AcceptLogin(ctx context.Context, challenge, subject string, remember bool) (string, error) {
	body := hydra.NewAcceptOAuth2LoginRequest(subject)
	body.SetRemember(remember)
	out, _, err := h.api.OAuth2API.AcceptOAuth2LoginRequest(ctx).
		LoginChallenge(challenge).AcceptOAuth2LoginRequest(*body).Execute()
	if err != nil {
		return "", fmt.Errorf("accept login: %w", err)
	}
	return out.RedirectTo, nil
}

// ConsentRequest is the subset of hydra's consent challenge bkn-safe needs.
type ConsentRequest struct {
	Challenge      string
	Subject        string
	RequestedScope []string
	Audience       []string
	ClientID       string // requesting OAuth2 client (shown on the consent page)
	ClientName     string
	RequestURL     string // original OAuth2 authorization URL, including ui_locales
}

// GetConsent fetches the consent request for a challenge.
func (h *HydraAdmin) GetConsent(ctx context.Context, challenge string) (*ConsentRequest, error) {
	req, _, err := h.api.OAuth2API.GetOAuth2ConsentRequest(ctx).ConsentChallenge(challenge).Execute()
	if err != nil {
		return nil, fmt.Errorf("get consent request: %w", err)
	}
	cr := &ConsentRequest{
		Challenge:      challenge,
		RequestedScope: req.RequestedScope,
		RequestURL:     req.GetRequestUrl(),
	}
	if req.Client != nil {
		if req.Client.ClientId != nil {
			cr.ClientID = *req.Client.ClientId
		}
		if req.Client.ClientName != nil {
			cr.ClientName = *req.Client.ClientName
		}
	}
	if req.Subject != nil {
		cr.Subject = *req.Subject
	}
	if req.RequestedAccessTokenAudience != nil {
		cr.Audience = req.RequestedAccessTokenAudience
	}
	return cr, nil
}

// AcceptConsent grants the requested scope and injects ext into the access
// token session. hydra surfaces session.access_token under the introspect
// `ext` field — this is what satisfies the §1 introspect contract.
func (h *HydraAdmin) AcceptConsent(ctx context.Context, cr *ConsentRequest, ext map[string]any, remember bool) (string, error) {
	body := hydra.NewAcceptOAuth2ConsentRequest()
	body.SetGrantScope(cr.RequestedScope)
	body.SetGrantAccessTokenAudience(cr.Audience)
	body.SetRemember(remember)
	session := hydra.NewAcceptOAuth2ConsentRequestSession()
	session.SetAccessToken(ext)
	body.SetSession(*session)
	out, _, err := h.api.OAuth2API.AcceptOAuth2ConsentRequest(ctx).
		ConsentChallenge(cr.Challenge).AcceptOAuth2ConsentRequest(*body).Execute()
	if err != nil {
		return "", fmt.Errorf("accept consent: %w", err)
	}
	return out.RedirectTo, nil
}

// RejectConsent denies the consent request.
func (h *HydraAdmin) RejectConsent(ctx context.Context, challenge string) (string, error) {
	body := hydra.NewRejectOAuth2Request()
	body.SetError("access_denied")
	body.SetErrorDescription("user denied consent")
	out, _, err := h.api.OAuth2API.RejectOAuth2ConsentRequest(ctx).
		ConsentChallenge(challenge).RejectOAuth2Request(*body).Execute()
	if err != nil {
		return "", fmt.Errorf("reject consent: %w", err)
	}
	return out.RedirectTo, nil
}

// DynamicClient is a public OAuth2 client an MCP agent registers for itself.
type DynamicClient struct {
	Name         string
	RedirectURIs []string
	GrantTypes   []string
	Scope        string
	// Metadata marks the client as self-registered, for later listing.
	Metadata map[string]any
}

// RegisteredClient is what Hydra assigned to a newly created client.
type RegisteredClient struct {
	ClientID string
	IssuedAt time.Time
}

// CreateDynamicClient creates a public client (no secret, PKCE) through the
// admin API. Hydra's own dynamic registration stays disabled; bkn-safe decides
// what a self-registered client may be before calling this.
func (h *HydraAdmin) CreateDynamicClient(ctx context.Context, c DynamicClient) (RegisteredClient, error) {
	body := hydra.NewOAuth2Client()
	body.SetClientName(c.Name)
	body.SetRedirectUris(c.RedirectURIs)
	body.SetGrantTypes(c.GrantTypes)
	body.SetResponseTypes([]string{"code"})
	body.SetScope(c.Scope)
	body.SetTokenEndpointAuthMethod("none")
	if c.Metadata != nil {
		body.SetMetadata(c.Metadata)
	}
	out, _, err := h.api.OAuth2API.CreateOAuth2Client(ctx).OAuth2Client(*body).Execute()
	if err != nil {
		return RegisteredClient{}, fmt.Errorf("create oauth2 client: %w", err)
	}
	issued := time.Now()
	if out.CreatedAt != nil {
		issued = *out.CreatedAt
	}
	return RegisteredClient{ClientID: out.GetClientId(), IssuedAt: issued}, nil
}

// DynamicClientKind marks clients bkn-safe created through self-registration.
const DynamicClientKind = "dynamic"

// ErrNotDynamicClient refuses to touch a client bkn-safe did not self-register,
// such as openbkn-studio: deleting one of those would break every sign-in.
var ErrNotDynamicClient = errors.New("oauth client is not self-registered")

// ConnectedClient is a self-registered client and, for a user, their grant to it.
type ConnectedClient struct {
	ClientID     string
	ClientName   string
	RedirectURIs []string
	// RegisteredAt is when the client registered itself.
	RegisteredAt time.Time
	// RegisteredFrom is the address the registration came from.
	RegisteredFrom string
	// GrantedAt and Scopes describe a user's latest grant; zero for admin listings.
	GrantedAt time.Time
	Scopes    []string
}

const hydraPageSize = 500

// maxHydraPages bounds a listing; a subject or deployment past it is cut off
// rather than holding the request open.
const maxHydraPages = 20

// ListConnectedClients returns the self-registered clients a user granted
// access to, latest grant per client. First-party clients (Studio, CLI) are
// left out: they are the user's own sign-ins, not connections.
func (h *HydraAdmin) ListConnectedClients(ctx context.Context, subject string) ([]ConnectedClient, error) {
	byClient := map[string]ConnectedClient{}
	token := ""
	for page := 0; page < maxHydraPages; page++ {
		req := h.api.OAuth2API.ListOAuth2ConsentSessions(ctx).Subject(subject).PageSize(hydraPageSize)
		if token != "" {
			req = req.PageToken(token)
		}
		sessions, res, err := req.Execute()
		if err != nil {
			return nil, fmt.Errorf("list consent sessions: %w", err)
		}
		for _, s := range sessions {
			cr := s.ConsentRequest
			if cr == nil || cr.Client == nil || !isDynamicClient(*cr.Client) {
				continue
			}
			client := connectedClient(*cr.Client)
			if s.HandledAt != nil {
				client.GrantedAt = *s.HandledAt
			}
			client.Scopes = append([]string(nil), s.GrantScope...)
			if prev, ok := byClient[client.ClientID]; !ok || client.GrantedAt.After(prev.GrantedAt) {
				byClient[client.ClientID] = client
			}
		}
		if token = nextPageToken(res); token == "" {
			break
		}
	}
	out := make([]ConnectedClient, 0, len(byClient))
	for _, c := range byClient {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GrantedAt.After(out[j].GrantedAt) })
	return out, nil
}

// RevokeConnectedClient withdraws a user's grant to one client; Hydra
// invalidates the access and refresh tokens issued under it. It returns the
// client's display name for the audit record.
func (h *HydraAdmin) RevokeConnectedClient(ctx context.Context, subject, clientID string) (string, error) {
	cl, err := h.dynamicClient(ctx, clientID)
	if err != nil {
		return "", err
	}
	if _, err := h.api.OAuth2API.RevokeOAuth2ConsentSessions(ctx).Subject(subject).Client(clientID).Execute(); err != nil {
		return "", fmt.Errorf("revoke consent sessions: %w", err)
	}
	return cl.GetClientName(), nil
}

// ListDynamicClients returns every self-registered client, newest first.
func (h *HydraAdmin) ListDynamicClients(ctx context.Context) ([]ConnectedClient, error) {
	var out []ConnectedClient
	token := ""
	for page := 0; page < maxHydraPages; page++ {
		req := h.api.OAuth2API.ListOAuth2Clients(ctx).PageSize(hydraPageSize)
		if token != "" {
			req = req.PageToken(token)
		}
		clients, res, err := req.Execute()
		if err != nil {
			return nil, fmt.Errorf("list oauth2 clients: %w", err)
		}
		for _, cl := range clients {
			if isDynamicClient(cl) {
				out = append(out, connectedClient(cl))
			}
		}
		if token = nextPageToken(res); token == "" {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RegisteredAt.After(out[j].RegisteredAt) })
	return out, nil
}

// DeleteDynamicClient removes a self-registered client; every grant to it and
// every token issued to it stop working. It returns the client's display name
// for the audit record.
func (h *HydraAdmin) DeleteDynamicClient(ctx context.Context, clientID string) (string, error) {
	cl, err := h.dynamicClient(ctx, clientID)
	if err != nil {
		return "", err
	}
	if _, err := h.api.OAuth2API.DeleteOAuth2Client(ctx, clientID).Execute(); err != nil {
		return "", fmt.Errorf("delete oauth2 client %q: %w", clientID, err)
	}
	return cl.GetClientName(), nil
}

func (h *HydraAdmin) dynamicClient(ctx context.Context, clientID string) (hydra.OAuth2Client, error) {
	cl, res, err := h.api.OAuth2API.GetOAuth2Client(ctx, clientID).Execute()
	if res != nil && res.StatusCode == http.StatusNotFound {
		return hydra.OAuth2Client{}, ErrNotDynamicClient
	}
	if err != nil {
		return hydra.OAuth2Client{}, fmt.Errorf("get oauth2 client %q: %w", clientID, err)
	}
	if !isDynamicClient(*cl) {
		return hydra.OAuth2Client{}, ErrNotDynamicClient
	}
	return *cl, nil
}

func isDynamicClient(cl hydra.OAuth2Client) bool {
	meta, _ := cl.Metadata.(map[string]any)
	return meta["openbkn_kind"] == DynamicClientKind
}

func connectedClient(cl hydra.OAuth2Client) ConnectedClient {
	meta, _ := cl.Metadata.(map[string]any)
	from, _ := meta["registered_from"].(string)
	c := ConnectedClient{
		ClientID:       cl.GetClientId(),
		ClientName:     cl.GetClientName(),
		RedirectURIs:   append([]string(nil), cl.GetRedirectUris()...),
		RegisteredFrom: from,
	}
	if cl.CreatedAt != nil {
		c.RegisteredAt = *cl.CreatedAt
	}
	return c
}

// nextPageToken reads Hydra's RFC 8288 Link header for the next page.
func nextPageToken(res *http.Response) string {
	if res == nil {
		return ""
	}
	for _, link := range strings.Split(res.Header.Get("Link"), ",") {
		parts := strings.Split(link, ";")
		if len(parts) < 2 || !strings.Contains(strings.Join(parts[1:], ";"), `rel="next"`) {
			continue
		}
		raw := strings.Trim(strings.TrimSpace(parts[0]), "<>")
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		return u.Query().Get("page_token")
	}
	return ""
}
