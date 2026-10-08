// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package traceadmissionsvc contains the traces-only admission decision used
// by the OCB processor adapter. It has no Evidence, log, Audit or persistence
// responsibilities.
package traceadmissionsvc

import (
	"errors"
	"sync"
	"time"
)

type Mode string

const (
	ModeEnabled  Mode = "enabled"
	ModeDisabled Mode = "disabled"
)

const ContractVersion = "TraceEvidencePolicySnapshotV1"

const (
	ReasonAccepted       = "accepted"
	ReasonPolicyDisabled = "policy_disabled"
	ReasonPolicyExpired  = "policy_expired"
	ReasonPolicyMissing  = "policy_unavailable"
)

var (
	ErrInvalidSnapshot = errors.New("invalid trace admission policy snapshot")
	ErrStaleRevision   = errors.New("stale trace admission policy revision")
	ErrInvalidAck      = errors.New("invalid trace gateway acknowledgement")
)

type Snapshot struct {
	ContractVersion   string    `json:"contract_version"`
	Revision          uint64    `json:"revision"`
	TraceAdmission    Mode      `json:"trace_admission"`
	EvidenceAdmission Mode      `json:"evidence_admission"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type GatewayConfig struct{ Now func() time.Time }

type Gateway struct {
	mu      sync.RWMutex
	now     func() time.Time
	current *Snapshot
}

func NewGateway(config GatewayConfig) *Gateway {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Gateway{now: now}
}

func (g *Gateway) Apply(snapshot Snapshot) error {
	if g == nil || !g.verify(snapshot) {
		return ErrInvalidSnapshot
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.current != nil && snapshot.Revision < g.current.Revision {
		return ErrStaleRevision
	}
	if g.current != nil && snapshot.Revision == g.current.Revision {
		if *g.current == snapshot {
			return nil
		}
		// The control plane refreshes the same policy revision on each read.
		// A changed mode requires a new policy revision. For unchanged modes,
		// ignore an older issuance (for example, from a skewed server replica)
		// without interrupting the heartbeat. A newer issuance replaces the
		// expiry even when the snapshot TTL has been shortened.
		if snapshot.TraceAdmission != g.current.TraceAdmission ||
			snapshot.EvidenceAdmission != g.current.EvidenceAdmission {
			return ErrStaleRevision
		}
		if !snapshot.IssuedAt.After(g.current.IssuedAt) {
			return nil
		}
	}
	g.current = &snapshot
	return nil
}

func (g *Gateway) verify(snapshot Snapshot) bool {
	now := g.now().UTC()
	if snapshot.ContractVersion != ContractVersion || snapshot.Revision == 0 || (snapshot.TraceAdmission != ModeEnabled && snapshot.TraceAdmission != ModeDisabled) || snapshot.EvidenceAdmission != snapshot.TraceAdmission || snapshot.IssuedAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.IssuedAt) || now.Before(snapshot.IssuedAt) || !now.Before(snapshot.ExpiresAt) {
		return false
	}
	return true
}

type AdmissionDecision struct {
	Accepted int
	Dropped  int
	Reason   string
}

func (g *Gateway) Admit(spanCount int) AdmissionDecision {
	if spanCount < 0 {
		spanCount = 0
	}
	g.mu.RLock()
	snapshot := g.current
	g.mu.RUnlock()
	if snapshot == nil {
		return AdmissionDecision{Dropped: spanCount, Reason: ReasonPolicyMissing}
	}
	if !g.now().UTC().Before(snapshot.ExpiresAt) {
		return AdmissionDecision{Dropped: spanCount, Reason: ReasonPolicyExpired}
	}
	if snapshot.TraceAdmission == ModeDisabled {
		return AdmissionDecision{Dropped: spanCount, Reason: ReasonPolicyDisabled}
	}
	return AdmissionDecision{Accepted: spanCount, Reason: ReasonAccepted}
}

// ReadyFor is used by the collector adapter when reporting an instance ack.
// An old process that has not applied the current revision is never treated
// as a healthy replacement for the frozen expected instance set.
func (g *Gateway) ReadyFor(revision uint64) bool {
	g.mu.RLock()
	snapshot := g.current
	g.mu.RUnlock()
	return snapshot != nil && snapshot.Revision == revision && g.now().UTC().Before(snapshot.ExpiresAt)
}

type QueueDispositionStatus string

const (
	QueueComplete      QueueDispositionStatus = "complete"
	QueueGap           QueueDispositionStatus = "gap"
	QueueNotApplicable QueueDispositionStatus = "not_applicable"
)

type QueueDisposition struct {
	Status      QueueDispositionStatus
	Exported    int
	Dropped     int
	Unaccounted *int
}

type Acknowledgement struct {
	Mode  Mode
	Queue QueueDisposition
}

func ValidateAcknowledgement(ack Acknowledgement) error {
	if ack.Mode == ModeEnabled {
		if ack.Queue.Status != QueueNotApplicable {
			return ErrInvalidAck
		}
		return nil
	}
	if ack.Mode != ModeDisabled {
		return ErrInvalidAck
	}
	switch ack.Queue.Status {
	case QueueComplete:
		if ack.Queue.Unaccounted == nil || *ack.Queue.Unaccounted != 0 {
			return ErrInvalidAck
		}
	case QueueGap:
		// Unknown is represented by null; a known non-zero value is not a gap.
		if ack.Queue.Unaccounted != nil && *ack.Queue.Unaccounted == 0 {
			return ErrInvalidAck
		}
	default:
		return ErrInvalidAck
	}
	return nil
}
