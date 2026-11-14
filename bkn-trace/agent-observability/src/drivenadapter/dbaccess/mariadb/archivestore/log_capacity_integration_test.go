// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package archivestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

func TestLogArchiveCapacityMariaDB(t *testing.T) {
	dsn := os.Getenv("BKN_LOG_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("dedicated disposable MariaDB DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "" || cfg.Net != "tcp" || (cfg.Addr != "127.0.0.1:13343" && !(os.Getenv("CI") == "true" && cfg.Addr == "127.0.0.1:3306")) {
		t.Fatal("dedicated local test endpoint without database required")
	}
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	var original int64
	if err = admin.QueryRow("SELECT @@GLOBAL.max_allowed_packet").Scan(&original); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec("SET GLOBAL max_allowed_packet=?", original) }()
	if _, err = admin.Exec("SET GLOBAL max_allowed_packet=16777216"); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("trace2947_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec("DROP DATABASE `" + name + "`") }()
	cfg.DBName = name
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err = sessionstore.New(db).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.AddDate(0, 0, -31)
	source := &logCapacitySource{}
	for i := 0; i < 10000; i++ {
		payload, _ := json.Marshal(map[string]any{"@timestamp": old.Format(time.RFC3339Nano), "message": strings.Repeat("x", 1400)})
		source.rows = append(source.rows, archivesvc.Candidate{ID: fmt.Sprintf("log-%05d", i), OccurredAt: old, Payload: payload})
	}
	raw, err := json.Marshal(source.rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 16<<20 {
		t.Fatal("fixture does not reproduce original Base64 expansion")
	}
	// Reproduce only the old checkpoint statement in this disposable database.
	legacy := archivesvc.Job{ID: "legacy-oversized", Kind: observabilityvo.ArchiveKindLog, Range: observabilityvo.ArchiveRange{To: now}, Status: observabilityvo.ArchiveStatusFailed, CandidateIDs: []string{}, CreatedAt: now, UpdatedAt: now}
	if err = New(db).Create(legacy); err != nil {
		t.Fatal(err)
	}
	// Oversized packets cause the server to close the connection. Keep this
	// deliberate negative control out of the pool used to verify the new flow.
	faultDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	// Do not retain the deliberately broken connection for pool shutdown.
	faultDB.SetMaxIdleConns(0)
	_, oldWriteErr := faultDB.Exec("UPDATE bkn_trace_archive_jobs SET candidate_payloads=? WHERE archive_job_id=?", raw, legacy.ID)
	if err = faultDB.Close(); err != nil {
		t.Fatal(err)
	}
	if oldWriteErr == nil {
		t.Fatal("old oversized checkpoint unexpectedly succeeded")
	}
	t.Logf("old checkpoint rejected at %d bytes; source still contains %d synthetic logs", len(raw), len(source.rows))
	store, err := NewWithDSN(db, cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	objects := &checkpointObservingObjects{t: t, store: store}
	source.store = store
	service := archivesvc.New(store, source, objects, archivesvc.Options{})
	job, err := service.Create(ctx, observabilityvo.ArchiveKindLog)
	if err != nil || len(source.rows) != 0 || objects.writes < 2 || job.BatchProgress.CleanedInRun != 10000 || !job.BatchProgress.RunComplete {
		t.Fatalf("capacity split failed: job=%+v err=%v writes=%d left=%d", job, err, objects.writes, len(source.rows))
	}
	t.Logf("new flow: %d uploads; every full checkpoint durable before upload, all 10000 logs cleaned only after verified metadata", objects.writes)
	// Pin an old connection, lower GLOBAL, and prove SESSION does not change.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err = conn.QueryRowContext(ctx, "SELECT @@SESSION.max_allowed_packet").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec("SET GLOBAL max_allowed_packet=4194304"); err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(ctx, "SELECT @@SESSION.max_allowed_packet").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("existing session unexpectedly changed")
	}
	_ = conn.Close()
	fresh, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	fresh.SetMaxOpenConns(3)
	defer func() { _ = fresh.Close() }()
	var newer int64
	if err = fresh.QueryRow("SELECT @@SESSION.max_allowed_packet").Scan(&newer); err != nil || newer != 4<<20 {
		t.Fatalf("new session=%d err=%v", newer, err)
	}
	for _, interpolate := range []bool{false, true} {
		clientCfg := *cfg
		clientCfg.InterpolateParams = interpolate
		clientCfg.MaxAllowedPacket = 2 << 20
		limited, err := sql.Open("mysql", clientCfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		limited.SetMaxOpenConns(3)
		limitedStore, err := NewWithDSN(limited, clientCfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		rows := &logCapacitySource{}
		for i := 0; i < 4; i++ {
			payload, _ := json.Marshal(map[string]string{"message": strings.Repeat("y", 400000)})
			rows.rows = append(rows.rows, archivesvc.Candidate{ID: fmt.Sprintf("limited-%t-%d", interpolate, i), OccurredAt: old, Payload: payload})
		}
		obj := &checkpointObservingObjects{t: t, store: limitedStore}
		rows.store = limitedStore
		last, e := archivesvc.New(limitedStore, rows, obj, archivesvc.Options{}).Create(ctx, observabilityvo.ArchiveKindLog)
		if e != nil || len(rows.rows) != 0 || obj.writes != 4 || !last.BatchProgress.RunComplete {
			t.Fatalf("client packet limit/interpolation=%v writes=%d err=%v", interpolate, obj.writes, e)
		}
		_ = limited.Close()
	}
	t.Log("GLOBAL shrink observed; existing SESSION retained; new SESSION changed; 2MiB client limits honored with interpolation on/off")
}

type logCapacitySource struct {
	rows  []archivesvc.Candidate
	store *Store
}

func (s *logCapacitySource) Freeze(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) ([]archivesvc.Candidate, error) {
	return nil, fmt.Errorf("bounded source required")
}
func (s *logCapacitySource) FreezeBatch(ctx context.Context, _ observabilityvo.ArchiveKind, _ observabilityvo.ArchiveRange) (archivesvc.Batch, error) {
	n := min(len(s.rows), archivesvc.LogLimitsFromContext(ctx).MaxCandidates)
	return archivesvc.Batch{Candidates: append([]archivesvc.Candidate(nil), s.rows[:n]...), EligibleCount: len(s.rows), Batched: true}, nil
}
func (s *logCapacitySource) Purge(_ context.Context, _ observabilityvo.ArchiveKind, c []archivesvc.Candidate) error {
	jobs := s.store.List(observabilityvo.ArchiveKindLog, 1)
	if len(jobs) != 1 || jobs[0].Status != observabilityvo.ArchiveStatusCleanupIncomplete || jobs[0].ManifestRef == "" {
		return fmt.Errorf("cleanup preceded verified metadata commit")
	}
	ids := map[string]bool{}
	for _, v := range c {
		ids[v.ID] = true
	}
	kept := []archivesvc.Candidate{}
	for _, v := range s.rows {
		if !ids[v.ID] {
			kept = append(kept, v)
		}
	}
	s.rows = kept
	return nil
}

type checkpointObservingObjects struct {
	t      *testing.T
	store  *Store
	writes int
}

func (o *checkpointObservingObjects) WriteAndVerify(_ context.Context, j archivesvc.Job, c []archivesvc.Candidate) (string, error) {
	o.writes++
	saved, ok := o.store.Get(j.ID)
	if !ok || saved.Status != observabilityvo.ArchiveStatusFailed || len(saved.Candidates) != len(c) || saved.BatchProgress == nil || !saved.BatchProgress.CheckpointPrepared || saved.BatchProgress.VerifiedCount != 0 {
		o.t.Fatalf("upload preceded durable full checkpoint: %+v", saved)
	}
	for i := range c {
		if string(saved.Candidates[i].Payload) != string(c[i].Payload) {
			o.t.Fatal("frozen payload changed")
		}
	}
	return "synthetic/" + j.ID + "/manifest.json", nil
}
