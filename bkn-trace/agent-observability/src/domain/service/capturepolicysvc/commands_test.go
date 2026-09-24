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
func (commandStore) AppendOperationEvent(context.Context, icapturepolicy.OperationEvent) error {
	return nil
}
func (commandStore) UpsertEndpointLease(context.Context, icapturepolicy.EndpointLease) error {
	return nil
}
func (commandStore) RecordAcknowledgement(context.Context, icapturepolicy.ExpectedAcknowledgement) error {
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

func ptr(value uint64) *uint64 { return &value }
