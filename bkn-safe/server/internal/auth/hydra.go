package auth

import (
	"bytes"
	"context"
	"encoding/json"
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
}

// GetConsent fetches the consent request for a challenge.
func (h *HydraAdmin) GetConsent(ctx context.Context, challenge string) (*ConsentRequest, error) {
	req, _, err := h.api.OAuth2API.GetOAuth2ConsentRequest(ctx).ConsentChallenge(challenge).Execute()
	if err != nil {
		return nil, fmt.Errorf("get consent request: %w", err)
	}
	cr := &ConsentRequest{Challenge: challenge, RequestedScope: req.RequestedScope}
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
