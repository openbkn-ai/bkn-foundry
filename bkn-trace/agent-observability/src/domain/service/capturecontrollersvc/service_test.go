// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package capturecontrollersvc

import (
	"context"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

type fakeStore struct {
	state icapturepolicy.ControlState
	op    icapturepolicy.Operation
	acks  []icapturepolicy.ExpectedAcknowledgement
	now   time.Time
}

func (f *fakeStore) ReadControlState(context.Context) (icapturepolicy.ControlState, error) {
	return f.state, nil
}
func (f *fakeStore) NextCapturePolicyRevision(context.Context) (uint64, error) {
	return f.state.CurrentRevision + 1, nil
}
func (f *fakeStore) ListReadyEndpointLeases(context.Context, time.Time) ([]icapturepolicy.EndpointLease, error) {
	return nil, nil
}
func (f *fakeStore) StartOperation(_ context.Context, _ icapturepolicy.ControlState, operation icapturepolicy.Operation, expected []icapturepolicy.ExpectedAcknowledgement) error {
	f.op, f.acks = operation, expected
	f.state.CurrentRevision, f.state.DesiredState, f.state.ActiveOperationID = operation.PolicyRevision, operation.RequestedState, operation.ID
	return nil
}
func (f *fakeStore) ClaimCapturePolicyOperation(_ context.Context, workerID string, lease time.Duration, now time.Time) (icapturepolicy.Operation, bool, error) {
	if f.state.ActiveOperationID == "" {
		return icapturepolicy.Operation{}, false, nil
	}
	if f.op.Phase == icapturepolicy.PhasePending {
		if f.op.RequestedState == icapturepolicy.StateEnabled {
			f.op.Phase = icapturepolicy.PhaseEnabling
		} else {
			f.op.Phase = icapturepolicy.PhaseDisabling
		}
	}
	f.op.LeaseOwner, f.op.LeaseToken, f.op.LeaseExpiresAt = workerID, f.op.LeaseToken+1, ptr(now.Add(lease))
	return f.op, true, nil
}
func (f *fakeStore) ReadCapturePolicyAcknowledgements(context.Context, string, uint64) ([]icapturepolicy.ExpectedAcknowledgement, error) {
	return append([]icapturepolicy.ExpectedAcknowledgement(nil), f.acks...), nil
}
func (f *fakeStore) BeginRollback(_ context.Context, _ string, token, revision uint64, restored string, expected []icapturepolicy.ExpectedAcknowledgement, now time.Time) error {
	f.op.Phase, f.op.CompensationRevision, f.op.RestoredState, f.op.LeaseToken, f.op.ConvergenceDeadline = icapturepolicy.PhaseRollingBack, revision, restored, token, now.Add(10*time.Minute)
	f.state.CurrentRevision, f.state.DesiredState = revision, restored
	f.acks = expected
	return nil
}
func (f *fakeStore) CompleteSucceeded(context.Context, string, uint64, time.Time) error {
	f.op.Phase, f.state.EffectiveState, f.state.ActiveOperationID = icapturepolicy.PhaseSucceeded, f.op.RequestedState, ""
	return nil
}
func (f *fakeStore) CompleteFailed(context.Context, string, uint64, time.Time) error {
	f.op.Phase, f.state.ActiveOperationID = icapturepolicy.PhaseFailed, ""
	return nil
}
func (f *fakeStore) CompleteRollback(context.Context, string, uint64, time.Time) error {
	f.op.Phase, f.state.EffectiveState, f.state.ActiveOperationID = icapturepolicy.PhaseRollbackCompleted, f.op.RestoredState, ""
	return nil
}
func (f *fakeStore) CompleteRollbackFailed(context.Context, string, uint64, time.Time) error {
	f.op.Phase, f.state.ActiveOperationID = icapturepolicy.PhaseRollbackFailed, ""
	return nil
}

func ptr(value time.Time) *time.Time { return &value }

func TestControllerRequestAndReconcileUsesFrozenExpectedSet(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	store := &fakeStore{state: icapturepolicy.ControlState{CurrentRevision: 1, DesiredState: icapturepolicy.StateEnabled, EffectiveState: icapturepolicy.StateEnabled, LastStableRevision: 1, ActiveOperationID: "", CoverageGapUpdatedAt: now, UpdatedAt: now}}
	controller, err := New(Options{Store: store, WorkerID: "controller-1", Lease: time.Minute, Convergence: time.Minute, Targets: []Target{{EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway-1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1"}}, Now: func() time.Time { return now }, OperationID: func() (string, error) { return "op-1", nil }})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := controller.Request(context.Background(), capturepolicysvc.ChangeRequest{DesiredState: capturepolicysvc.StateDisabled, ExpectedRevision: 1})
	if err != nil || snapshot.Operation.ID != "op-1" || len(store.acks) != 1 {
		t.Fatalf("request = %#v, err=%v acks=%#v", snapshot, err, store.acks)
	}
	exported, dropped, unaccounted := uint64(3), uint64(2), uint64(0)
	ackTime := now.Add(time.Second)
	store.acks[0].AckState, store.acks[0].Ready, store.acks[0].AcknowledgedAt = icapturepolicy.AckDisabled, false, &ackTime
	store.acks[0].ExportedCount, store.acks[0].DroppedCount, store.acks[0].UnaccountedCount = &exported, &dropped, &unaccounted
	store.acks[0].TraceDisposition = icapturepolicy.DispositionComplete
	if _, err := controller.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.op.Phase != icapturepolicy.PhaseSucceeded || store.state.EffectiveState != icapturepolicy.StateDisabled {
		t.Fatalf("operation did not converge: op=%+v state=%+v", store.op, store.state)
	}
}

func TestControllerDeadlineStartsCompensationRevision(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	store := &fakeStore{state: icapturepolicy.ControlState{CurrentRevision: 1, DesiredState: icapturepolicy.StateEnabled, EffectiveState: icapturepolicy.StateEnabled, LastStableRevision: 1, CoverageGapUpdatedAt: now, UpdatedAt: now}}
	controller, err := New(Options{Store: store, WorkerID: "controller-1", Lease: time.Minute, Convergence: time.Minute, Targets: []Target{{EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway-1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1"}}, Now: func() time.Time { return now }, OperationID: func() (string, error) { return "op-2", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Request(context.Background(), capturepolicysvc.ChangeRequest{DesiredState: capturepolicysvc.StateDisabled, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	store.op.ConvergenceDeadline = now.Add(-time.Second)
	if _, err := controller.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.op.Phase != icapturepolicy.PhaseRollingBack || store.op.CompensationRevision != 3 || store.state.DesiredState != icapturepolicy.StateEnabled {
		t.Fatalf("compensation was not started: op=%+v state=%+v", store.op, store.state)
	}
}
