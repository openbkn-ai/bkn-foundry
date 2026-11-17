// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type logoutClaims struct {
	jwt.RegisteredClaims
	SID             string `json:"sid"`
	AuthorizedParty string `json:"azp"`
}

// An ID token is a signed, session-scoped logout credential, not an API access
// credential. Expired hints remain usable for logout, as in OIDC end-session;
// signature, issuer, audience, subject and session ID are still mandatory.
func (s *StudioLogout) logoutHint(ctx context.Context, raw string) (*logoutClaims, error) {
	var discovery struct {
		Issuer string `json:"issuer"`
	}
	if err := s.getLogoutJSON(ctx, s.publicURL+"/.well-known/openid-configuration", &discovery); err != nil {
		return nil, err
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	// Use the configured in-cluster endpoint; never follow a URL from the hint.
	if err := s.getLogoutJSON(ctx, s.publicURL+"/.well-known/jwks.json", &jwks); err != nil {
		return nil, err
	}
	claims := &logoutClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		for _, key := range jwks.Keys {
			if kid == "" || key.Kid != kid || key.Kty != "RSA" {
				continue
			}
			n, e1 := base64.RawURLEncoding.DecodeString(key.N)
			e, e2 := base64.RawURLEncoding.DecodeString(key.E)
			if e1 != nil || e2 != nil || len(e) == 0 || len(e) > 4 {
				return nil, ErrLogoutToken
			}
			modulus := new(big.Int).SetBytes(n)
			exponent := new(big.Int).SetBytes(e).Int64()
			if modulus.BitLen() < 2048 || exponent < 3 || exponent > 1<<31-1 {
				return nil, ErrLogoutToken
			}
			return &rsa.PublicKey{N: modulus, E: int(exponent)}, nil
		}
		return nil, ErrLogoutToken
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithoutClaimsValidation())
	if err != nil || discovery.Issuer == "" || claims.Issuer != discovery.Issuer || claims.Subject == "" || claims.SID == "" || len(claims.SID) > 512 ||
		(claims.AuthorizedParty != "" && claims.AuthorizedParty != "openbkn-studio") {
		return nil, ErrLogoutToken
	}
	for _, aud := range claims.Audience {
		if aud == "openbkn-studio" {
			return claims, nil
		}
	}
	return nil, ErrLogoutToken
}

func (s *StudioLogout) getLogoutJSON(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return errors.New("logout session verification unavailable")
	}
	res, err := s.admin.http.Do(req)
	if err != nil {
		return errors.New("logout session verification unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 128<<10)).Decode(out) != nil {
		return errors.New("logout session verification unavailable")
	}
	return nil
}

// The signed sid selects a browser login, and the live consent rows must also
// match its subject and the fixed Studio client before their IDs can be revoked.
func (s *StudioLogout) logoutConsentIDs(ctx context.Context, subject, sid string) ([]string, error) {
	ids := []string{}
	next := ""
	for page := 0; page < maxHydraPages; page++ {
		req := s.admin.api.OAuth2API.ListOAuth2ConsentSessions(ctx).Subject(subject).LoginSessionId(sid).PageSize(hydraPageSize)
		if next != "" {
			req = req.PageToken(next)
		}
		sessions, res, err := req.Execute()
		if err != nil {
			return nil, errors.New("logout consent lookup unavailable")
		}
		for _, session := range sessions {
			cr := session.ConsentRequest
			if cr == nil || cr.Client == nil || cr.Client.GetClientId() != "openbkn-studio" || cr.GetSubject() != subject || cr.GetLoginSessionId() != sid {
				continue
			}
			// Hydra v26 adds this field; the pinned v2.2 SDK preserves it here.
			id, _ := session.AdditionalProperties["consent_request_id"].(string)
			if id == "" {
				return nil, errors.New("logout consent identifier unavailable")
			}
			ids = append(ids, id)
		}
		next = nextPageToken(res)
		if next == "" {
			return ids, nil
		}
	}
	return nil, errors.New("logout consent pagination limit exceeded")
}

func (s *StudioLogout) revokeLogoutConsent(ctx context.Context, id string) error {
	// Revoke one verified consent request, never all subject/client sessions.
	target := strings.TrimRight(s.admin.adminURL, "/") + "/admin/oauth2/auth/sessions/consent?" + url.Values{"consent_request_id": {id}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return errors.New("logout consent revocation unavailable")
	}
	res, err := s.admin.http.Do(req)
	if err != nil {
		return errors.New("logout consent revocation unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("logout consent revocation unavailable")
	}
	return nil
}
