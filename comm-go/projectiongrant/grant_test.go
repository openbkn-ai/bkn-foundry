// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package projectiongrant

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestSignAndVerifyBindsGrantToExactProjectionNetworks(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	grant := Claims{
		Version:             1,
		Issuer:              "trace-core-projection",
		KeyID:               "key-2026-08",
		Audience:            "bkn-projection-read",
		EventID:             "event-1",
		InteractionID:       "interaction-1",
		FactsHash:           "f3e5b2db0dc9b053f80d6be8fdbbc21df6d5232263ed2eef2eb5a2f4183b31ce",
		KnowledgeNetworkIDs: []string{"kn-b", "kn-a", "kn-a"},
		IssuedAt:            now,
		ExpiresAt:           now.Add(time.Hour),
	}

	token, err := Sign(grant, privateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	verified, err := Verify(token, map[string]ed25519.PublicKey{"key-2026-08": publicKey}, VerifyOptions{
		Now: now.Add(time.Minute), ExpectedIssuer: "trace-core-projection", ExpectedAudience: "bkn-projection-read",
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !verified.AllowsKnowledgeNetwork("kn-a") || !verified.AllowsKnowledgeNetwork("kn-b") || verified.AllowsKnowledgeNetwork("kn-c") {
		t.Fatalf("grant widened or lost its network scope: %#v", verified)
	}
	if len(verified.KnowledgeNetworkIDs) != 2 || verified.KnowledgeNetworkIDs[0] != "kn-a" || verified.KnowledgeNetworkIDs[1] != "kn-b" {
		t.Fatalf("grant networks were not canonicalized: %#v", verified.KnowledgeNetworkIDs)
	}
}

func TestVerifyRejectsExpiredOrWrongAudienceGrant(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	now := time.Date(2026, 8, 31, 1, 0, 0, 0, time.UTC)
	token, err := Sign(Claims{
		Version: 1, Issuer: "trace-core-projection", KeyID: "key-1", Audience: "bkn-projection-read",
		EventID: "event-1", InteractionID: "interaction-1", FactsHash: "hash", KnowledgeNetworkIDs: []string{"kn-1"},
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute),
	}, privateKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err = Verify(token, map[string]ed25519.PublicKey{"key-1": publicKey}, VerifyOptions{
		Now: now, ExpectedIssuer: "trace-core-projection", ExpectedAudience: "bkn-projection-read",
	}); err == nil {
		t.Fatal("expired grant was accepted")
	}
	if _, err = Verify(token, map[string]ed25519.PublicKey{"key-1": publicKey}, VerifyOptions{
		Now: now.Add(-2 * time.Hour), ExpectedIssuer: "trace-core-projection", ExpectedAudience: "another-service",
	}); err == nil {
		t.Fatal("wrong audience grant was accepted")
	}
}
