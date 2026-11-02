// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sso

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/auth"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

const jwtBearerGrant = "urn:ietf:params:oauth:grant-type:jwt-bearer"

// Exchange uses a dedicated Hydra client and a short-lived RSA assertion.
// The corresponding public JWK must be registered as a Hydra trusted issuer.
type Exchange struct {
	service  *Service
	hydra    *auth.HydraAdmin
	client   *http.Client
	tokenURL string
	audience string
	issuer   string
	keyID    string
	clientID string
	secret   string
	hookKey  string
	key      *rsa.PrivateKey
}

type ExchangeConfig struct {
	TokenURL      string
	Audience      string
	Issuer        string
	KeyID         string
	ClientID      string
	ClientSecret  string
	HookKey       string
	PrivateKeyPEM string
}

func NewExchange(service *Service, hydra *auth.HydraAdmin, cfg ExchangeConfig) (*Exchange, error) {
	if service == nil || hydra == nil || cfg.TokenURL == "" || cfg.Audience == "" || cfg.Issuer == "" || cfg.ClientID == "" ||
		cfg.ClientSecret == "" || len(cfg.HookKey) < 32 || cfg.PrivateKeyPEM == "" || cfg.KeyID == "" {
		return nil, errors.New("incomplete SSO exchange configuration")
	}
	block, _ := pem.Decode([]byte(cfg.PrivateKeyPEM))
	if block == nil {
		return nil, errors.New("invalid SSO assertion private key")
	}
	var key *rsa.PrivateKey
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		key, _ = parsed.(*rsa.PrivateKey)
	} else {
		key, _ = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	if key == nil || key.N.BitLen() < 2048 {
		return nil, errors.New("SSO assertion key must be RSA 2048+ bits")
	}
	if err := validateEndpoint(cfg.TokenURL, true); err != nil {
		return nil, err
	}
	if err := validateEndpoint(cfg.Audience, true); err != nil {
		return nil, err
	}
	return &Exchange{service: service, hydra: hydra, client: &http.Client{Timeout: 8 * time.Second},
		tokenURL: cfg.TokenURL, audience: cfg.Audience, issuer: cfg.Issuer, keyID: cfg.KeyID, clientID: cfg.ClientID,
		secret: cfg.ClientSecret, hookKey: cfg.HookKey, key: key}, nil
}

type assertionClaims struct {
	Issuer   string   `json:"iss"`
	Subject  string   `json:"sub"`
	Audience []string `json:"aud"`
	IssuedAt int64    `json:"iat"`
	Expires  int64    `json:"exp"`
	ID       string   `json:"jti"`
	LoginIP  string   `json:"login_ip"`
}

func (e *Exchange) sign(userID, loginIP string) (string, error) {
	id, err := randomURLToken()
	if err != nil {
		return "", err
	}
	claims := assertionClaims{Issuer: e.issuer, Subject: userID, Audience: []string{e.audience, "bkn-safe"},
		IssuedAt: time.Now().Unix(), Expires: time.Now().Add(time.Minute).Unix(), ID: id, LoginIP: loginIP}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": e.keyID})
	payload, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, e.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (e *Exchange) verify(assertion string) (*assertionClaims, error) {
	if len(assertion) > 16<<10 {
		return nil, ErrInvalidToken
	}
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var headerFields struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(header, &headerFields) != nil || headerFields.Alg != "RS256" || headerFields.Kid != e.keyID {
		return nil, ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&e.key.PublicKey, crypto.SHA256, digest[:], signature) != nil {
		return nil, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var claims assertionClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != e.issuer ||
		!contains(claims.Audience, e.audience) || !contains(claims.Audience, "bkn-safe") || claims.Subject == "" || claims.ID == "" ||
		claims.Expires <= time.Now().Unix() || claims.Expires-claims.IssuedAt > 60 || claims.IssuedAt > time.Now().Unix()+5 {
		return nil, ErrInvalidToken
	}
	return &claims, nil
}

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

// Swap validates Casdoor's bearer token, maps the user, then issues a Hydra
// token. The token is withheld unless introspection matches the user contract.
func (e *Exchange) Swap(ctx context.Context, providerID, externalToken, loginIP string) (*TokenResponse, error) {
	p, err := e.service.Provider(ctx, providerID)
	if err != nil {
		return nil, err
	}
	info, err := e.service.ValidateExchange(ctx, p, externalToken)
	if err != nil {
		return nil, err
	}
	u, err := e.service.ResolveUser(ctx, p, externalToken, info.Subject)
	if err != nil {
		return nil, err
	}
	assertion, err := e.sign(u.ID, loginIP)
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {jwtBearerGrant}, "assertion": {assertion}, "scope": {hydraIssueScope(p)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(e.clientID, e.secret)
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hydra JWT bearer grant status %d", resp.StatusCode)
	}
	var out TokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" || !strings.EqualFold(out.TokenType, "Bearer") || out.ExpiresIn <= 0 {
		return nil, ErrInvalidToken
	}
	issued, err := e.hydra.InspectToken(ctx, out.AccessToken)
	if err != nil || !issued.GetActive() || issued.GetSub() != u.ID || issued.GetClientId() != e.clientID ||
		!contains(issued.GetAud(), "bkn-safe") {
		return nil, ErrInvalidToken
	}
	ext := issued.GetExt()
	for _, key := range []string{"visitor_type", "login_ip", "udid", "account_type", "client_type"} {
		if _, ok := ext[key].(string); !ok {
			return nil, ErrInvalidToken
		}
	}
	return &out, nil
}

type hookInput struct {
	Request struct {
		ClientID   string              `json:"client_id"`
		GrantTypes []string            `json:"grant_types"`
		Payload    map[string][]string `json:"payload"`
	} `json:"request"`
}

// Hook is configured on Hydra's token endpoint. It returns ext claims only for
// our dedicated client; all other grants keep their existing consent session.
func (e *Exchange) Hook(ctx context.Context, providedKey string, body []byte) (map[string]any, bool, error) {
	if subtle.ConstantTimeCompare([]byte(providedKey), []byte(e.hookKey)) != 1 {
		return nil, false, ErrForbidden
	}
	var input hookInput
	if len(body) > 32<<10 || json.Unmarshal(body, &input) != nil {
		return nil, false, ErrInvalidToken
	}
	if input.Request.ClientID != e.clientID {
		return nil, false, nil
	}
	if !contains(input.Request.GrantTypes, jwtBearerGrant) || len(input.Request.Payload["assertion"]) != 1 {
		return nil, false, ErrForbidden
	}
	claims, err := e.verify(input.Request.Payload["assertion"][0])
	if err != nil {
		return nil, false, err
	}
	var user model.User
	if err := e.service.db.WithContext(ctx).First(&user, "id = ? AND enabled = ? AND source = ?",
		claims.Subject, true, model.SourceSSO).Error; err != nil {
		return nil, false, ErrForbidden
	}
	return auth.ExtClaims(&user, claims.LoginIP, "unknown"), true, nil
}
