// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package entitlement

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/openbkn-ai/licverify"
)

// mintLicence signs a payload with a throwaway key, in the wire format
// licverify parses: v1.<base64url(payload)>.<base64url(signature)>.
func mintLicence(t *testing.T, p licverify.Payload) (string, map[string]ed25519.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	p.Kid = "test"
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	text := "v1." + base64.RawURLEncoding.EncodeToString(body) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, body))
	return text, map[string]ed25519.PublicKey{"test": pub}
}

func TestEvaluateValidLicence(t *testing.T) {
	now := time.Now()
	text, keys := mintLicence(t, licverify.Payload{
		LicID:     "l1",
		Edition:   licverify.EditionEnterprise,
		IssuedAt:  now.Add(-time.Hour).Unix(),
		ExpiresAt: now.Add(30 * 24 * time.Hour).Unix(),
		Features:  []string{"audit", "perm_object_level"},
	})

	snap := evaluate(text, keys)
	if !snap.Licensed || snap.Edition != licverify.EditionEnterprise {
		t.Fatalf("snapshot = %+v, want licensed enterprise", snap)
	}
	if snap.State != licverify.StateValid {
		t.Fatalf("State = %q, want valid", snap.State)
	}
	if len(snap.Features) != 2 {
		t.Fatalf("Features = %v, want both carried through for display", snap.Features)
	}
}

func TestEvaluateGraceStillServes(t *testing.T) {
	now := time.Now()
	// Expired yesterday: the renewal has not landed yet. Taking the customer's
	// capability away over that is a support incident, not enforcement.
	text, keys := mintLicence(t, licverify.Payload{
		LicID:     "l2",
		Edition:   licverify.EditionProfessional,
		IssuedAt:  now.Add(-90 * 24 * time.Hour).Unix(),
		ExpiresAt: now.Add(-24 * time.Hour).Unix(),
	})

	snap := evaluate(text, keys)
	if snap.State != licverify.StateGrace {
		t.Fatalf("State = %q, want grace", snap.State)
	}
	if !snap.Licensed || snap.Edition != licverify.EditionProfessional {
		t.Fatalf("a licence inside its grace window must keep serving, got %+v", snap)
	}
}

func TestEvaluateFallsBackToCommunityPastGrace(t *testing.T) {
	now := time.Now()
	text, keys := mintLicence(t, licverify.Payload{
		LicID:     "l3",
		Edition:   licverify.EditionEnterprise,
		IssuedAt:  now.Add(-400 * 24 * time.Hour).Unix(),
		ExpiresAt: now.Add(-200 * 24 * time.Hour).Unix(),
	})

	snap := evaluate(text, keys)
	if snap.Licensed {
		t.Fatal("a licence long past grace must not carry paid capability")
	}
	if snap.Edition != licverify.EditionCommunity {
		t.Fatalf("Edition = %q, want community", snap.Edition)
	}
	// The distinction survives for the operator: "expired" reads differently
	// from "never installed", even though the product behaves the same.
	if snap.State != licverify.StateFallback {
		t.Fatalf("State = %q, want fallback_community", snap.State)
	}
}

func TestEvaluateRejectsUnsignedText(t *testing.T) {
	// Garbage, a forged certificate and an unknown signing key all land here.
	// The product still runs — community capability is never withheld — but
	// nothing paid turns on.
	snap := evaluate("not-a-licence", map[string]ed25519.PublicKey{})
	if snap.Licensed || snap.Edition != licverify.EditionCommunity {
		t.Fatalf("unverifiable text must yield community, got %+v", snap)
	}
	if snap.State != licverify.StateInvalid {
		t.Fatalf("State = %q, want invalid", snap.State)
	}
}

func TestEvaluateIgnoresAnUnknownTier(t *testing.T) {
	now := time.Now()
	// A certificate naming a tier this build predates: serve community rather
	// than guess, and do not fail to parse the whole licence over it.
	text, keys := mintLicence(t, licverify.Payload{
		LicID:     "l4",
		Edition:   licverify.Edition("galactic"),
		IssuedAt:  now.Add(-time.Hour).Unix(),
		ExpiresAt: now.Add(24 * time.Hour).Unix(),
	})

	snap := evaluate(text, keys)
	if !snap.Licensed {
		t.Fatal("the certificate is valid; only its tier is unrecognised")
	}
	if snap.Edition.AtLeast(licverify.EditionProfessional) {
		t.Fatal("an unrecognised tier must not clear a paid bar")
	}
}
