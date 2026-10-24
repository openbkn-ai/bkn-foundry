// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivestore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestProgressPersistenceAndLegacyReads(t *testing.T) {
	for _, raw := range []string{"", `{"run_id":"run1","batch_number":2,"eligible_at_selection":2200,"verified_count":2000,"cleaned_count":1900,"cleaned_in_run":2100,"remaining_at_selection":200,"run_complete":false}`} {
		t.Run(raw, func(t *testing.T) {
			db, m, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			store := New(db)
			now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
			var want *archivesvc.BatchProgress
			var value driver.Value
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &want); err != nil {
					t.Fatal(err)
				}
				value = []byte(raw)
			}
			cols := []string{"archive_job_id", "range_from", "range_to", "archive_status", "candidate_ids", "candidate_count", "manifest_ref", "error_message", "created_at", "updated_at", "batch_progress"}
			row := func() *sqlmock.Rows {
				return sqlmock.NewRows(cols).AddRow("job", nil, now, "archived", []byte(`["a"]`), 1, "manifest", "", now, now, value)
			}
			m.ExpectQuery("SELECT archive_job_id.*LIMIT 1").WithArgs(observabilityvo.ArchiveKindLog).WillReturnRows(row())
			got, ok := store.Latest(observabilityvo.ArchiveKindLog)
			if !ok || !reflect.DeepEqual(got.BatchProgress, want) {
				t.Fatalf("latest=%+v %v", got, ok)
			}
			m.ExpectQuery("SELECT archive_job_id.*LIMIT ").WithArgs(observabilityvo.ArchiveKindLog, 2).WillReturnRows(row())
			jobs := store.List(observabilityvo.ArchiveKindLog, 2)
			if len(jobs) != 1 || !reflect.DeepEqual(jobs[0].BatchProgress, want) {
				t.Fatalf("list=%+v", jobs)
			}
			m.ExpectQuery("SELECT archive_kind").WithArgs("job").WillReturnRows(sqlmock.NewRows([]string{"kind", "from", "to", "status", "ids", "payloads", "count", "manifest", "message", "created", "updated", "progress"}).AddRow("log", nil, now, "archived", []byte(`["a"]`), []byte(`[]`), 1, "manifest", "", now, now, value))
			got, ok = store.Get("job")
			if !ok || !reflect.DeepEqual(got.BatchProgress, want) {
				t.Fatalf("get=%+v %v", got, ok)
			}
			got.BatchProgress = want
			m.ExpectExec("INSERT INTO bkn_trace_archive_jobs.*batch_progress").WithArgs("job", observabilityvo.ArchiveKindLog, nil, now, got.Status, []byte(`["a"]`), []byte(`[]`), 1, "manifest", nil, now, now, value).WillReturnResult(sqlmock.NewResult(1, 1))
			if err := store.Create(got); err != nil {
				t.Fatal(err)
			}
			m.ExpectExec("UPDATE bkn_trace_archive_jobs.*batch_progress").WithArgs(got.Status, []byte(`[]`), "manifest", nil, now, value, "job").WillReturnResult(sqlmock.NewResult(0, 1))
			if err := store.Update(got); err != nil {
				t.Fatal(err)
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMalformedProgressDoesNotReturnJob(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	now := time.Now()
	m.ExpectQuery("SELECT archive_job_id").WillReturnRows(sqlmock.NewRows([]string{"id", "from", "to", "status", "ids", "count", "manifest", "message", "created", "updated", "progress"}).AddRow("job", nil, now, "archived", []byte(`[]`), 0, "", "", now, now, []byte(`{broken`)))
	if _, ok := New(db).Latest(observabilityvo.ArchiveKindLog); ok {
		t.Fatal("corrupt metadata accepted")
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHasIncompleteCleanupPropagatesFailure(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	store := New(db)
	for _, want := range []bool{true, false} {
		m.ExpectQuery("SELECT EXISTS").WithArgs(observabilityvo.ArchiveKindLog, observabilityvo.ArchiveStatusCleanupIncomplete).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(want))
		got, err := store.HasIncompleteCleanup(context.Background(), observabilityvo.ArchiveKindLog)
		if err != nil || got != want {
			t.Fatalf("%v %v", got, err)
		}
	}
	failure := errors.New("db unavailable")
	m.ExpectQuery("SELECT EXISTS").WillReturnError(failure)
	if _, err := store.HasIncompleteCleanup(context.Background(), observabilityvo.ArchiveKindLog); !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestKindLockLifecycleAndErrorIdentity(t *testing.T) {
	for _, releaseFail := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "discard_failed_release"}[releaseFail], func(t *testing.T) {
			db, m, _ := sqlmock.New()
			defer func() { _ = db.Close() }()
			db.SetMaxOpenConns(3)
			name := archiveLockName("schema1", observabilityvo.ArchiveKindLog)
			m.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"db"}).AddRow("schema1"))
			m.ExpectQuery("SELECT GET_LOCK").WithArgs(name).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
			if releaseFail {
				m.ExpectQuery("SELECT RELEASE_LOCK").WithArgs(name).WillReturnError(errors.New("lost connection"))
				m.ExpectClose()
			} else {
				m.ExpectQuery("SELECT RELEASE_LOCK").WithArgs(name).WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("original callback error")
			err := New(db).WithKindLock(ctx, observabilityvo.ArchiveKindLog, func() error { cancel(); return failure })
			if err != failure {
				t.Fatalf("error identity changed: %v", err)
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKindLockBusyNullAndQueryFailuresNeverInvokeCallback(t *testing.T) {
	for _, mode := range []string{"busy", "null", "error"} {
		t.Run(mode, func(t *testing.T) {
			db, m, _ := sqlmock.New()
			defer func() { _ = db.Close() }()
			db.SetMaxOpenConns(3)
			m.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"db"}).AddRow("schema1"))
			q := m.ExpectQuery("SELECT GET_LOCK").WithArgs(archiveLockName("schema1", observabilityvo.ArchiveKindLog))
			switch mode {
			case "busy":
				q.WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(0))
			case "null":
				q.WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(nil))
				m.ExpectClose()
			case "error":
				q.WillReturnError(context.DeadlineExceeded)
				m.ExpectClose()
			}
			called := false
			err := New(db).WithKindLock(context.Background(), observabilityvo.ArchiveKindLog, func() error { called = true; return nil })
			if called || err == nil {
				t.Fatalf("called=%v error=%v", called, err)
			}
			if mode == "busy" && err != archivesvc.ErrArchiveBusy {
				t.Fatalf("busy=%v", err)
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestKindLockRejectsInsufficientConnectionsAndNamesAreIsolated(t *testing.T) {
	for _, limit := range []int{1, 2} {
		db, m, _ := sqlmock.New()
		db.SetMaxOpenConns(limit)
		if err := New(db).WithKindLock(context.Background(), observabilityvo.ArchiveKindLog, func() error { t.Fatal("callback invoked"); return nil }); err == nil {
			t.Fatalf("pool limit %d accepted", limit)
		}
		if err := m.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
	a := archiveLockName("schema1", observabilityvo.ArchiveKindLog)
	if len(a) > 64 || a == archiveLockName("schema2", observabilityvo.ArchiveKindLog) || a == archiveLockName("schema1", observabilityvo.ArchiveKindTrace) {
		t.Fatal("lock names not isolated")
	}
}

func TestSuccessfulCallbackRemainsSuccessfulWhenReleaseFails(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(3)
	m.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"db"}).AddRow("schema1"))
	m.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	m.ExpectQuery("SELECT RELEASE_LOCK").WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(0))
	m.ExpectClose()
	if err := New(db).WithKindLock(context.Background(), observabilityvo.ArchiveKindLog, func() error { return nil }); err != nil {
		t.Fatalf("successful callback became error: %v", err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProgressCompatibilityWithoutRunComplete(t *testing.T) {
	p, err := decodeProgress([]byte(`{"run_id":"old-run","batch_number":1}`))
	if err != nil || p == nil || p.RunID != "old-run" || p.BatchNumber != 1 || p.RunComplete {
		t.Fatalf("legacy progress: %+v %v", p, err)
	}
}

func TestKindLockReleasesOnPanic(t *testing.T) {
	db, m, _ := sqlmock.New()
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(3)
	m.ExpectQuery("SELECT DATABASE").WillReturnRows(sqlmock.NewRows([]string{"db"}).AddRow("schema1"))
	m.ExpectQuery("SELECT GET_LOCK").WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
	m.ExpectQuery("SELECT RELEASE_LOCK").WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(1))
	func() {
		defer func() {
			if recover() != "callback panic" {
				t.Error("panic identity changed")
			}
		}()
		_ = New(db).WithKindLock(context.Background(), observabilityvo.ArchiveKindLog, func() error { panic("callback panic") })
	}()
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
