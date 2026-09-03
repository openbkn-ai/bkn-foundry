// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkntrace

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/projectiongrant"
)

func TestProjectionGrantVerifierAllowsOnlyClaimedNetwork(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	token, err := projectiongrant.Sign(projectiongrant.Claims{
		Version: 1, Issuer: "trace-core-projection", KeyID: "key-1", Audience: "bkn-projection-read",
		EventID: "event-1", InteractionID: "interaction-1", FactsHash: "facts-1",
		KnowledgeNetworkIDs: []string{"kn-allowed"}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}, privateKey)
	if err != nil {
		t.Fatal(err)
	}

	verifier := newProjectionGrantVerifier(map[string]ed25519.PublicKey{"key-1": publicKey}, "trace-core-projection", "bkn-projection-read", func() time.Time { return now })
	if _, err := verifier.Authorize(token, "kn-allowed"); err != nil {
		t.Fatalf("claimed network was rejected: %v", err)
	}
	if _, err := verifier.Authorize(token, "kn-other"); err == nil {
		t.Fatal("network outside the grant must be rejected")
	}
}

func TestProjectionGrantVerifierFromEnvRejectsMissingOrMalformedKeys(t *testing.T) {
	t.Setenv("BKN_TRACE_PROJECTION_GRANT_ISSUER", "trace-core-projection")
	t.Setenv("BKN_TRACE_PROJECTION_GRANT_AUDIENCE", "bkn-projection-read")
	t.Setenv("BKN_TRACE_PROJECTION_GRANT_PUBLIC_KEYS", "key-1=not-base64")
	if _, err := NewProjectionGrantVerifierFromEnv(); err == nil {
		t.Fatal("malformed public keys must be rejected")
	}

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BKN_TRACE_PROJECTION_GRANT_PUBLIC_KEYS", "key-1="+base64.StdEncoding.EncodeToString(publicKey))
	if _, err := NewProjectionGrantVerifierFromEnv(); err != nil {
		t.Fatalf("complete public-key configuration was rejected: %v", err)
	}
}
