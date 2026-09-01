// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/projectiongrant"
)

// projectionGrantVerifier validates the narrow capability that permits an EE
// projection builder to read one explicitly recorded knowledge network.
type ProjectionGrantVerifier struct {
	keys     map[string]ed25519.PublicKey
	issuer   string
	audience string
	now      func() time.Time
}

func newProjectionGrantVerifier(
	keys map[string]ed25519.PublicKey,
	issuer string,
	audience string,
	now func() time.Time,
) ProjectionGrantVerifier {
	return ProjectionGrantVerifier{keys: keys, issuer: issuer, audience: audience, now: now}
}

// NewProjectionGrantVerifierFromEnv loads BKN's verification-only key table.
// The comma-separated value is kid=base64-ed25519-public-key. It intentionally
// has no private-key input.
func NewProjectionGrantVerifierFromEnv() (ProjectionGrantVerifier, error) {
	issuer := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_ISSUER"))
	audience := strings.TrimSpace(os.Getenv("BKN_TRACE_PROJECTION_GRANT_AUDIENCE"))
	if issuer == "" || audience == "" {
		return ProjectionGrantVerifier{}, errors.New("projection grant issuer and audience are required")
	}
	keys := make(map[string]ed25519.PublicKey)
	for _, entry := range strings.Split(os.Getenv("BKN_TRACE_PROJECTION_GRANT_PUBLIC_KEYS"), ",") {
		kid, encoded, found := strings.Cut(strings.TrimSpace(entry), "=")
		kid, encoded = strings.TrimSpace(kid), strings.TrimSpace(encoded)
		if !found || kid == "" || encoded == "" {
			return ProjectionGrantVerifier{}, errors.New("projection grant public keys must use kid=base64 format")
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return ProjectionGrantVerifier{}, fmt.Errorf("projection grant public key %q is invalid", kid)
		}
		if _, duplicate := keys[kid]; duplicate {
			return ProjectionGrantVerifier{}, fmt.Errorf("projection grant public key %q is duplicated", kid)
		}
		keys[kid] = ed25519.PublicKey(decoded)
	}
	if len(keys) == 0 {
		return ProjectionGrantVerifier{}, errors.New("projection grant public keys are required")
	}
	return newProjectionGrantVerifier(keys, issuer, audience, time.Now), nil
}

func (v ProjectionGrantVerifier) Authorize(token string, knowledgeNetworkID string) (projectiongrant.Claims, error) {
	if strings.TrimSpace(token) == "" {
		return projectiongrant.Claims{}, errors.New("projection grant is required")
	}
	if v.now == nil {
		v.now = time.Now
	}
	claims, err := projectiongrant.Verify(token, v.keys, projectiongrant.VerifyOptions{
		Now: v.now(), ExpectedIssuer: v.issuer, ExpectedAudience: v.audience,
	})
	if err != nil {
		return projectiongrant.Claims{}, err
	}
	if !claims.AllowsKnowledgeNetwork(knowledgeNetworkID) {
		return projectiongrant.Claims{}, errors.New("projection grant does not allow this knowledge network")
	}
	return claims, nil
}
