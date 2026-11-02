// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package sso

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

type idTokenClaims struct {
	Issuer          string         `json:"iss"`
	Subject         string         `json:"sub"`
	Audience        stringAudience `json:"aud"`
	AuthorizedParty string         `json:"azp"`
	Nonce           string         `json:"nonce"`
	Expiry          int64          `json:"exp"`
	IssuedAt        int64          `json:"iat"`
}

// verifyIDToken implements the RS256 subset of OIDC Core. An unsupported
// algorithm is rejected; a Casdoor installation using another algorithm needs
// an explicit, reviewed verifier rather than an unsigned-token fallback.
func (s *Service) verifyIDToken(ctx context.Context, p *model.SSOProvider, m *metadata, raw, nonce string) (string, error) {
	if len(raw) == 0 || len(raw) > 16<<10 || nonce == "" {
		return "", ErrInvalidToken
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", ErrInvalidToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrInvalidToken
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return "", ErrInvalidToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.JWKS, nil)
	if err != nil {
		return "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", ErrInvalidToken
	}
	var set struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			KeyType   string `json:"kty"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			Modulus   string `json:"n"`
			Exponent  string `json:"e"`
		} `json:"keys"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&set) != nil {
		return "", ErrInvalidToken
	}
	var key *rsa.PublicKey
	for _, item := range set.Keys {
		if item.KeyID != header.KeyID || item.KeyType != "RSA" ||
			(item.Algorithm != "" && item.Algorithm != "RS256") ||
			(item.Use != "" && item.Use != "sig") {
			continue
		}
		n, nErr := base64.RawURLEncoding.DecodeString(item.Modulus)
		e, eErr := base64.RawURLEncoding.DecodeString(item.Exponent)
		if nErr != nil || eErr != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exponent := new(big.Int).SetBytes(e)
		if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > 1<<31-1 || exponent.Int64()%2 == 0 {
			continue
		}
		key = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent.Int64())}
		if key.N.BitLen() < 2048 {
			return "", ErrInvalidToken
		}
		break
	}
	if key == nil {
		return "", ErrInvalidToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return "", ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrInvalidToken
	}
	var claims idTokenClaims
	now := time.Now().Unix()
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != p.Issuer || claims.Subject == "" ||
		!contains(claims.Audience, p.ClientID) || claims.Nonce != nonce || claims.Expiry <= now ||
		claims.IssuedAt > now+60 || claims.IssuedAt == 0 ||
		(len(claims.Audience) > 1 && claims.AuthorizedParty != p.ClientID) {
		return "", ErrInvalidToken
	}
	return claims.Subject, nil
}
