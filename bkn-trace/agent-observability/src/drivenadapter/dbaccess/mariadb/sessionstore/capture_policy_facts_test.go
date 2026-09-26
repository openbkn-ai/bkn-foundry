// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/icapturepolicy"
)

type evidencePublisherHeartbeatWriter interface {
	RegisterEvidencePublisherHeartbeat(context.Context, icapturepolicy.EndpointLease) error
}

func TestCapturePolicyFactsRegistersCurrentEvidencePublisherHeartbeatAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	writer, ok := any(store).(evidencePublisherHeartbeatWriter)
	if !ok {
		t.Fatal("session store does not implement atomic evidence publisher heartbeat registration")
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	lease := icapturepolicy.EndpointLease{
		EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "bkn-backend#boot-1",
		WorkloadIdentity: "bkn-backend", ProcessBootID: "boot-1", ObservedRevision: 7, Ready: true,
		HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(uint64(7)))
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, "bkn-backend#boot-1", "boot-1").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, "bkn-backend#boot-1", "bkn-backend", "boot-1", uint64(7), true, now, now.Add(30*time.Second), now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT process_boot_id, registration_state, revoked_at FROM bkn_trace_producer_instance_registrations").WithArgs("bkn-backend#boot-1", uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"process_boot_id", "registration_state", "revoked_at"}))
	mock.ExpectExec("INSERT INTO bkn_trace_producer_instance_registrations").WithArgs("bkn-backend#boot-1", uint64(7), "boot-1", "registered", now, nil).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := writer.RegisterEvidencePublisherHeartbeat(context.Background(), lease); err != nil {
		t.Fatalf("RegisterEvidencePublisherHeartbeat() error = %v", err)
	}
	// A repeated heartbeat for the same boot and policy revision refreshes the
	// lease and keeps the existing registration rather than conflicting.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(uint64(7)))
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, "bkn-backend#boot-1", "boot-1").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}).AddRow("bkn-backend", "boot-1"))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, "bkn-backend#boot-1", "bkn-backend", "boot-1", uint64(7), true, now, now.Add(30*time.Second), now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT process_boot_id, registration_state, revoked_at FROM bkn_trace_producer_instance_registrations").WithArgs("bkn-backend#boot-1", uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"process_boot_id", "registration_state", "revoked_at"}).AddRow("boot-1", "registered", nil))
	mock.ExpectCommit()
	if err := writer.RegisterEvidencePublisherHeartbeat(context.Background(), lease); err != nil {
		t.Fatalf("idempotent RegisterEvidencePublisherHeartbeat() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRejectsEvidencePublisherHeartbeatForStaleRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	writer, ok := any(store).(evidencePublisherHeartbeatWriter)
	if !ok {
		t.Fatal("session store does not implement atomic evidence publisher heartbeat registration")
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	lease := icapturepolicy.EndpointLease{
		EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "bkn-backend#old-boot",
		WorkloadIdentity: "bkn-backend", ProcessBootID: "old-boot", ObservedRevision: 7, Ready: true,
		HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(uint64(8)))
	mock.ExpectRollback()
	if err := writer.RegisterEvidencePublisherHeartbeat(context.Background(), lease); err == nil {
		t.Fatal("RegisterEvidencePublisherHeartbeat() accepted a stale policy revision")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRefreshesLeaseWithoutRegisteringWhenAdmissionDisabled(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	writer, ok := any(store).(evidencePublisherHeartbeatWriter)
	if !ok {
		t.Fatal("session store does not implement atomic evidence publisher heartbeat registration")
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	lease := icapturepolicy.EndpointLease{
		EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "bkn-backend#boot-1",
		WorkloadIdentity: "bkn-backend", ProcessBootID: "boot-1", ObservedRevision: 7, Ready: true,
		HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(uint64(7)))
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(false))
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, lease.InstanceID, "boot-1").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, lease.InstanceID, "bkn-backend", "boot-1", uint64(7), true, now, now.Add(30*time.Second), now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := writer.RegisterEvidencePublisherHeartbeat(context.Background(), lease); err != nil {
		t.Fatalf("RegisterEvidencePublisherHeartbeat() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsAllowsConcurrentPublisherBootsAtSameRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	writer, ok := any(store).(evidencePublisherHeartbeatWriter)
	if !ok {
		t.Fatal("session store does not implement atomic evidence publisher heartbeat registration")
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	for _, bootID := range []string{"boot-1", "boot-2"} {
		lease := icapturepolicy.EndpointLease{
			EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "bkn-backend#" + bootID,
			WorkloadIdentity: "bkn-backend", ProcessBootID: bootID, ObservedRevision: 7, Ready: true,
			HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
		}
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(uint64(7)))
		mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
		mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, lease.InstanceID, bootID).WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}))
		mock.ExpectExec("INSERT INTO bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, lease.InstanceID, "bkn-backend", bootID, uint64(7), true, now, now.Add(30*time.Second), now).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT process_boot_id, registration_state, revoked_at FROM bkn_trace_producer_instance_registrations").WithArgs(lease.InstanceID, uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"process_boot_id", "registration_state", "revoked_at"}))
		mock.ExpectExec("INSERT INTO bkn_trace_producer_instance_registrations").WithArgs(lease.InstanceID, uint64(7), bootID, "registered", now, nil).WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		if err := writer.RegisterEvidencePublisherHeartbeat(context.Background(), lease); err != nil {
			t.Fatalf("RegisterEvidencePublisherHeartbeat(%s) error = %v", bootID, err)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRollsBackLeaseRefreshWhenRegistrationIsRevoked(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	writer, ok := any(store).(evidencePublisherHeartbeatWriter)
	if !ok {
		t.Fatal("session store does not implement atomic evidence publisher heartbeat registration")
	}
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	lease := icapturepolicy.EndpointLease{
		EndpointKind: icapturepolicy.EndpointEvidencePublisher, InstanceID: "bkn-backend#boot-1",
		WorkloadIdentity: "bkn-backend", ProcessBootID: "boot-1", ObservedRevision: 7, Ready: true,
		HeartbeatAt: now, LeaseExpiresAt: now.Add(30 * time.Second), UpdatedAt: now,
	}
	revokedAt := now.Add(-time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT current_revision FROM bkn_trace_capture_control_state").WillReturnRows(sqlmock.NewRows([]string{"current_revision"}).AddRow(uint64(7)))
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
	mock.ExpectQuery("SELECT workload_identity, process_boot_id FROM bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, lease.InstanceID, "boot-1").WillReturnRows(sqlmock.NewRows([]string{"workload_identity", "process_boot_id"}).AddRow("bkn-backend", "boot-1"))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_endpoint_leases").WithArgs(icapturepolicy.EndpointEvidencePublisher, lease.InstanceID, "bkn-backend", "boot-1", uint64(7), true, now, now.Add(30*time.Second), now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT process_boot_id, registration_state, revoked_at FROM bkn_trace_producer_instance_registrations").WithArgs(lease.InstanceID, uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"process_boot_id", "registration_state", "revoked_at"}).AddRow("boot-1", "revoked", revokedAt))
	mock.ExpectRollback()
	if err := writer.RegisterEvidencePublisherHeartbeat(context.Background(), lease); err == nil {
		t.Fatal("RegisterEvidencePublisherHeartbeat() accepted a revoked registration")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsPersistPolicyRevisionIdempotently(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
	mock.ExpectCommit()
	if err := store.PersistPolicyRevision(context.Background(), icapturepolicy.PolicyRevision{Revision: 7, AdmissionEnabled: true, RecordedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRetriesTransientPolicyRevisionRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnError(&mysql.MySQLError{Number: 1213, Message: "deadlock"})
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(true))
	mock.ExpectCommit()
	if err := store.PersistPolicyRevision(context.Background(), icapturepolicy.PolicyRevision{Revision: 7, AdmissionEnabled: true, RecordedAt: now}); err != nil {
		t.Fatalf("PersistPolicyRevision() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRejectPolicyRevisionModeConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}).AddRow(false))
	mock.ExpectRollback()
	if err := store.PersistPolicyRevision(context.Background(), icapturepolicy.PolicyRevision{Revision: 7, AdmissionEnabled: true, RecordedAt: now}); err == nil {
		t.Fatal("PersistPolicyRevision() accepted a conflicting immutable revision")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsAppendsOnlyTheNextPolicyRevision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT admission_enabled FROM bkn_trace_capture_policy_revisions").WithArgs(uint64(8)).WillReturnRows(sqlmock.NewRows([]string{"admission_enabled"}))
	mock.ExpectQuery("SELECT revision FROM bkn_trace_capture_policy_revisions").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(uint64(7)))
	mock.ExpectExec("INSERT INTO bkn_trace_capture_policy_revisions").WithArgs(uint64(8), false, now).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.PersistPolicyRevision(context.Background(), icapturepolicy.PolicyRevision{Revision: 8, AdmissionEnabled: false, RecordedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePolicyFactsRegisterProducerAndClosure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT process_boot_id, registration_state, revoked_at FROM bkn_trace_producer_instance_registrations").WithArgs("backend#boot-1", uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"process_boot_id", "registration_state", "revoked_at"}))
	mock.ExpectExec("INSERT INTO bkn_trace_producer_instance_registrations").WithArgs("backend#boot-1", uint64(7), "boot-1", "registered", now, nil).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.RegisterProducer(context.Background(), icapturepolicy.ProducerRegistration{InstanceID: "backend#boot-1", PolicyRevision: 7, ProcessBootID: "boot-1", RegistrationState: "registered", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}

	closed := now.Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT last_accepted_sequence, closed_at, acknowledged_at FROM bkn_trace_producer_closure_watermarks").WithArgs("backend#boot-1", uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"last_accepted_sequence", "closed_at", "acknowledged_at"}))
	mock.ExpectExec("INSERT INTO bkn_trace_producer_closure_watermarks").WithArgs("backend#boot-1", uint64(7), uint64(12), closed, closed.Add(time.Second)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := store.PersistClosureWatermark(context.Background(), icapturepolicy.ClosureWatermark{InstanceID: "backend#boot-1", PolicyRevision: 7, LastAcceptedSequence: 12, ClosedAt: closed, AcknowledgedAt: closed.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
