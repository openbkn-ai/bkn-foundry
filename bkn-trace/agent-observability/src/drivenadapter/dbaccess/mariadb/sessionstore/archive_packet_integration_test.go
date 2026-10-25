// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

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
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/archivestore"
)

// Verify the full batching workflow can persist its frozen cleanup checkpoint.
func TestTraceArchiveCheckpointFitsMariaDBPacket(t *testing.T) {
	dsn := os.Getenv("BKN_TRACE_ARCHIVE_BATCH_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit isolated packet reproduction DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated packet regression DSN")
	}
	dedicated := cfg.Addr == "127.0.0.1:13341" || (os.Getenv("CI") == "true" && cfg.Addr == "127.0.0.1:3306")
	if cfg.Net != "tcp" || !dedicated || cfg.DBName != "" {
		t.Fatal("dedicated local archive packet endpoint without database required")
	}
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	name := fmt.Sprintf("bkn_trace_archive_packet_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(clean, "DROP DATABASE `"+name+"`"); err != nil {
			t.Error(err)
		}
	}()
	cfg.DBName = name
	cfg.ParseTime = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err = New(db).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var packet uint64
	if err = db.QueryRowContext(ctx, "SELECT @@max_allowed_packet").Scan(&packet); err != nil {
		t.Fatal(err)
	}

	if packet != 16<<20 {
		t.Skipf("default 16MiB server packet boundary required, got %d", packet)
	}
	t.Logf("server max_allowed_packet=%d driver maxAllowedPacket=%d interpolateParams=%v", packet, cfg.MaxAllowedPacket, cfg.InterpolateParams)
	now := time.Now().UTC()
	payload := []byte(`{"data":"` + strings.Repeat("a", (7<<20)-11) + `"}`)
	if len(payload) != 7<<20 || !json.Valid(payload) {
		t.Fatal("invalid bounded JSON fixture")
	}
	objects := &packetArchiveObjects{}
	jobs := archivestore.New(db)
	core := &packetArchiveCore{rows: []archivesvc.Candidate{{ID: "a", OccurredAt: now, Payload: payload}, {ID: "b", OccurredAt: now, Payload: payload}}, objects: objects, store: jobs}
	service := archivesvc.New(jobs, archivesvc.TraceBundleSource{Core: core}, objects, archivesvc.Options{Now: func() time.Time { return now }})
	final, err := service.Create(ctx, observabilityvo.ArchiveKindTrace)
	if err != nil || final.Status != observabilityvo.ArchiveStatusCompleted || final.BatchProgress == nil || !final.BatchProgress.RunComplete || final.BatchProgress.CleanedInRun != 2 || core.purges != 2 || len(core.rows) != 0 || len(objects.counts) != 2 {
		t.Fatalf("checkpoint workflow failed: status=%s progress=%+v writes=%v purges=%d remaining=%d err=%v", final.Status, final.BatchProgress, objects.counts, core.purges, len(core.rows), err)
	}
	for _, count := range objects.counts {
		if count != 1 {
			t.Fatalf("unbounded object batch count=%d", count)
		}
	}
	stored := jobs.List(observabilityvo.ArchiveKindTrace, 10)
	if len(stored) != 2 {
		t.Fatalf("persisted jobs=%d", len(stored))
	}
	for _, job := range stored {
		if job.CandidateCount != 1 || job.Status != observabilityvo.ArchiveStatusCompleted {
			t.Fatalf("unexpected stored batch: count=%d status=%s", job.CandidateCount, job.Status)
		}
	}
	t.Log("two 7MiB JSON candidates completed as two durable one-candidate batches, two purges, cleaned_in_run=2")
}

type packetArchiveObjects struct {
	lastID string
	counts []int
}

func (s *packetArchiveObjects) WriteAndVerify(_ context.Context, job archivesvc.Job, candidates []archivesvc.Candidate) (string, error) {
	s.lastID = job.ID
	s.counts = append(s.counts, len(candidates))
	return "archive/" + job.ID + "/manifest.json", nil
}

type packetArchiveCore struct {
	rows    []archivesvc.Candidate
	purges  int
	objects *packetArchiveObjects
	store   *archivestore.Store
}

func (s *packetArchiveCore) Freeze(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) ([]archivesvc.Candidate, error) {
	return append([]archivesvc.Candidate(nil), s.rows...), nil
}
func (s *packetArchiveCore) FreezeBatch(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) (archivesvc.Batch, error) {
	return archivesvc.Batch{Candidates: append([]archivesvc.Candidate(nil), s.rows...), EligibleCount: len(s.rows), Batched: true}, nil
}
func (s *packetArchiveCore) Purge(_ context.Context, _ observabilityvo.ArchiveKind, candidates []archivesvc.Candidate) error {
	job, ok := s.store.Get(s.objects.lastID)
	if !ok || job.Status != observabilityvo.ArchiveStatusCleanupIncomplete || len(job.Candidates) != 1 || len(candidates) != 1 || job.Candidates[0].ID != candidates[0].ID || len(job.Candidates[0].Payload) != len(candidates[0].Payload) {
		return fmt.Errorf("cleanup checkpoint was not durably readable")
	}
	s.purges++
	kept := s.rows[:0]
	for _, row := range s.rows {
		if row.ID != candidates[0].ID {
			kept = append(kept, row)
		}
	}
	s.rows = kept
	return nil
}
