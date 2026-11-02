// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

var ErrInvalidState = errors.New("invalid or expired SSO login state")

func randomURLToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func stateDigest(state string) string {
	digest := sha256.Sum256([]byte(state))
	return hex.EncodeToString(digest[:])
}

// Begin starts one browser authorization-code transaction after checking the
// Hydra login challenge. The caller binds state to the browser in an HttpOnly
// SameSite cookie and redirects to the returned URL.
func (s *Service) Begin(ctx context.Context, h *auth.HydraAdmin, providerID, challenge string) (string, string, error) {
	p, err := s.Provider(ctx, providerID)
	if err != nil || !p.LoginEnabled || challenge == "" {
		return "", "", ErrInvalidProvider
	}
	if _, err := h.GetLogin(ctx, challenge); err != nil {
		return "", "", err
	}
	m, err := s.discover(ctx, p)
	if err != nil {
		return "", "", err
	}
	state, err := randomURLToken()
	if err != nil {
		return "", "", err
	}
	verifier, err := randomURLToken()
	if err != nil {
		return "", "", err
	}
	nonce, err := randomURLToken()
	if err != nil {
		return "", "", err
	}
	row := model.SSOLoginState{StateHash: stateDigest(state), ProviderID: providerID,
		Challenge: challenge, CodeVerifier: verifier, Nonce: nonce, ExpiresAt: time.Now().Add(5 * time.Minute)}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return "", "", err
	}
	challengeHash := sha256.Sum256([]byte(verifier))
	u, _ := url.Parse(m.Authorize)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", strings.TrimSpace(p.Scopes))
	if q.Get("scope") == "" {
		q.Set("scope", "openid profile email")
	}
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challengeHash[:]))
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String(), state, nil
}

func (s *Service) consumeState(ctx context.Context, providerID, state, cookie string) (*model.SSOLoginState, error) {
	if len(state) != 43 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie)) != 1 {
		return nil, ErrInvalidState
	}
	var row model.SSOLoginState
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&row, "state_hash = ? AND provider_id = ? AND expires_at > ?", stateDigest(state), providerID, time.Now()).Error; err != nil {
			return ErrInvalidState
		}
		deleted := tx.Delete(&model.SSOLoginState{}, "state_hash = ?", row.StateHash)
		if deleted.Error != nil || deleted.RowsAffected != 1 {
			return ErrInvalidState
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Complete consumes state once, exchanges Casdoor's code, verifies its access
// token via introspection and UserInfo, and resumes the original Hydra flow.
func (s *Service) Complete(ctx context.Context, h *auth.HydraAdmin, providerID, state, cookie, code string) (string, error) {
	if code == "" || len(code) > 4096 {
		return "", ErrInvalidState
	}
	row, err := s.consumeState(ctx, providerID, state, cookie)
	if err != nil {
		return "", err
	}
	p, err := s.Provider(ctx, providerID)
	if err != nil || !p.LoginEnabled {
		return "", ErrInvalidProvider
	}
	// HTTP 1/7 (Casdoor): fetch OIDC discovery metadata.
	m, err := s.discover(ctx, p)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {p.ClientID},
		"client_secret": {os.Getenv(p.ClientSecretEnv)}, "code": {code},
		"redirect_uri": {p.RedirectURI}, "code_verifier": {row.CodeVerifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// HTTP 2/7 (Casdoor): exchange the authorization code for tokens.
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OIDC code exchange status %d", resp.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&token); err != nil {
		return "", err
	}
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return "", ErrInvalidToken
	}
	// HTTP 3/7 (Casdoor): fetch JWKS to verify the ID token signature.
	idSubject, err := s.verifyIDToken(ctx, p, m, token.IDToken, row.Nonce)
	if err != nil {
		return "", err
	}
	// HTTP 4/7 (Casdoor): introspect the access token and verify its client and subject.
	info, err := s.introspect(ctx, p, m, token.AccessToken)
	if err != nil || info.ClientID != p.ClientID || info.Subject != idSubject {
		return "", ErrInvalidToken
	}
	// HTTP 5/7-6/7 (Casdoor): ResolveUser rediscovers metadata, then fetches UserInfo.
	user, err := s.ResolveUser(ctx, p, token.AccessToken, info.Subject)
	if err != nil {
		return "", err
	}
	// HTTP 7/7 (Hydra Admin): accept the login challenge and get the next redirect URL.
	return h.AcceptLogin(ctx, row.Challenge, user.ID, false)
}
