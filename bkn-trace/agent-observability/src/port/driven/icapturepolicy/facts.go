// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package icapturepolicy

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidPolicyRevision       = errors.New("invalid capture policy revision")
	ErrInvalidProducerRegistration = errors.New("invalid producer registration")
	ErrInvalidClosureWatermark     = errors.New("invalid closure watermark")
	ErrCaptureFactConflict         = errors.New("capture control fact conflicts with persisted state")
)

type PolicyRevision struct {
	Revision         uint64
	AdmissionEnabled bool
	RecordedAt       time.Time
}

type ProducerRegistration struct {
	InstanceID        string
	PolicyRevision    uint64
	ProcessBootID     string
	RegistrationState string
	RegisteredAt      time.Time
	RevokedAt         *time.Time
}

type ClosureWatermark struct {
	InstanceID           string
	PolicyRevision       uint64
	LastAcceptedSequence uint64
	ClosedAt             time.Time
	AcknowledgedAt       time.Time
}

type PolicyRevisionWriter interface {
	PersistPolicyRevision(context.Context, PolicyRevision) error
}

type ProducerRegistrationWriter interface {
	RegisterProducer(context.Context, ProducerRegistration) error
}

type ClosureWatermarkWriter interface {
	PersistClosureWatermark(context.Context, ClosureWatermark) error
}

type FactWriter interface {
	PolicyRevisionWriter
	ProducerRegistrationWriter
	ClosureWatermarkWriter
}

func (p PolicyRevision) Validate() error {
	if p.Revision == 0 || p.RecordedAt.IsZero() {
		return ErrInvalidPolicyRevision
	}
	return nil
}

func (p ProducerRegistration) Validate() error {
	if p.InstanceID == "" || p.PolicyRevision == 0 || p.ProcessBootID == "" || p.RegistrationState == "" || p.RegisteredAt.IsZero() {
		return ErrInvalidProducerRegistration
	}
	if p.RevokedAt != nil && p.RevokedAt.Before(p.RegisteredAt) {
		return ErrInvalidProducerRegistration
	}
	return nil
}

func (p ClosureWatermark) Validate() error {
	if p.InstanceID == "" || p.PolicyRevision == 0 || p.ClosedAt.IsZero() || p.AcknowledgedAt.IsZero() || p.AcknowledgedAt.Before(p.ClosedAt) {
		return ErrInvalidClosureWatermark
	}
	return nil
}
