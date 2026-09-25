// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

// Package traceadmissionsvc contains the traces-only admission decision used
// by the OCB processor adapter. It has no Evidence, log, Audit or persistence
// responsibilities.
package traceadmissionsvc

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
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

type SignedSnapshot struct {
	ContractVersion   string    `json:"contract_version"`
	Revision          uint64    `json:"revision"`
	TraceAdmission    Mode      `json:"trace_admission"`
	EvidenceAdmission Mode      `json:"evidence_admission"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	KeyID             string    `json:"key_id"`
	Audience          string    `json:"audience_cluster_id"`
	Signature         string    `json:"signature"`
}

func (s SignedSnapshot) canonicalBytes() []byte {
	payload := struct {
		ContractVersion   string    `json:"contract_version"`
		Revision          uint64    `json:"revision"`
		TraceAdmission    Mode      `json:"trace_admission"`
		EvidenceAdmission Mode      `json:"evidence_admission"`
		IssuedAt          time.Time `json:"issued_at"`
		ExpiresAt         time.Time `json:"expires_at"`
		KeyID             string    `json:"key_id"`
		Audience          string    `json:"audience_cluster_id"`
	}{s.ContractVersion, s.Revision, s.TraceAdmission, s.EvidenceAdmission, s.IssuedAt, s.ExpiresAt, s.KeyID, s.Audience}
	bytes, _ := json.Marshal(payload)
	return bytes
}

type GatewayConfig struct {
	Audience      string
	CurrentKeyID  string
	CurrentKey    ed25519.PublicKey
	PreviousKeyID string
	PreviousKey   ed25519.PublicKey
	Now           func() time.Time
}

type Gateway struct {
	mu      sync.RWMutex
	cfg     GatewayConfig
	now     func() time.Time
	current *SignedSnapshot
}

func NewGateway(config GatewayConfig) *Gateway {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Gateway{cfg: config, now: now}
}

func (g *Gateway) Apply(snapshot SignedSnapshot) error {
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
		return ErrStaleRevision
	}
	g.current = &snapshot
	return nil
}

func (g *Gateway) verify(snapshot SignedSnapshot) bool {
	now := g.now().UTC()
	if snapshot.ContractVersion != ContractVersion || snapshot.Revision == 0 || (snapshot.TraceAdmission != ModeEnabled && snapshot.TraceAdmission != ModeDisabled) || snapshot.EvidenceAdmission != snapshot.TraceAdmission || snapshot.KeyID == "" || snapshot.Audience != g.cfg.Audience || snapshot.IssuedAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.IssuedAt) || now.Before(snapshot.IssuedAt) || !now.Before(snapshot.ExpiresAt) {
		return false
	}
	key := g.cfg.CurrentKey
	if snapshot.KeyID != g.cfg.CurrentKeyID {
		if snapshot.KeyID != g.cfg.PreviousKeyID {
			return false
		}
		key = g.cfg.PreviousKey
	}
	if len(key) != ed25519.PublicKeySize {
		return false
	}
	if len(snapshot.Signature) <= len("ed25519:") || snapshot.Signature[:len("ed25519:")] != "ed25519:" {
		return false
	}
	signatureText := snapshot.Signature[len("ed25519:"):]
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil {
		signature, err = base64.StdEncoding.DecodeString(signatureText)
	}
	return err == nil && len(signature) == ed25519.SignatureSize && ed25519.Verify(key, snapshot.canonicalBytes(), signature)
}

// SignSnapshot signs the canonical policy snapshot with the dedicated Trace
// capture policy key. Projection-grant keys are intentionally not accepted by
// this API; callers provide an independent key and key ID/audience.
func SignSnapshot(snapshot SignedSnapshot, privateKey ed25519.PrivateKey) (SignedSnapshot, error) {
	if snapshot.ContractVersion == "" {
		snapshot.ContractVersion = ContractVersion
	}
	if snapshot.ContractVersion != ContractVersion || snapshot.Revision == 0 || (snapshot.TraceAdmission != ModeEnabled && snapshot.TraceAdmission != ModeDisabled) || snapshot.EvidenceAdmission != snapshot.TraceAdmission || snapshot.KeyID == "" || snapshot.Audience == "" || snapshot.IssuedAt.IsZero() || !snapshot.ExpiresAt.After(snapshot.IssuedAt) || len(privateKey) != ed25519.PrivateKeySize {
		return SignedSnapshot{}, ErrInvalidSnapshot
	}
	snapshot.Signature = "ed25519:" + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, snapshot.canonicalBytes()))
	return snapshot, nil
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
