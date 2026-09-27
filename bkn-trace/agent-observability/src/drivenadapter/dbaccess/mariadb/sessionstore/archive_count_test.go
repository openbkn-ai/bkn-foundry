// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package sessionstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

func TestTraceArchiveCountUsesTheFreezeEligibilityPredicateWithoutReadingPayloads(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	source := sessionstore.NewTraceArchiveSource(sessionstore.New(db))
	cutoff := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	archiveRange := observabilityvo.ArchiveRange{To: cutoff}
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM bkn_trace_interactions WHERE execution_status<>\? AND terminal_at<\?`).
		WithArgs(sessionvo.InteractionActive, cutoff).
		WillReturnRows(sqlmock.NewRows([]string{"candidate_count"}).AddRow(184))

	count, err := source.Count(context.Background(), observabilityvo.ArchiveKindTrace, archiveRange)
	if err != nil || count != 184 {
		t.Fatalf("count = %d, err = %v", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestTraceArchiveCountPropagatesDatabaseFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	source := sessionstore.NewTraceArchiveSource(sessionstore.New(db))
	cutoff := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	queryErr := errors.New("database unavailable")
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM bkn_trace_interactions WHERE execution_status<>\? AND terminal_at<\?`).
		WithArgs(sessionvo.InteractionActive, cutoff).
		WillReturnError(queryErr)

	_, err = source.Count(context.Background(), observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: cutoff})
	if !errors.Is(err, queryErr) {
		t.Fatalf("count error = %v, want %v", err, queryErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
