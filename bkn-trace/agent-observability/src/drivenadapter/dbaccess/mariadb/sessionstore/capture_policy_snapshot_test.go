// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturepolicysvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

func TestCapturePolicySnapshotUsesBootstrapOperationWhenNoOperationExists(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	store := sessionstore.New(db)
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT current_revision, desired_state, effective_state, last_stable_revision").WillReturnRows(sqlmock.NewRows([]string{
		"current_revision", "desired_state", "effective_state", "last_stable_revision", "active_operation_id", "last_operation_id", "coverage_gap", "coverage_gap_reason", "coverage_gap_started_at", "coverage_gap_updated_at", "updated_at",
	}).AddRow(uint64(1), "enabled", "enabled", uint64(1), nil, nil, false, nil, nil, now, now))

	snapshot, err := store.ReadCapturePolicySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != 1 || snapshot.DesiredState != capturepolicysvc.StateEnabled || snapshot.EffectiveState != capturepolicysvc.StateEnabled {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Operation.ID != "bootstrap" || snapshot.Operation.Phase != capturepolicysvc.PhaseSucceeded {
		t.Fatalf("unexpected bootstrap operation: %+v", snapshot.Operation)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
