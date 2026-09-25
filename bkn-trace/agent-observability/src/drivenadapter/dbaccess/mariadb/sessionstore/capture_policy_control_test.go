// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

func TestCapturePolicyControlReadsSingletonState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT current_revision, desired_state, effective_state, last_stable_revision").WillReturnRows(sqlmock.NewRows([]string{
		"current_revision", "desired_state", "effective_state", "last_stable_revision", "active_operation_id", "last_operation_id", "coverage_gap", "coverage_gap_reason", "coverage_gap_started_at", "coverage_gap_updated_at", "updated_at",
	}).AddRow(uint64(7), "disabled", "disabling", uint64(6), "op-7", "op-6", true, "ack_gap", nil, now, now))
	state, err := store.ReadControlState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentRevision != 7 || state.ActiveOperationID != "op-7" || !state.CoverageGap || state.CoverageGapReason != "ack_gap" {
		t.Fatalf("unexpected control state: %+v", state)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlRejectsAcknowledgementOutsideFrozenExpectedSet(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id FROM bkn_trace_capture_operation_acknowledgements").WithArgs("op-7", icapturepolicy.EndpointTraceGateway, "gateway#missing").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "workload_identity", "process_boot_id"}))
	mock.ExpectRollback()
	err = store.RecordAcknowledgement(context.Background(), icapturepolicy.ExpectedAcknowledgement{
		OperationID: "op-7", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#missing",
		WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 7, AckState: "gap",
	})
	if err != icapturepolicy.ErrExpectedSetConflict {
		t.Fatalf("RecordAcknowledgement() error = %v, want expected-set conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlUpsertsLiveEndpointLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, "publisher#boot-1").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_endpoint_leases").WithArgs(
		icapturepolicy.EndpointEvidencePublisher, "publisher#boot-1", "sa/publisher", "boot-1", uint64(7), true, now, now.Add(30*time.Second), now,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.UpsertEndpointLease(context.Background(), icapturepolicy.EndpointLease{
		EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "publisher#boot-1", WorkloadIdentity: "sa/publisher", ProcessBootID: "boot-1", ObservedRevision: 7, Ready: true, HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlRejectsLeaseIdentityTakeover(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointTraceGateway, "gateway#1").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}).AddRow("sa/old", "boot-old"))
	mock.ExpectRollback()
	err = store.UpsertEndpointLease(context.Background(), icapturepolicy.EndpointLease{EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/new", ProcessBootID: "boot-new", ObservedRevision: 8, Ready: true, HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now})
	if err != icapturepolicy.ErrExpectedSetConflict {
		t.Fatalf("UpsertEndpointLease() error = %v, want identity conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlRejectsAckFromDifferentBoot(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id FROM bkn_trace_capture_operation_acknowledgements").WithArgs("op-7", icapturepolicy.EndpointTraceGateway, "gateway#1").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "workload_identity", "process_boot_id"}).AddRow(uint64(7), "sa/gateway", "boot-old"))
	mock.ExpectRollback()
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	exported, dropped, unaccounted := uint64(10), uint64(1), uint64(0)
	err = store.RecordAcknowledgement(context.Background(), icapturepolicy.ExpectedAcknowledgement{OperationID: "op-7", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-new", PolicyRevision: 7, AckState: icapturepolicy.AckDisabled, AcknowledgedAt: &now, ExportedCount: &exported, DroppedCount: &dropped, UnaccountedCount: &unaccounted, TraceDisposition: icapturepolicy.DispositionComplete})
	if err != icapturepolicy.ErrExpectedSetConflict {
		t.Fatalf("RecordAcknowledgement() error = %v, want boot conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlCompletesSucceededAndStabilizesRequestedState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, expected_revision, requested_state, phase, lease_token").WithArgs("op-8").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "expected_revision", "requested_state", "phase", "lease_token", "lease_expires_at", "compensation_revision", "restored_state"}).AddRow(uint64(8), uint64(7), icapturepolicy.StateDisabled, icapturepolicy.PhaseDisabling, uint64(3), now.Add(time.Minute), nil, nil))
	mock.ExpectExec("UPDATE bkn_trace_capture_operations").WithArgs(icapturepolicy.PhaseSucceeded, now, now, "op-8", uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_operation_events").WithArgs("op-8", icapturepolicy.PhaseSucceeded, icapturepolicy.EventPhaseChanged, uint64(3), nil, nil, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE bkn_trace_capture_control_state").WithArgs("op-8", icapturepolicy.StateDisabled, uint64(8), now, "op-8").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.CompleteSucceeded(context.Background(), "op-8", 3, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlCompletesRollbackToExplicitCompensationState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, expected_revision, requested_state, phase, lease_token").WithArgs("op-9").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "expected_revision", "requested_state", "phase", "lease_token", "lease_expires_at", "compensation_revision", "restored_state"}).AddRow(uint64(9), uint64(8), icapturepolicy.StateDisabled, icapturepolicy.PhaseRollingBack, uint64(4), now.Add(time.Minute), uint64(10), icapturepolicy.StateEnabled))
	mock.ExpectExec("UPDATE bkn_trace_capture_operations").WithArgs(icapturepolicy.PhaseRollbackCompleted, now, now, "op-9", uint64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_operation_events").WithArgs("op-9", icapturepolicy.PhaseRollbackCompleted, icapturepolicy.EventPhaseChanged, uint64(4), nil, nil, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE bkn_trace_capture_control_state").WithArgs(uint64(10), icapturepolicy.StateEnabled, icapturepolicy.StateEnabled, uint64(10), "op-9", now, "op-9").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.CompleteRollback(context.Background(), "op-9", 4, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
