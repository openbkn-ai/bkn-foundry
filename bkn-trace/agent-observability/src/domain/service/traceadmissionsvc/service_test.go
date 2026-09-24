// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package traceadmissionsvc

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func TestGatewayDropsWhenPolicyIsDisabledOrExpired(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		mode    Mode
		expired bool
		reason  string
	}{
		{name: "disabled", mode: ModeDisabled, reason: ReasonPolicyDisabled},
		{name: "expired", mode: ModeEnabled, expired: true, reason: ReasonPolicyExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			currentTime := now
			gateway := NewGateway(GatewayConfig{Audience: "cluster-a", CurrentKeyID: "k1", CurrentKey: publicKey, Now: func() time.Time { return currentTime }})
			expires := now.Add(time.Minute)
			snapshot := SignedSnapshot{Revision: 2, Mode: test.mode, IssuedAt: now.Add(-time.Second), ExpiresAt: expires, KeyID: "k1", Audience: "cluster-a"}
			snapshot.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, snapshot.canonicalBytes()))
			if err := gateway.Apply(snapshot); err != nil {
				t.Fatal(err)
			}
			if test.expired {
				currentTime = expires.Add(time.Second)
			}
			decision := gateway.Admit(3)
			if decision.Accepted != 0 || decision.Dropped != 3 || decision.Reason != test.reason {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}
}

func TestGatewayRejectsStaleAndInvalidAudienceSnapshots(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	gateway := NewGateway(GatewayConfig{Audience: "cluster-a", CurrentKeyID: "k1", CurrentKey: publicKey, Now: func() time.Time { return now }})
	valid := SignedSnapshot{Revision: 3, Mode: ModeEnabled, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), KeyID: "k1", Audience: "cluster-a"}
	valid.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, valid.canonicalBytes()))
	if err := gateway.Apply(valid); err != nil {
		t.Fatal(err)
	}
	stale := valid
	stale.Revision = 2
	stale.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, stale.canonicalBytes()))
	if err := gateway.Apply(stale); err != ErrStaleRevision {
		t.Fatalf("stale Apply() error = %v", err)
	}
	wrongAudience := valid
	wrongAudience.Revision = 4
	wrongAudience.Audience = "cluster-b"
	wrongAudience.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, wrongAudience.canonicalBytes()))
	if err := gateway.Apply(wrongAudience); err != ErrInvalidSnapshot {
		t.Fatalf("wrong audience Apply() error = %v", err)
	}
}

func TestDisabledAckRequiresCompleteQueueDisposition(t *testing.T) {
	complete := QueueDisposition{Status: QueueComplete, Exported: 4, Dropped: 1, Unaccounted: intPtr(0)}
	if err := ValidateAcknowledgement(Acknowledgement{Mode: ModeDisabled, Queue: complete}); err != nil {
		t.Fatalf("complete acknowledgement rejected: %v", err)
	}
	gap := QueueDisposition{Status: QueueGap, Unaccounted: nil}
	if err := ValidateAcknowledgement(Acknowledgement{Mode: ModeDisabled, Queue: gap}); err != nil {
		t.Fatalf("gap acknowledgement rejected: %v", err)
	}
	invalid := QueueDisposition{Status: QueueComplete, Unaccounted: intPtr(1)}
	if err := ValidateAcknowledgement(Acknowledgement{Mode: ModeDisabled, Queue: invalid}); err == nil {
		t.Fatal("complete acknowledgement with unaccounted spans was accepted")
	}
}

func TestGatewayDoesNotReportOldPodReadyForNewRevision(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	gateway := NewGateway(GatewayConfig{Audience: "cluster-a", CurrentKeyID: "k1", CurrentKey: publicKey, Now: func() time.Time { return now }})
	snapshot := SignedSnapshot{Revision: 12, Mode: ModeEnabled, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute), KeyID: "k1", Audience: "cluster-a"}
	snapshot.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, snapshot.canonicalBytes()))
	if err := gateway.Apply(snapshot); err != nil {
		t.Fatal(err)
	}
	if gateway.ReadyFor(11) || !gateway.ReadyFor(12) {
		t.Fatal("gateway accepted an old pod revision as ready")
	}
}

func intPtr(value int) *int { return &value }
