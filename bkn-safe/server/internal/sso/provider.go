// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package sso connects an external OIDC provider to bkn-safe's local identity
// store. The external access token is validated by its issuer's introspection
// endpoint; it is never treated as an OpenBKN token.
package sso

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/gorm"
)

var (
	ErrInvalidProvider = errors.New("invalid SSO provider configuration")
	ErrInvalidToken    = errors.New("invalid external access token")
	ErrForbidden       = errors.New("external token is not permitted for exchange")
	envName            = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

type Service struct {
	db     *gorm.DB
	client *http.Client
}

func New(db *gorm.DB, client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Service{db: db, client: client}
}

func (s *Service) Provider(ctx context.Context, id string) (*model.SSOProvider, error) {
	if !validID(id) {
		return nil, ErrInvalidProvider
	}
	var p model.SSOProvider
	if err := s.db.WithContext(ctx).First(&p, "id = ? AND enabled = ?", id, true).Error; err != nil {
		return nil, err
	}
	if err := validateProvider(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) LoginProviders(ctx context.Context) ([]model.SSOProvider, error) {
	var rows []model.SSOProvider
	if err := s.db.WithContext(ctx).Where("enabled = ? AND login_enabled = ?", true, true).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		if err := validateProvider(&rows[i]); err != nil {
			return nil, err // fail closed on a broken active configuration
		}
	}
	return rows, nil
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func validateEndpoint(raw string, allowHTTP bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return ErrInvalidProvider
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return ErrInvalidProvider
	}
	return nil
}

func validateProvider(p *model.SSOProvider) error {
	if !validID(p.ID) || p.Protocol != "oidc" || (p.Adapter != "casdoor" && p.Adapter != "generic") ||
		p.ClientID == "" || !envName.MatchString(p.ClientSecretEnv) || os.Getenv(p.ClientSecretEnv) == "" {
		return ErrInvalidProvider
	}
	if err := validateEndpoint(p.Issuer, p.AllowInsecureHTTP); err != nil {
		return err
	}
	if err := validateEndpoint(p.DiscoveryURL, p.AllowInsecureHTTP); err != nil {
		return err
	}
	if p.LoginEnabled {
		if err := validateEndpoint(p.RedirectURI, p.AllowInsecureHTTP); err != nil {
			return err
		}
		u, _ := url.Parse(p.RedirectURI)
		if u.Path != "/sso/callback/"+p.ID {
			return ErrInvalidProvider
		}
	}
	if p.ExchangeEnabled && (len(strings.Fields(p.ExchangeAudience)) == 0 ||
		len(strings.Fields(externalRequiredScope(p))) == 0 ||
		len(strings.Fields(hydraIssueScope(p))) == 0 || len(strings.Fields(p.AllowedClientIDs)) == 0) {
		return ErrInvalidProvider
	}
	return nil
}

// Existing installations keep their scope policy until the two new settings
// are configured explicitly. A whitespace-only explicit value is invalid,
// rather than falling back or disabling the external scope check.
func externalRequiredScope(p *model.SSOProvider) string {
	if p.ExternalRequiredScope != "" {
		return p.ExternalRequiredScope
	}
	return p.ExchangeScope
}

func hydraIssueScope(p *model.SSOProvider) string {
	if p.HydraIssueScope != "" {
		return p.HydraIssueScope
	}
	return p.ExchangeScope
}

type metadata struct {
	Issuer     string `json:"issuer"`
	Authorize  string `json:"authorization_endpoint"`
	Token      string `json:"token_endpoint"`
	UserInfo   string `json:"userinfo_endpoint"`
	Introspect string `json:"introspection_endpoint"`
	JWKS       string `json:"jwks_uri"`
}

func (s *Service) discover(ctx context.Context, p *model.SSOProvider) (*metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.DiscoveryURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC discovery status %d", resp.StatusCode)
	}
	var m metadata
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m); err != nil {
		return nil, err
	}
	if m.Issuer != p.Issuer {
		return nil, ErrInvalidProvider
	}
	for _, endpoint := range []string{m.Authorize, m.Token, m.UserInfo, m.Introspect, m.JWKS} {
		if err := validateEndpoint(endpoint, p.AllowInsecureHTTP); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

type stringAudience []string

func (a *stringAudience) UnmarshalJSON(raw []byte) error {
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		*a = list
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err != nil {
		return err
	}
	*a = []string{one}
	return nil
}

type TokenInfo struct {
	Active   bool           `json:"active"`
	Issuer   string         `json:"iss"`
	Subject  string         `json:"sub"`
	Audience stringAudience `json:"aud"`
	ClientID string         `json:"client_id"`
	Scope    string         `json:"scope"`
	Type     string         `json:"token_type"`
	Expiry   int64          `json:"exp"`
}

func contains(words []string, want string) bool {
	for _, word := range words {
		if word == want {
			return true
		}
	}
	return false
}

func (s *Service) introspect(ctx context.Context, p *model.SSOProvider, m *metadata, token string) (*TokenInfo, error) {
	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Introspect, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(p.ClientID, os.Getenv(p.ClientSecretEnv))
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OIDC introspection status %d", resp.StatusCode)
	}
	var out TokenInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Active || out.Subject == "" || out.Issuer != p.Issuer || out.Expiry <= time.Now().Unix() || !strings.EqualFold(out.Type, "Bearer") {
		return nil, ErrInvalidToken
	}
	return &out, nil
}

// ValidateExchange proves that the external token is active and was issued for
// an explicitly admitted application, audience and scope.
func (s *Service) ValidateExchange(ctx context.Context, p *model.SSOProvider, token string) (*TokenInfo, error) {
	if !p.ExchangeEnabled || len(token) == 0 || len(token) > 16<<10 {
		return nil, ErrForbidden
	}
	requiredScopes := strings.Fields(externalRequiredScope(p))
	if len(requiredScopes) == 0 {
		return nil, ErrInvalidProvider
	}
	m, err := s.discover(ctx, p)
	if err != nil {
		return nil, err
	}
	info, err := s.introspect(ctx, p, m, token)
	if err != nil {
		return nil, err
	}
	allowedAudience := false
	for _, audience := range strings.Fields(p.ExchangeAudience) {
		if contains(info.Audience, audience) {
			allowedAudience = true
			break
		}
	}
	if !allowedAudience ||
		!contains(strings.Fields(p.AllowedClientIDs), info.ClientID) {
		return nil, ErrForbidden
	}
	grantedScopes := strings.Fields(info.Scope)
	for _, scope := range requiredScopes {
		if !contains(grantedScopes, scope) {
			return nil, ErrForbidden
		}
	}
	return info, nil
}
