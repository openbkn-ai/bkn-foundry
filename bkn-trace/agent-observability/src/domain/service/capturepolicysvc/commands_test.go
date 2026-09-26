// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the repository root for the full text.

package capturepolicysvc

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

type commandFactWriter struct{}

func (commandFactWriter) PersistPolicyRevision(context.Context, icapturepolicy.PolicyRevision) error {
	return nil
}
func (commandFactWriter) RegisterProducer(context.Context, icapturepolicy.ProducerRegistration) error {
	return nil
}
func (commandFactWriter) PersistClosureWatermark(context.Context, icapturepolicy.ClosureWatermark) error {
	return nil
}

type commandStore struct{}

func (commandStore) ReadControlState(context.Context) (icapturepolicy.ControlState, error) {
	return icapturepolicy.ControlState{}, nil
}
func (commandStore) StartOperation(context.Context, icapturepolicy.ControlState, icapturepolicy.Operation, []icapturepolicy.ExpectedAcknowledgement) error {
	return nil
}
func (commandStore) AdvanceOperation(context.Context, string, uint64, string, string, string, time.Time) error {
	return nil
}
func (commandStore) BeginRollback(context.Context, string, uint64, uint64, string, []icapturepolicy.ExpectedAcknowledgement, time.Time) error {
	return nil
}
func (commandStore) CompleteSucceeded(context.Context, string, uint64, time.Time) error {
	return nil
}
func (commandStore) CompleteFailed(context.Context, string, uint64, time.Time) error {
	return nil
}
func (commandStore) CompleteRollback(context.Context, string, uint64, time.Time) error {
	return nil
}
func (commandStore) CompleteRollbackFailed(context.Context, string, uint64, time.Time) error {
	return nil
}
func (commandStore) AppendOperationEvent(context.Context, icapturepolicy.OperationEvent) error {
	return nil
}
func (commandStore) UpsertEndpointLease(context.Context, icapturepolicy.EndpointLease) error {
	return nil
}
func (commandStore) RecordAcknowledgement(context.Context, icapturepolicy.ExpectedAcknowledgement) error {
	return nil
}

func (commandStore) RecordEvidencePublisherAcknowledgement(context.Context, icapturepolicy.ExpectedAcknowledgement) error {
	return nil
}

func TestCommandServiceRejectsRevisionThatDoesNotAdvanceCurrentState(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	service := NewCommandService(commandFactWriter{}, commandStore{})
	err := service.StartOperation(context.Background(), icapturepolicy.ControlState{
		CurrentRevision:      4,
		DesiredState:         icapturepolicy.StateEnabled,
		EffectiveState:       icapturepolicy.StateEnabled,
		LastStableRevision:   4,
		CoverageGapUpdatedAt: now,
		UpdatedAt:            now,
	}, icapturepolicy.Operation{
		ID:                  "op-5",
		PolicyRevision:      4,
		RequestedState:      icapturepolicy.StateDisabled,
		ExpectedRevision:    4,
		Phase:               "pending",
		ConvergenceDeadline: now.Add(time.Minute),
		CreatedAt:           now,
		UpdatedAt:           now,
	}, nil)
	if err != icapturepolicy.ErrRevisionConflict {
		t.Fatalf("StartOperation() error = %v, want revision conflict", err)
	}
}

func TestCommandServiceValidatesEndpointKindAndConditionalFacts(t *testing.T) {
	service := NewCommandService(commandFactWriter{}, commandStore{})
	if err := service.UpsertEndpointLease(context.Background(), icapturepolicy.EndpointLease{InstanceID: "i"}); err != icapturepolicy.ErrInvalidEndpointLease {
		t.Fatalf("UpsertEndpointLease() error = %v, want invalid endpoint lease", err)
	}
	ack := icapturepolicy.ExpectedAcknowledgement{
		OperationID:          "op-1",
		EndpointKind:         icapturepolicy.EndpointTraceGateway,
		InstanceID:           "gateway#1",
		WorkloadIdentity:     "sa/gateway",
		ProcessBootID:        "boot-1",
		PolicyRevision:       2,
		AckState:             "disabled",
		LastAcceptedSequence: ptr(uint64(4)),
	}
	if err := service.RecordAcknowledgement(context.Background(), ack); err != icapturepolicy.ErrInvalidAcknowledgement {
		t.Fatalf("RecordAcknowledgement() error = %v, want invalid acknowledgement", err)
	}
}

func TestCaptureControlEnumsAndTransitionsAreClosed(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	if (icapturepolicy.ControlState{CurrentRevision: 1, DesiredState: "draining", EffectiveState: icapturepolicy.StateEnabled, LastStableRevision: 1, CoverageGapUpdatedAt: now, UpdatedAt: now}).Validate() == nil {
		t.Fatal("ControlState accepted a non-contract state")
	}
	if (icapturepolicy.Operation{ID: "op", PolicyRevision: 2, RequestedState: icapturepolicy.StateEnabled, ExpectedRevision: 1, Phase: "draining", ConvergenceDeadline: now, CreatedAt: now, UpdatedAt: now}).Validate() == nil {
		t.Fatal("Operation accepted a non-contract phase")
	}
	for _, transition := range [][2]string{{icapturepolicy.PhasePending, icapturepolicy.PhaseEnabling}, {icapturepolicy.PhaseEnabling, icapturepolicy.PhaseSucceeded}, {icapturepolicy.PhaseDisabling, icapturepolicy.PhaseRollingBack}, {icapturepolicy.PhaseRollingBack, icapturepolicy.PhaseRollbackCompleted}} {
		if !icapturepolicy.CanTransition(transition[0], transition[1]) {
			t.Fatalf("expected transition %s -> %s", transition[0], transition[1])
		}
	}
	for _, transition := range [][2]string{{icapturepolicy.PhaseSucceeded, icapturepolicy.PhaseEnabling}, {icapturepolicy.PhasePending, icapturepolicy.PhaseSucceeded}, {icapturepolicy.PhaseRollingBack, icapturepolicy.PhaseSucceeded}} {
		if icapturepolicy.CanTransition(transition[0], transition[1]) {
			t.Fatalf("unexpected transition %s -> %s", transition[0], transition[1])
		}
	}
}

func TestCaptureControlAcknowledgementsKeepTraceAndEvidenceFactsSeparate(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	exported, dropped, unaccounted := uint64(10), uint64(1), uint64(0)
	trace := icapturepolicy.ExpectedAcknowledgement{OperationID: "op", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 2, AckState: icapturepolicy.AckDisabled, AcknowledgedAt: &now, ExportedCount: &exported, DroppedCount: &dropped, UnaccountedCount: &unaccounted, TraceDisposition: icapturepolicy.DispositionComplete}
	if err := trace.Validate(); err != nil {
		t.Fatalf("valid trace acknowledgement rejected: %v", err)
	}
	trace.LastAcceptedSequence = ptr(3)
	if err := trace.Validate(); err == nil {
		t.Fatal("trace acknowledgement accepted an Evidence sequence field")
	}
	published, queueEmpty := uint64(9), true
	evidence := icapturepolicy.ExpectedAcknowledgement{OperationID: "op", EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "publisher#1", WorkloadIdentity: "sa/publisher", ProcessBootID: "boot-1", PolicyRevision: 2, AckState: icapturepolicy.AckDisabled, AcknowledgedAt: &now, LastAcceptedSequence: ptr(12), PublishedCount: &published, DroppedCount: &dropped, QueueEmpty: &queueEmpty, EvidenceDisposition: icapturepolicy.DispositionComplete}
	if err := evidence.Validate(); err != nil {
		t.Fatalf("valid evidence acknowledgement rejected: %v", err)
	}
	evidence.TraceDisposition = icapturepolicy.DispositionComplete
	if err := evidence.Validate(); err == nil {
		t.Fatal("evidence acknowledgement accepted a Trace disposition field")
	}
}

func ptr(value uint64) *uint64 { return &value }
