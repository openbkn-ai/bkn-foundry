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
	mock.ExpectQuery("SELECT policy_revision FROM bkn_trace_capture_operation_acknowledgements").WithArgs("op-7", icapturepolicy.EndpointTraceGateway, "gateway#missing").WillReturnRows(sqlmock.NewRows([]string{"policy_revision"}))
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
	mock.ExpectExec("INSERT INTO bkn_trace_capture_endpoint_leases").WithArgs(
		icapturepolicy.EndpointEvidencePublisher, "publisher#boot-1", "sa/publisher", "boot-1", uint64(7), true, now, now.Add(30*time.Second), now,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	if err := store.UpsertEndpointLease(context.Background(), icapturepolicy.EndpointLease{
		EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "publisher#boot-1", WorkloadIdentity: "sa/publisher", ProcessBootID: "boot-1", ObservedRevision: 7, Ready: true, HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
