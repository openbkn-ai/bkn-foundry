// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package archivestore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestArchiveHistoryMariaDB(t *testing.T) {
	dsn := os.Getenv("BKN_LOG_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("dedicated disposable MariaDB DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "" || cfg.Net != "tcp" || cfg.Addr != "127.0.0.1:13343" {
		t.Fatal("dedicated local test endpoint without database required")
	}
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	name := fmt.Sprintf("archive_history_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec("DROP DATABASE `" + name + "`") }()
	cfg.DBName, cfg.ParseTime = name, true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// A local query fixture only; this test neither runs nor changes migrations.
	_, err = db.Exec(`CREATE TABLE bkn_trace_archive_jobs (
		archive_job_id VARCHAR(255) PRIMARY KEY, archive_kind VARCHAR(32), range_from DATETIME(6), range_to DATETIME(6),
		archive_status VARCHAR(32), candidate_ids LONGTEXT, candidate_payloads LONGTEXT, candidate_count INT,
		manifest_ref TEXT, error_message TEXT, created_at DATETIME(6), updated_at DATETIME(6), batch_progress LONGTEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	store := New(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 25; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		if err := store.Create(archivesvc.Job{ID: fmt.Sprintf("recent-%02d", i), Kind: observabilityvo.ArchiveKindLog, Range: observabilityvo.ArchiveRange{To: now}, Status: observabilityvo.ArchiveStatusCompleted, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		id   string
		kind observabilityvo.ArchiveKind
		at   time.Time
	}{
		{"old-pending", observabilityvo.ArchiveKindLog, now.Add(-time.Hour)},
		{"new-pending", observabilityvo.ArchiveKindLog, now.Add(time.Hour)},
		{"trace-pending", observabilityvo.ArchiveKindTrace, now.Add(-time.Hour)},
	} {
		if err := store.Create(archivesvc.Job{ID: tc.id, Kind: tc.kind, Range: observabilityvo.ArchiveRange{To: now}, Status: observabilityvo.ArchiveStatusCleanupIncomplete, CreatedAt: tc.at, UpdatedAt: tc.at}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := store.ListJobs(context.Background(), observabilityvo.ArchiveKindLog, 20)
	if err != nil || len(jobs) != 21 || jobs[0].ID != "new-pending" || jobs[20].ID != "old-pending" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
	seen := map[string]bool{}
	for _, job := range jobs {
		if seen[job.ID] || job.Kind != observabilityvo.ArchiveKindLog {
			t.Fatalf("duplicate or mixed job: %+v", job)
		}
		seen[job.ID] = true
	}
	recent := store.List(observabilityvo.ArchiveKindLog, 1)
	if len(recent) != 1 || recent[0].ID != "new-pending" {
		t.Fatalf("legacy bounded history=%+v", recent)
	}
	jobs, err = store.ListJobs(context.Background(), observabilityvo.ArchiveKindTrace, 20)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "trace-pending" {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}
