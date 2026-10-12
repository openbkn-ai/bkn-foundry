// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package traceadmissionsvc

import (
	"testing"
	"time"
)

func TestGatewayDropsWhenPolicyIsDisabledOrExpired(t *testing.T) {
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
			gateway := NewGateway(GatewayConfig{Now: func() time.Time { return currentTime }})
			expires := now.Add(time.Minute)
			snapshot := Snapshot{ContractVersion: ContractVersion, Revision: 2, TraceAdmission: test.mode, EvidenceAdmission: test.mode, IssuedAt: now.Add(-time.Second), ExpiresAt: expires}
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

func TestGatewayRejectsStaleAndInvalidModeSnapshots(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	gateway := NewGateway(GatewayConfig{Now: func() time.Time { return now }})
	valid := Snapshot{ContractVersion: ContractVersion, Revision: 3, TraceAdmission: ModeEnabled, EvidenceAdmission: ModeEnabled, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
	if err := gateway.Apply(valid); err != nil {
		t.Fatal(err)
	}
	stale := valid
	stale.Revision = 2
	if err := gateway.Apply(stale); err != ErrStaleRevision {
		t.Fatalf("stale Apply() error = %v", err)
	}
	wrongAudience := valid
	wrongAudience.Revision = 4
	wrongAudience.EvidenceAdmission = ModeDisabled
	if err := gateway.Apply(wrongAudience); err != ErrInvalidSnapshot {
		t.Fatalf("wrong audience Apply() error = %v", err)
	}
}

func TestGatewayAcceptsFreshSameRevisionAndRenewsExpiry(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	gateway := NewGateway(GatewayConfig{Now: func() time.Time { return now }})
	first := Snapshot{ContractVersion: ContractVersion, Revision: 3, TraceAdmission: ModeEnabled, EvidenceAdmission: ModeEnabled, IssuedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := gateway.Apply(first); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	refreshed := first
	refreshed.IssuedAt = now
	refreshed.ExpiresAt = now.Add(time.Minute)
	if err := gateway.Apply(refreshed); err != nil {
		t.Fatalf("fresh snapshot of the same policy revision was rejected: %v", err)
	}
	now = first.ExpiresAt.Add(time.Second)
	if decision := gateway.Admit(1); decision.Accepted != 1 {
		t.Fatalf("renewed policy expired at the first snapshot's deadline: %+v", decision)
	}
}

func TestGatewayRejectsSameRevisionPolicyChangeAndIgnoresOlderRefresh(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 30, 0, time.UTC)
	gateway := NewGateway(GatewayConfig{Now: func() time.Time { return now }})
	current := Snapshot{ContractVersion: ContractVersion, Revision: 3, TraceAdmission: ModeEnabled, EvidenceAdmission: ModeEnabled, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
	if err := gateway.Apply(current); err != nil {
		t.Fatal(err)
	}
	changed := current
	changed.TraceAdmission, changed.EvidenceAdmission = ModeDisabled, ModeDisabled
	changed.IssuedAt = now
	changed.ExpiresAt = now.Add(2 * time.Minute)
	if err := gateway.Apply(changed); err != ErrStaleRevision {
		t.Fatalf("changed policy with unchanged revision error = %v", err)
	}
	older := current
	older.IssuedAt = now.Add(-2 * time.Second)
	older.ExpiresAt = now.Add(30 * time.Second)
	if err := gateway.Apply(older); err != nil {
		t.Fatalf("older same-revision snapshot interrupted heartbeat: %v", err)
	}
	now = older.ExpiresAt.Add(time.Second)
	if decision := gateway.Admit(1); decision.Accepted != 1 {
		t.Fatalf("rejected/ignored snapshots changed the active policy: %+v", decision)
	}
}

func TestGatewayAcceptsSameRevisionRefreshWhenSnapshotTTLShortens(t *testing.T) {
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	gateway := NewGateway(GatewayConfig{Now: func() time.Time { return now }})
	longTTL := Snapshot{ContractVersion: ContractVersion, Revision: 3, TraceAdmission: ModeEnabled, EvidenceAdmission: ModeEnabled, IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute)}
	if err := gateway.Apply(longTTL); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	shortTTL := longTTL
	shortTTL.IssuedAt = now
	shortTTL.ExpiresAt = now.Add(5 * time.Minute)
	if err := gateway.Apply(shortTTL); err != nil {
		t.Fatalf("same-revision policy with shorter configured TTL was rejected: %v", err)
	}
	now = shortTTL.ExpiresAt.Add(time.Second)
	if decision := gateway.Admit(1); decision.Reason != ReasonPolicyExpired {
		t.Fatalf("shorter valid TTL was not applied: %+v", decision)
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
	now := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	gateway := NewGateway(GatewayConfig{Now: func() time.Time { return now }})
	snapshot := Snapshot{ContractVersion: ContractVersion, Revision: 12, TraceAdmission: ModeEnabled, EvidenceAdmission: ModeEnabled, IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(time.Minute)}
	if err := gateway.Apply(snapshot); err != nil {
		t.Fatal(err)
	}
	if gateway.ReadyFor(11) || !gateway.ReadyFor(12) {
		t.Fatal("gateway accepted an old pod revision as ready")
	}
}

func intPtr(value int) *int { return &value }

func TestGatewayValidatesUnsignedSnapshotStructureAndFreshness(t *testing.T) {
	now := time.Date(2026, 10, 8, 2, 0, 0, 0, time.UTC)
	valid := Snapshot{ContractVersion: ContractVersion, Revision: 1, TraceAdmission: ModeEnabled, EvidenceAdmission: ModeEnabled, IssuedAt: now, ExpiresAt: now.Add(time.Minute)}
	for _, tc := range []struct {
		name   string
		change func(*Snapshot)
	}{
		{"contract", func(s *Snapshot) { s.ContractVersion = "unknown" }},
		{"zero revision", func(s *Snapshot) { s.Revision = 0 }},
		{"invalid mode", func(s *Snapshot) { s.TraceAdmission = "unknown" }},
		{"inconsistent modes", func(s *Snapshot) { s.EvidenceAdmission = ModeDisabled }},
		{"missing issuance", func(s *Snapshot) { s.IssuedAt = time.Time{} }},
		{"future issuance", func(s *Snapshot) { s.IssuedAt = now.Add(time.Second) }},
		{"expired", func(s *Snapshot) { s.IssuedAt = now.Add(-time.Minute); s.ExpiresAt = now }},
		{"invalid deadline", func(s *Snapshot) { s.ExpiresAt = s.IssuedAt }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := valid
			tc.change(&s)
			g := NewGateway(GatewayConfig{Now: func() time.Time { return now }})
			if err := g.Apply(s); err != ErrInvalidSnapshot {
				t.Fatalf("Apply()=%v", err)
			}
			if g.Admit(1).Reason != ReasonPolicyMissing {
				t.Fatal("invalid snapshot became active")
			}
		})
	}
}
