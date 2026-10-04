package sessionstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

func TestClaimCapturePolicyOperationReturnsVerifiedStateWhenIdle(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT active_operation_id, current_revision, desired_state, effective_state").
		WillReturnRows(sqlmock.NewRows([]string{"active_operation_id", "current_revision", "desired_state", "effective_state"}).
			AddRow(nil, uint64(5), "enabled", "enabled"))
	mock.ExpectCommit()

	op, claimed, err := store.ClaimCapturePolicyOperation(context.Background(), "worker", time.Second, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("idle control state must not claim an operation")
	}
	if op.CurrentRevision != 5 || op.DesiredState != "enabled" || op.EffectiveState != "enabled" {
		t.Fatalf("verified idle control state lost: %+v", op)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimCapturePolicyOperationReturnsVerifiedStateWhenHeldByOtherWorker(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC)
	leaseExpires := now.Add(time.Minute)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT active_operation_id, current_revision, desired_state, effective_state").
		WillReturnRows(sqlmock.NewRows([]string{"active_operation_id", "current_revision", "desired_state", "effective_state"}).
			AddRow("op-held", uint64(7), "disabled", "disabled"))
	mock.ExpectQuery("SELECT policy_revision, requested_state, expected_revision, phase, error_code").
		WillReturnRows(sqlmock.NewRows([]string{
			"policy_revision", "requested_state", "expected_revision", "phase", "error_code",
			"lease_owner", "lease_token", "lease_expires_at", "convergence_deadline",
			"created_at", "updated_at", "terminal_at", "compensation_revision", "restored_state",
		}).AddRow(uint64(7), "disabled", uint64(6), "disabling", nil, "other-worker", uint64(2),
			leaseExpires, now.Add(time.Hour), now, now, nil, nil, nil))
	mock.ExpectCommit()

	op, claimed, err := store.ClaimCapturePolicyOperation(context.Background(), "worker", time.Second, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("operation held by another worker must not be claimed")
	}
	if op.CurrentRevision != 7 || op.DesiredState != "disabled" || op.EffectiveState != "disabled" {
		t.Fatalf("verified held-operation control state lost: %+v", op)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
