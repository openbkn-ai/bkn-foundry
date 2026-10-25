// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func retrySQLRow(now time.Time, attempts int, token string) *sqlmock.Rows {
	return sqlmock.NewRows(strings.Split(integrityRetryColumns, ",")).AddRow("i", 1, "running", attempts, now.Add(-time.Minute), now, now, now.Add(time.Minute), token, "", "inspection_failed")
}
func TestSQLRetryFinishAtomicVerdictAndFencing(t *testing.T) {
	for _, mode := range []string{"success", "state-write-fails", "old-token", "expired", "failure", "source-changed"} {
		t.Run(mode, func(t *testing.T) {
			db, m, e := sqlmock.New()
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = db.Close() }()
			s := New(db)
			now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			claim := isessionstore.RecordIntegrityRetry{InteractionID: "i", SourceVersion: 1, Token: "lease"}
			stored := &sessionvo.StoredRecordIntegrity{SourceVersion: 1, Applicable: false}
			reason := ""
			m.ExpectBegin()
			version := 1
			if mode == "source-changed" {
				version = 2
			}
			m.ExpectQuery("SELECT record_integrity_version,execution_status,record_integrity_json.*FOR UPDATE").WithArgs("i").WillReturnRows(sqlmock.NewRows([]string{"version", "status", "verdict"}).AddRow(version, "completed", nil))
			token := "lease"
			if mode == "old-token" {
				token = "other"
			}
			m.ExpectQuery("SELECT .* FROM bkn_trace_record_integrity_retries.*FOR UPDATE").WithArgs("i", uint64(1)).WillReturnRows(retrySQLRow(now, 1, token))
			clock := now
			if mode == "expired" {
				clock = now.Add(2 * time.Minute)
			}
			m.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(clock))
			if mode == "expired" || mode == "old-token" {
				m.ExpectRollback()
			} else {
				if mode == "failure" {
					stored = nil
					reason = "inspection_failed"
				} else if mode != "source-changed" {
					m.ExpectExec("UPDATE bkn_trace_interactions SET record_integrity_json").WithArgs(`{"source_version":1,"applicable":false}`, "i", uint64(1), sessionvo.InteractionActive).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				q := m.ExpectExec("UPDATE bkn_trace_record_integrity_retries SET retry_status")
				if mode == "state-write-fails" {
					q.WillReturnError(errors.New("persist retry state"))
					m.ExpectRollback()
				} else {
					q.WillReturnResult(sqlmock.NewResult(0, 1))
					m.ExpectCommit()
				}
			}
			ok, err := s.FinishRecordIntegrityRetry(context.Background(), claim, stored, reason)
			want := mode == "success" || mode == "failure"
			if ok != want || (mode == "state-write-fails") != (err != nil) {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if e = m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestSQLRetryClaimExpiredLastAttemptPersistsExhaustion(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	s := New(db)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	m.ExpectBegin()
	m.ExpectQuery("SELECT record_integrity_version,execution_status,record_integrity_json.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"version", "status", "verdict"}).AddRow(1, "completed", nil))
	m.ExpectExec("UPDATE bkn_trace_record_integrity_retries SET retry_status='superseded'").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectQuery("SELECT .* FROM bkn_trace_record_integrity_retries.*FOR UPDATE").WillReturnRows(retrySQLRow(now.Add(-2*time.Minute), 8, "old"))
	m.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(now))
	m.ExpectExec("UPDATE bkn_trace_record_integrity_retries SET retry_status").WithArgs("exhausted", 8, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "", "attempts_exhausted", "lease_expired", "i", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	r, ok, e := s.ClaimRecordIntegrityRetry(context.Background(), "i", 1)
	if e != nil || ok || r.Status != "exhausted" || r.LastFailureReason != "lease_expired" {
		t.Fatalf("state=%+v ok=%v err=%v", r, ok, e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestSQLRetryFinishesLeasedAttemptAfterRetryDeadline(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			db, m, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			s := New(db)
			last := time.Date(2026, 10, 24, 0, 30, 0, 0, time.UTC)
			first := last.Add(-isessionstore.RecordIntegrityRetryDeadline + 10*time.Second)
			finished := last.Add(20 * time.Second)
			claim := isessionstore.RecordIntegrityRetry{InteractionID: "i", SourceVersion: 1, Token: "lease"}
			m.ExpectBegin()
			m.ExpectQuery("SELECT record_integrity_version,execution_status,record_integrity_json.*FOR UPDATE").WithArgs("i").WillReturnRows(sqlmock.NewRows([]string{"version", "status", "verdict"}).AddRow(1, "completed", nil))
			m.ExpectQuery("SELECT .* FROM bkn_trace_record_integrity_retries.*FOR UPDATE").WithArgs("i", uint64(1)).WillReturnRows(sqlmock.NewRows(strings.Split(integrityRetryColumns, ",")).AddRow("i", 1, "running", 2, first, last, last, last.Add(time.Minute), "lease", "inspection_failed", "inspection_failed"))
			m.ExpectQuery("SELECT UTC_TIMESTAMP").WillReturnRows(sqlmock.NewRows([]string{"now"}).AddRow(finished))
			var stored *sessionvo.StoredRecordIntegrity
			reason := "attempt_timeout"
			if success {
				stored = &sessionvo.StoredRecordIntegrity{SourceVersion: 1, Applicable: false}
				reason = ""
				m.ExpectExec("UPDATE bkn_trace_interactions SET record_integrity_json").WithArgs(`{"source_version":1,"applicable":false}`, "i", uint64(1), sessionvo.InteractionActive).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec("UPDATE bkn_trace_record_integrity_retries SET retry_status").WithArgs("succeeded", 2, first, last, last, last.Add(time.Minute), "", "", "inspection_failed", "i", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				m.ExpectExec("UPDATE bkn_trace_record_integrity_retries SET retry_status").WithArgs("exhausted", 2, first, last, finished.Add(time.Minute), last.Add(time.Minute), "", "retry_deadline_exceeded", "attempt_timeout", "i", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			}
			m.ExpectCommit()
			if ok, err := s.FinishRecordIntegrityRetry(context.Background(), claim, stored, reason); err != nil || !ok {
				t.Fatalf("finish valid lease: %v %v", ok, err)
			}
			if err = m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
