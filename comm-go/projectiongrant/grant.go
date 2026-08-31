// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package projectiongrant signs and verifies the narrow capability used by a
// Trace historical-provenance builder to read one of its recorded networks.
package projectiongrant

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

const algorithm = "EdDSA"

type Claims struct {
	Version             int
	Issuer              string
	KeyID               string
	Audience            string
	EventID             string
	InteractionID       string
	FactsHash           string
	KnowledgeNetworkIDs []string
	IssuedAt            time.Time
	ExpiresAt           time.Time
}

type VerifyOptions struct {
	Now              time.Time
	ExpectedIssuer   string
	ExpectedAudience string
}

type header struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
	Type      string `json:"typ"`
}

type wireClaims struct {
	Version             int      `json:"v"`
	Issuer              string   `json:"iss"`
	KeyID               string   `json:"kid"`
	Audience            string   `json:"aud"`
	EventID             string   `json:"event_id"`
	InteractionID       string   `json:"interaction_id"`
	FactsHash           string   `json:"facts_hash"`
	KnowledgeNetworkIDs []string `json:"knowledge_network_ids"`
	IssuedAt            int64    `json:"iat"`
	ExpiresAt           int64    `json:"exp"`
}

func Sign(claims Claims, privateKey ed25519.PrivateKey) (string, error) {
	canonical, err := canonicalClaims(claims)
	if err != nil {
		return "", err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("projection grant signing key is invalid")
	}
	head, err := json.Marshal(header{Algorithm: algorithm, KeyID: canonical.KeyID, Type: "JWT"})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(toWire(canonical))
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	signature := ed25519.Sign(privateKey, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func Verify(token string, keys map[string]ed25519.PublicKey, options VerifyOptions) (Claims, error) {
	if strings.TrimSpace(options.ExpectedIssuer) == "" || strings.TrimSpace(options.ExpectedAudience) == "" {
		return Claims{}, errors.New("projection grant verification requires issuer and audience")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Claims{}, errors.New("projection grant has invalid compact encoding")
	}
	var head header
	if err := decode(parts[0], &head); err != nil {
		return Claims{}, errors.New("projection grant has invalid header")
	}
	if head.Algorithm != algorithm || head.Type != "JWT" || strings.TrimSpace(head.KeyID) == "" {
		return Claims{}, errors.New("projection grant uses an unsupported header")
	}
	key, found := keys[head.KeyID]
	if !found || len(key) != ed25519.PublicKeySize {
		return Claims{}, errors.New("projection grant signing key is unknown")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, errors.New("projection grant signature is invalid")
	}
	var wire wireClaims
	if err := decode(parts[1], &wire); err != nil {
		return Claims{}, errors.New("projection grant has invalid claims")
	}
	claims, err := canonicalClaims(fromWire(wire))
	if err != nil {
		return Claims{}, err
	}
	if claims.KeyID != head.KeyID {
		return Claims{}, errors.New("projection grant key ID does not match header")
	}
	if claims.Issuer != options.ExpectedIssuer {
		return Claims{}, errors.New("projection grant issuer is invalid")
	}
	if claims.Audience != options.ExpectedAudience {
		return Claims{}, errors.New("projection grant audience is invalid")
	}
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !claims.ExpiresAt.After(now) {
		return Claims{}, errors.New("projection grant is expired")
	}
	if claims.IssuedAt.After(now) {
		return Claims{}, errors.New("projection grant is not yet valid")
	}
	return claims, nil
}

func (claims Claims) AllowsKnowledgeNetwork(networkID string) bool {
	networkID = strings.TrimSpace(networkID)
	index := sort.SearchStrings(claims.KnowledgeNetworkIDs, networkID)
	return index < len(claims.KnowledgeNetworkIDs) && claims.KnowledgeNetworkIDs[index] == networkID
}

func canonicalClaims(claims Claims) (Claims, error) {
	claims.Issuer = strings.TrimSpace(claims.Issuer)
	claims.KeyID = strings.TrimSpace(claims.KeyID)
	claims.Audience = strings.TrimSpace(claims.Audience)
	claims.EventID = strings.TrimSpace(claims.EventID)
	claims.InteractionID = strings.TrimSpace(claims.InteractionID)
	claims.FactsHash = strings.TrimSpace(claims.FactsHash)
	claims.KnowledgeNetworkIDs = canonicalNetworkIDs(claims.KnowledgeNetworkIDs)
	if claims.Version != 1 || claims.Issuer == "" || claims.KeyID == "" || claims.Audience == "" || claims.EventID == "" || claims.InteractionID == "" || claims.FactsHash == "" || claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) {
		return Claims{}, errors.New("projection grant claims are invalid")
	}
	return claims, nil
}

func canonicalNetworkIDs(ids []string) []string {
	unique := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			unique[id] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for id := range unique {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func toWire(claims Claims) wireClaims {
	return wireClaims{Version: claims.Version, Issuer: claims.Issuer, KeyID: claims.KeyID, Audience: claims.Audience, EventID: claims.EventID, InteractionID: claims.InteractionID, FactsHash: claims.FactsHash, KnowledgeNetworkIDs: claims.KnowledgeNetworkIDs, IssuedAt: claims.IssuedAt.Unix(), ExpiresAt: claims.ExpiresAt.Unix()}
}

func fromWire(claims wireClaims) Claims {
	return Claims{Version: claims.Version, Issuer: claims.Issuer, KeyID: claims.KeyID, Audience: claims.Audience, EventID: claims.EventID, InteractionID: claims.InteractionID, FactsHash: claims.FactsHash, KnowledgeNetworkIDs: claims.KnowledgeNetworkIDs, IssuedAt: time.Unix(claims.IssuedAt, 0).UTC(), ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC()}
}

func decode(part string, value any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}
