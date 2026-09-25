// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
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

func TestCapturePolicyControlInitializesSingletonAndRevisionIdempotently(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)

	// A clean install creates the singleton and immutable revision 1 in one
	// transaction. The second bootstrap sees the row and must not rewrite it.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_control_state").WithArgs(uint8(1), uint64(1), icapturepolicy.StateEnabled, icapturepolicy.StateEnabled, uint64(1), now, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_policy_revisions").WithArgs(uint64(1), true, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.EnsureControlState(context.Background(), true, now); err != nil {
		t.Fatal(err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(uint64(1)))
	mock.ExpectCommit()
	if err := store.EnsureControlState(context.Background(), false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlUsesHistoricalMaxAfterFailedOperation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	state := icapturepolicy.ControlState{
		CurrentRevision:      7,
		DesiredState:         icapturepolicy.StateEnabled,
		EffectiveState:       icapturepolicy.StateEnabled,
		LastStableRevision:   7,
		CoverageGapUpdatedAt: now,
		UpdatedAt:            now,
	}
	start := func(operationID string, revision uint64) {
		t.Helper()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT current_revision, active_operation_id").WillReturnRows(sqlmock.NewRows([]string{"current_revision", "active_operation_id"}).AddRow(uint64(7), nil))
		mock.ExpectQuery("SELECT revision FROM bkn_trace_capture_policy_revisions").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(revision - 1))
		mock.ExpectExec("INSERT INTO bkn_trace_capture_policy_revisions").WithArgs(revision, false, now).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO bkn_trace_capture_operations").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE bkn_trace_capture_control_state").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO bkn_trace_capture_operation_events").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		operation := icapturepolicy.Operation{
			ID: operationID, PolicyRevision: revision, RequestedState: icapturepolicy.StateDisabled,
			ExpectedRevision: 7, Phase: icapturepolicy.PhasePending, LeaseToken: 1,
			ConvergenceDeadline: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
		}
		if err := store.StartOperation(context.Background(), state, operation, nil); err != nil {
			t.Fatal(err)
		}
	}

	// The first operation is later failed, so its policy revision remains in
	// history while control.current_revision is restored to 7. The next
	// operation must therefore use 9, not current_revision+1 (which is 8).
	start("op-8", 8)
	start("op-9", 9)
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
	mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id FROM bkn_trace_capture_operation_acknowledgements").WithArgs("op-7", uint64(7), icapturepolicy.EndpointTraceGateway, "gateway#missing", "sa/gateway", "boot-1").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "workload_identity", "process_boot_id"}))
	mock.ExpectRollback()
	err = store.RecordAcknowledgement(context.Background(), icapturepolicy.ExpectedAcknowledgement{
		OperationID: "op-7", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#missing",
		WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 7, AckState: icapturepolicy.AckPending,
	})
	if err != icapturepolicy.ErrExpectedSetConflict {
		t.Fatalf("RecordAcknowledgement() error = %v, want expected-set conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlRetriesAfterTransientDeadlock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	exported, dropped, unaccounted := uint64(10), uint64(1), uint64(0)
	acknowledgement := icapturepolicy.ExpectedAcknowledgement{OperationID: "op-retry", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 7, AckState: icapturepolicy.AckDisabled, AcknowledgedAt: &now, ExportedCount: &exported, DroppedCount: &dropped, UnaccountedCount: &unaccounted, TraceDisposition: icapturepolicy.DispositionComplete}
	for attempt := 0; attempt < 2; attempt++ {
		mock.ExpectBegin()
		query := mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id").WithArgs("op-retry", uint64(7), icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1")
		if attempt == 0 {
			query.WillReturnError(&mysql.MySQLError{Number: 1213, Message: "deadlock"})
			mock.ExpectRollback()
			continue
		}
		query.WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "workload_identity", "process_boot_id"}).AddRow(uint64(7), "sa/gateway", "boot-1"))
		mock.ExpectExec("UPDATE bkn_trace_capture_operation_acknowledgements").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
	}
	if err := store.RecordAcknowledgement(context.Background(), acknowledgement); err != nil {
		t.Fatalf("RecordAcknowledgement() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlExhaustsTransientTransactionRetries(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	exported, dropped, unaccounted := uint64(10), uint64(1), uint64(0)
	acknowledgement := icapturepolicy.ExpectedAcknowledgement{OperationID: "op-exhausted", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 7, AckState: icapturepolicy.AckDisabled, AcknowledgedAt: &now, ExportedCount: &exported, DroppedCount: &dropped, UnaccountedCount: &unaccounted, TraceDisposition: icapturepolicy.DispositionComplete}
	for attempt := 0; attempt < 4; attempt++ {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id").WithArgs("op-exhausted", uint64(7), icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1").WillReturnError(&mysql.MySQLError{Number: 1205, Message: "lock wait timeout"})
		mock.ExpectRollback()
	}
	err = store.RecordAcknowledgement(context.Background(), acknowledgement)
	if err == nil || !strings.Contains(err.Error(), "transaction retry budget exhausted") {
		t.Fatalf("RecordAcknowledgement() error = %v, want exhausted retry error", err)
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
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, "publisher#boot-1", "boot-1").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}))
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
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointTraceGateway, "gateway#1", "boot-new").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}).AddRow("sa/old", "boot-new"))
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
	mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id FROM bkn_trace_capture_operation_acknowledgements").WithArgs("op-7", uint64(7), icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-new").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "workload_identity", "process_boot_id"}).AddRow(uint64(7), "sa/gateway", "boot-old"))
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

func TestCapturePolicyControlAcknowledgesCompensationRevisionAlongsideOriginal(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	exported, dropped, unaccounted := uint64(3), uint64(0), uint64(0)
	mock.ExpectBegin()
	// Revision 8 is the original operation and revision 9 is its
	// compensation; both rows may coexist for the same endpoint instance.
	mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id FROM bkn_trace_capture_operation_acknowledgements").WithArgs("op-8", uint64(9), icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "workload_identity", "process_boot_id"}).AddRow(uint64(9), "sa/gateway", "boot-1"))
	mock.ExpectExec("UPDATE bkn_trace_capture_operation_acknowledgements").WithArgs(
		false, icapturepolicy.AckDisabled, now, exported, dropped, unaccounted, icapturepolicy.DispositionComplete,
		nil, nil, nil, nil, nil, "op-8", icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1", uint64(9),
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.RecordAcknowledgement(context.Background(), icapturepolicy.ExpectedAcknowledgement{
		OperationID: "op-8", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1",
		WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 9, AckState: icapturepolicy.AckDisabled,
		AcknowledgedAt: &now, ExportedCount: &exported, DroppedCount: &dropped, UnaccountedCount: &unaccounted,
		TraceDisposition: icapturepolicy.DispositionComplete,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlAcceptsIdenticalAcknowledgementRowsZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	exported, dropped, unaccounted := uint64(10), uint64(1), uint64(0)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, workload_identity, process_boot_id").WithArgs("op-replay", uint64(9), icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "workload_identity", "process_boot_id"}).AddRow(uint64(9), "sa/gateway", "boot-1"))
	mock.ExpectExec("UPDATE bkn_trace_capture_operation_acknowledgements").WithArgs(
		false, icapturepolicy.AckDisabled, now, exported, dropped, unaccounted, icapturepolicy.DispositionComplete,
		nil, nil, nil, nil, nil, "op-replay", icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1", uint64(9),
	).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	if err := store.RecordAcknowledgement(context.Background(), icapturepolicy.ExpectedAcknowledgement{
		OperationID: "op-replay", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1",
		WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 9, AckState: icapturepolicy.AckDisabled,
		AcknowledgedAt: &now, ExportedCount: &exported, DroppedCount: &dropped, UnaccountedCount: &unaccounted,
		TraceDisposition: icapturepolicy.DispositionComplete,
	}); err != nil {
		t.Fatalf("RecordAcknowledgement() identical replay error = %v", err)
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

func TestCapturePolicyControlBeginsRollbackWithNewRevisionAndExpectedSet(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, phase, lease_token, lease_expires_at, compensation_revision, restored_state").WithArgs("op-10").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "phase", "lease_token", "lease_expires_at", "compensation_revision", "restored_state"}).AddRow(uint64(9), icapturepolicy.PhaseDisabling, uint64(4), now.Add(time.Minute), nil, nil))
	mock.ExpectQuery(`SELECT current_revision, desired_state, effective_state, COALESCE\(active_operation_id`).WillReturnRows(sqlmock.NewRows([]string{"current_revision", "desired_state", "effective_state", "active_operation_id"}).AddRow(uint64(9), icapturepolicy.StateDisabled, icapturepolicy.StateEnabled, "op-10"))
	mock.ExpectQuery("SELECT revision FROM bkn_trace_capture_policy_revisions").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(uint64(9)))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_policy_revisions").WithArgs(uint64(10), true, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_operation_acknowledgements").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE bkn_trace_capture_operations").WithArgs(icapturepolicy.PhaseRollingBack, uint64(10), icapturepolicy.StateEnabled, now.Add(10*time.Minute), now, "op-10", uint64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE bkn_trace_capture_control_state").WithArgs(uint64(10), icapturepolicy.StateEnabled, now, uint64(9), "op-10").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_operation_events").WithArgs("op-10", icapturepolicy.PhaseRollingBack, icapturepolicy.EventRollbackStarted, uint64(4), nil, nil, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.BeginRollback(context.Background(), "op-10", 4, 10, icapturepolicy.StateEnabled, []icapturepolicy.ExpectedAcknowledgement{{OperationID: "op-10", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 10, AckState: icapturepolicy.AckPending}}, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlRejectsRollingBackRetryWithDifferentExpectedSet(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, phase, lease_token, lease_expires_at, compensation_revision, restored_state").WithArgs("op-11").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "phase", "lease_token", "lease_expires_at", "compensation_revision", "restored_state"}).AddRow(uint64(9), icapturepolicy.PhaseRollingBack, uint64(4), now.Add(time.Minute), uint64(10), icapturepolicy.StateEnabled))
	mock.ExpectQuery(`SELECT current_revision, desired_state, effective_state, COALESCE\(active_operation_id`).WillReturnRows(sqlmock.NewRows([]string{"current_revision", "desired_state", "effective_state", "active_operation_id"}).AddRow(uint64(10), icapturepolicy.StateEnabled, icapturepolicy.StateEnabled, "op-11"))
	mock.ExpectQuery("SELECT endpoint_kind, instance_id, workload_identity, process_boot_id").WithArgs("op-11", uint64(10)).WillReturnRows(sqlmock.NewRows([]string{"endpoint_kind", "instance_id", "workload_identity", "process_boot_id"}).AddRow(icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1"))
	mock.ExpectRollback()
	err = store.BeginRollback(context.Background(), "op-11", 4, 10, icapturepolicy.StateEnabled, []icapturepolicy.ExpectedAcknowledgement{{OperationID: "op-11", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#2", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-2", PolicyRevision: 10, AckState: icapturepolicy.AckPending}}, now)
	if err != icapturepolicy.ErrExpectedSetConflict {
		t.Fatalf("BeginRollback() error = %v, want expected-set conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlAllowsRollingBackRetryAfterAcknowledgementProgress(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, phase, lease_token, lease_expires_at, compensation_revision, restored_state").WithArgs("op-13").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "phase", "lease_token", "lease_expires_at", "compensation_revision", "restored_state"}).AddRow(uint64(9), icapturepolicy.PhaseRollingBack, uint64(4), now.Add(time.Minute), uint64(10), icapturepolicy.StateEnabled))
	mock.ExpectQuery(`SELECT current_revision, desired_state, effective_state, COALESCE\(active_operation_id`).WillReturnRows(sqlmock.NewRows([]string{"current_revision", "desired_state", "effective_state", "active_operation_id"}).AddRow(uint64(10), icapturepolicy.StateEnabled, icapturepolicy.StateEnabled, "op-13"))
	// The ACK row has already advanced beyond pending in storage. The retry
	// must compare membership only and therefore still be idempotent.
	mock.ExpectQuery("SELECT endpoint_kind, instance_id, workload_identity, process_boot_id").WithArgs("op-13", uint64(10)).WillReturnRows(sqlmock.NewRows([]string{"endpoint_kind", "instance_id", "workload_identity", "process_boot_id"}).AddRow(icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1"))
	mock.ExpectCommit()
	if err := store.BeginRollback(context.Background(), "op-13", 4, 10, icapturepolicy.StateEnabled, []icapturepolicy.ExpectedAcknowledgement{{OperationID: "op-13", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 10, AckState: icapturepolicy.AckPending}}, now); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlRejectsDuplicateRollingBackRetryIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, phase, lease_token, lease_expires_at, compensation_revision, restored_state").WithArgs("op-14").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "phase", "lease_token", "lease_expires_at", "compensation_revision", "restored_state"}).AddRow(uint64(9), icapturepolicy.PhaseRollingBack, uint64(4), now.Add(time.Minute), uint64(10), icapturepolicy.StateEnabled))
	mock.ExpectQuery(`SELECT current_revision, desired_state, effective_state, COALESCE\(active_operation_id`).WillReturnRows(sqlmock.NewRows([]string{"current_revision", "desired_state", "effective_state", "active_operation_id"}).AddRow(uint64(10), icapturepolicy.StateEnabled, icapturepolicy.StateEnabled, "op-14"))
	mock.ExpectQuery("SELECT endpoint_kind, instance_id, workload_identity, process_boot_id").WithArgs("op-14", uint64(10)).WillReturnRows(sqlmock.NewRows([]string{"endpoint_kind", "instance_id", "workload_identity", "process_boot_id"}).AddRow(icapturepolicy.EndpointTraceGateway, "gateway#1", "sa/gateway", "boot-1").AddRow(icapturepolicy.EndpointEvidencePublisher, "publisher#1", "sa/publisher", "boot-1"))
	mock.ExpectRollback()
	duplicate := icapturepolicy.ExpectedAcknowledgement{OperationID: "op-14", EndpointKind: icapturepolicy.EndpointTraceGateway, InstanceID: "gateway#1", WorkloadIdentity: "sa/gateway", ProcessBootID: "boot-1", PolicyRevision: 10, AckState: icapturepolicy.AckPending}
	if err := store.BeginRollback(context.Background(), "op-14", 4, 10, icapturepolicy.StateEnabled, []icapturepolicy.ExpectedAcknowledgement{duplicate, duplicate}, now); err != icapturepolicy.ErrExpectedSetConflict {
		t.Fatalf("BeginRollback() error = %v, want expected-set conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyControlRejectsTerminalOperationCASMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT policy_revision, expected_revision, requested_state, phase, lease_token").WithArgs("op-12").WillReturnRows(sqlmock.NewRows([]string{"policy_revision", "expected_revision", "requested_state", "phase", "lease_token", "lease_expires_at", "compensation_revision", "restored_state"}).AddRow(uint64(8), uint64(7), icapturepolicy.StateDisabled, icapturepolicy.PhaseDisabling, uint64(3), now.Add(time.Minute), nil, nil))
	mock.ExpectExec("UPDATE bkn_trace_capture_operations").WithArgs(icapturepolicy.PhaseSucceeded, now, now, "op-12", uint64(3)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err := store.CompleteSucceeded(context.Background(), "op-12", 3, now); err != icapturepolicy.ErrRevisionConflict {
		t.Fatalf("CompleteSucceeded() error = %v, want revision conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
