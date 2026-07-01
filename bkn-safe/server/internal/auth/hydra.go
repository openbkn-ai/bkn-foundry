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

	hydra "github.com/ory/hydra-client-go/v2"
)

// HydraAdmin wraps hydra's admin OAuth2 API for the login & consent flow.
// Admin is internal-only; bkn-safe reaches it service-to-service.
type HydraAdmin struct {
	api      *hydra.APIClient
	adminURL string
	http     *http.Client
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
	defer resp.Body.Close()
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
	Challenge string
	Subject   string // set if hydra remembers a prior session (skip re-auth)
	Skip      bool   // hydra can skip showing the login UI
	ClientID  string
}

// GetLogin fetches the login request for a challenge.
func (h *HydraAdmin) GetLogin(ctx context.Context, challenge string) (*LoginRequest, error) {
	req, _, err := h.api.OAuth2API.GetOAuth2LoginRequest(ctx).LoginChallenge(challenge).Execute()
	if err != nil {
		return nil, fmt.Errorf("get login request: %w", err)
	}
	lr := &LoginRequest{Challenge: challenge}
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
}

// GetConsent fetches the consent request for a challenge.
func (h *HydraAdmin) GetConsent(ctx context.Context, challenge string) (*ConsentRequest, error) {
	req, _, err := h.api.OAuth2API.GetOAuth2ConsentRequest(ctx).ConsentChallenge(challenge).Execute()
	if err != nil {
		return nil, fmt.Errorf("get consent request: %w", err)
	}
	cr := &ConsentRequest{Challenge: challenge, RequestedScope: req.RequestedScope}
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
