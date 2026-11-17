// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	hydra "github.com/ory/hydra-client-go/v2"
)

// ErrLogoutToken rejects active credentials that do not belong to the Studio
// public client, or an access/refresh pair belonging to different subjects.
var ErrLogoutToken = errors.New("invalid Studio logout credentials")

// StudioLogout revokes the Studio grants selected by a verified browser ID token,
// including rotated credentials. Without a hint it revokes only presented tokens.
// It never revokes consent by subject/client, which would affect other devices.
type StudioLogout struct {
	admin     *HydraAdmin
	public    *hydra.APIClient
	publicURL string
}

func NewStudioLogout(admin *HydraAdmin, publicURL string) *StudioLogout {
	cfg := hydra.NewConfiguration()
	cfg.Servers = hydra.ServerConfigurations{{URL: publicURL}}
	return &StudioLogout{admin: admin, public: hydra.NewAPIClient(cfg), publicURL: strings.TrimRight(publicURL, "/")}
}

// Logout accepts an inactive access token so a partially completed revocation
// can be retried using the still-active refresh token. Ownership is established
// from live introspection, never from caller-provided subject/client IDs.
// An already inactive pair is an idempotent success with no new access fact.
func (s *StudioLogout) Logout(ctx context.Context, accessToken, refreshToken, idToken string) (string, error) {
	if accessToken == "" && refreshToken == "" && idToken == "" {
		return "", ErrLogoutToken
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	tokens := make([]struct{ value, use string }, 0, 2)
	if accessToken != "" {
		tokens = append(tokens, struct{ value, use string }{accessToken, "access_token"})
	}
	if refreshToken != "" {
		tokens = append(tokens, struct{ value, use string }{refreshToken, "refresh_token"})
	}
	subject := ""
	hadActive := false
	for _, token := range tokens {
		info, err := s.inspect(ctx, token.value, token.use)
		if err != nil {
			return "", errors.New("logout introspection unavailable")
		}
		if !info.GetActive() {
			continue
		}
		if info.GetClientId() != "openbkn-studio" || info.GetTokenUse() != token.use || info.GetSub() == "" ||
			(subject != "" && subject != info.GetSub()) {
			return "", ErrLogoutToken
		}
		subject = info.GetSub()
		hadActive = true
	}
	var consentIDs []string
	var sid string
	if idToken != "" {
		hint, err := s.logoutHint(ctx, idToken)
		if err != nil {
			return "", err
		}
		if subject != "" && subject != hint.Subject {
			return "", ErrLogoutToken
		}
		subject, sid = hint.Subject, hint.SID
		var errLookup error
		consentIDs, errLookup = s.logoutConsentIDs(ctx, subject, sid)
		if errLookup != nil {
			return "", errLookup
		}
		if hadActive && len(consentIDs) == 0 {
			return "", errors.New("logout session could not be located")
		}
	}

	// Revoke refresh first, then explicitly revoke access as well. Attempt both
	// even on partial failure; never include OAuth responses or tokens in errors.
	var failed bool
	for _, id := range consentIDs {
		if s.revokeLogoutConsent(ctx, id) != nil {
			failed = true
		}
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		if _, err := s.public.OAuth2API.RevokeOAuth2Token(ctx).
			Token(tokens[i].value).ClientId("openbkn-studio").Execute(); err != nil {
			failed = true
		}
	}
	if failed {
		return "", errors.New("logout revocation unavailable")
	}
	// RFC 7009 can return success for an unknown token. Confirm actual state
	// before claiming success (including deployments with unexpected behavior).
	for _, token := range tokens {
		info, err := s.inspect(ctx, token.value, token.use)
		if err != nil || info.GetActive() {
			return "", errors.New("logout revocation not confirmed")
		}
	}
	if sid != "" {
		remaining, err := s.logoutConsentIDs(ctx, subject, sid)
		if err != nil || len(remaining) > 0 {
			return "", errors.New("logout session revocation not confirmed")
		}
	}
	if !hadActive && len(consentIDs) == 0 {
		return "", nil
	}
	return subject, nil
}

// The pinned Hydra v2.2 SDK omits token_type_hint on introspection. Explicitly
// provide it so an active refresh token can authenticate a partial-logout retry.
func (s *StudioLogout) inspect(ctx context.Context, token, use string) (*hydra.IntrospectedOAuth2Token, error) {
	form := url.Values{"token": {token}, "token_type_hint": {use}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.admin.adminURL, "/")+"/admin/oauth2/introspect", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("logout introspection unavailable")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.admin.http.Do(req)
	if err != nil {
		return nil, errors.New("logout introspection unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	var info hydra.IntrospectedOAuth2Token
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info) != nil {
		return nil, errors.New("logout introspection unavailable")
	}
	return &info, nil
}
