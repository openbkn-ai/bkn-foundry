// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func TestOutboxLeaseBoundsPayloadBytesBeforeLoading(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectBegin()
	// The lengths are SQL metadata: no large test body needs allocating.
	rows := sqlmock.NewRows([]string{"id", "type", "aggregate", "version", "event_type", "event_id", "bytes", "attempts", "created"}).
		AddRow(1, "artifact", "a", 1, "artifact.persist_requested", "oversized", iprojectionoutbox.MaxPayloadBytes+1, 0, now).
		AddRow(2, "interaction", "b", 1, "interaction.completed", "bounded", iprojectionoutbox.MaxPayloadBytes, 0, now).
		AddRow(3, "artifact", "c", 1, "artifact.persist_requested", "next-batch", iprojectionoutbox.MaxPayloadBytes, 0, now)
	mock.ExpectQuery("SELECT outbox_id, aggregate_type, aggregate_id, aggregate_version, event_type, event_id,.*OCTET_LENGTH").WithArgs(100).WillReturnRows(rows)
	mock.ExpectQuery("SELECT outbox_id, payload FROM bkn_trace_projection_outbox WHERE outbox_id IN").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "payload"}).AddRow(2, []byte(`{"answer":"kept"}`)))
	for _, id := range []uint64{1, 2} {
		mock.ExpectExec("UPDATE bkn_trace_projection_outbox").WithArgs(sqlmock.AnyArg(), time.Minute.Microseconds(), id).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	items, err := New(db).Lease(context.Background(), 100, time.Minute)
	if err != nil || len(items) != 2 {
		t.Fatalf("lease: items=%d err=%v", len(items), err)
	}
	if !items[0].PayloadTooLarge || len(items[0].Payload) != 0 || items[1].PayloadTooLarge || string(items[1].Payload) != `{"answer":"kept"}` {
		t.Fatalf("wrong bounded payload decision: %+v", items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxDLQPreservesSQLPayloadWithoutLoadingIt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE bkn_trace_projection_outbox").WithArgs("payload_too_large", uint64(7), "lease").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO bkn_trace_dlq.*SELECT 'projection', event_id,.*FROM bkn_trace_projection_outbox").WithArgs("payload_too_large", "payload_too_large", uint64(7)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	err = New(db).MoveToDLQ(context.Background(), iprojectionoutbox.Item{ID: 7, EventID: "too-large", LeaseToken: "lease", PayloadTooLarge: true}, "payload_too_large")
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxLeaseDefersAnswerBodyUntilTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT outbox_id, aggregate_type").WithArgs(1).WillReturnRows(sqlmock.NewRows([]string{"id", "type", "aggregate", "version", "event_type", "event_id", "bytes", "attempts", "created"}).AddRow(8, "interaction", "i", 1, "artifact.persist_requested", "answer", iprojectionoutbox.MaxPayloadBytes, 0, time.Now()))
	// No payload SELECT is allowed before committing the claim.
	mock.ExpectExec("UPDATE bkn_trace_projection_outbox").WithArgs(sqlmock.AnyArg(), time.Minute.Microseconds(), uint64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	items, err := New(db).Lease(context.Background(), 1, time.Minute)
	if err != nil || len(items) != 1 {
		t.Fatalf("lease: %v %v", items, err)
	}
	if !items[0].PayloadDeferred || len(items[0].Payload) != 0 || items[0].LeaseToken == "" {
		t.Fatalf("answer body was not deferred: %+v", items[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxDeferredPayloadIsBoundedAndLeaseFenced(t *testing.T) {
	for _, name := range []string{"current", "lost", "oversized", "timeout"} {
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			query := mock.ExpectQuery("SELECT CASE WHEN OCTET_LENGTH.*status='processing' AND lease_token=.*locked_until>UTC_TIMESTAMP").WithArgs(iprojectionoutbox.MaxPayloadBytes, uint64(8), "current-token")
			switch name {
			case "current":
				query.WillReturnRows(sqlmock.NewRows([]string{"payload", "bytes"}).AddRow([]byte("answer"), 6))
			case "lost":
				query.WillReturnError(sql.ErrNoRows)
			case "oversized":
				query.WillReturnRows(sqlmock.NewRows([]string{"payload", "bytes"}).AddRow(nil, iprojectionoutbox.MaxPayloadBytes+1))
			case "timeout":
				query.WillReturnError(context.DeadlineExceeded)
			}
			payload, err := New(db).LoadPayload(context.Background(), iprojectionoutbox.Item{ID: 8, LeaseToken: "current-token"})
			switch name {
			case "current":
				if err != nil || string(payload) != "answer" {
					t.Fatalf("payload=%q err=%v", payload, err)
				}
			case "lost":
				if !errors.Is(err, iprojectionoutbox.ErrLeaseLost) {
					t.Fatal(err)
				}
			case "oversized":
				if !iprojectionoutbox.IsPermanent(err) || len(payload) != 0 {
					t.Fatalf("payload=%q err=%v", payload, err)
				}
			case "timeout":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
