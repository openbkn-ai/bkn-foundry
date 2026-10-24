// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type batchSourceFixture struct {
	t                *testing.T
	store            Store
	rows             []Candidate
	size             int
	window           observabilityvo.ArchiveRange
	failCleanup      bool
	cancelAfterFirst context.CancelFunc
	freezes, purges  int
	writtenID        string
	events           []string
	entered, release chan struct{}
}

func newBatchSourceFixture(t *testing.T, n, size int) *batchSourceFixture {
	s := &batchSourceFixture{t: t, store: NewMemoryStore(), size: size}
	for i := 0; i < n; i++ {
		s.rows = append(s.rows, Candidate{ID: fmt.Sprintf("log-%05d", i), OccurredAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Payload: []byte(`{"message":"fixture"}`)})
	}
	return s
}
func (s *batchSourceFixture) Freeze(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) ([]Candidate, error) {
	return nil, errors.New("legacy freeze must not run")
}
func (s *batchSourceFixture) FreezeBatch(ctx context.Context, k observabilityvo.ArchiveKind, w observabilityvo.ArchiveRange) (Batch, error) {
	s.freezes++
	if s.entered != nil {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return Batch{}, ctx.Err()
		}
		s.entered = nil
	}
	if s.window.To.IsZero() {
		s.window = w
	} else if s.window != w {
		s.t.Fatal("cutoff changed between batches")
	}
	return Batch{Candidates: append([]Candidate(nil), s.rows[:min(s.size, len(s.rows))]...), EligibleCount: len(s.rows), Batched: true}, nil
}
func (s *batchSourceFixture) Purge(_ context.Context, _ observabilityvo.ArchiveKind, c []Candidate) error {
	s.purges++
	j, ok := s.store.Get(s.writtenID)
	if !ok || j.ManifestRef == "" || j.Status != observabilityvo.ArchiveStatusCleanupIncomplete || j.BatchProgress.VerifiedCount != len(c) || len(j.Candidates) != len(c) {
		s.t.Fatal("cleanup preceded durable verified state")
	}
	s.events = append(s.events, "purge")
	ids := map[string]bool{}
	for _, r := range c {
		ids[r.ID] = true
	}
	if s.failCleanup {
		ids = map[string]bool{c[0].ID: true}
	}
	kept := []Candidate{}
	for _, r := range s.rows {
		if !ids[r.ID] {
			kept = append(kept, r)
		}
	}
	s.rows = kept
	if s.failCleanup {
		return errors.New("partial cleanup")
	}
	if s.cancelAfterFirst != nil && s.purges == 1 {
		s.cancelAfterFirst()
	}
	return nil
}

type batchObjectFixture struct {
	s              *batchSourceFixture
	writes, failAt int
	emptyManifest  bool
}

func (o *batchObjectFixture) WriteAndVerify(_ context.Context, j Job, c []Candidate) (string, error) {
	o.writes++
	o.s.events = append(o.s.events, "verify")
	o.s.writtenID = j.ID
	if o.writes == o.failAt {
		return "", errors.New("checksum mismatch")
	}
	if o.emptyManifest {
		return "", nil
	}
	return "objects/" + j.ID + "/manifest.json", nil
}
func batchService(s *batchSourceFixture, o *batchObjectFixture) *Service {
	return New(s.store, s, o, Options{Now: func() time.Time { return time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC) }, Location: time.UTC})
}

func TestOneRequestArchives10001InTwoVerifiedBatches(t *testing.T) {
	s := newBatchSourceFixture(t, 10001, 10000)
	o := &batchObjectFixture{s: s}
	svc := batchService(s, o)
	last, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err != nil || len(s.rows) != 0 || o.writes != 2 || s.purges != 2 || last.CandidateCount != 1 {
		t.Fatalf("last=%+v err=%v remaining=%d", last, err, len(s.rows))
	}
	p := last.BatchProgress
	if p == nil || p.BatchNumber != 2 || p.CleanedInRun != 10001 || p.CleanedCount != 1 || !p.RunComplete {
		t.Fatalf("incorrect progress: %+v", p)
	}
	if fmt.Sprint(s.events) != "[verify purge verify purge]" {
		t.Fatalf("wrong order %v", s.events)
	}
	jobs := s.store.List(observabilityvo.ArchiveKindLog, 20)
	if len(jobs) != 2 || jobs[0].ID != last.ID || jobs[1].CandidateCount != 10000 || jobs[1].BatchProgress.CleanedInRun != 10000 || jobs[1].BatchProgress.RemainingAtSelection != 1 || jobs[1].BatchProgress.RunComplete || jobs[1].BatchProgress.RunID != p.RunID || jobs[0].ManifestRef == jobs[1].ManifestRef {
		t.Fatalf("batch history inaccurate: jobs=%d last=%s", len(jobs), last.ID)
	}
}

func TestLaterBatchVerificationFailureRetainsItsHotRecords(t *testing.T) {
	s := newBatchSourceFixture(t, 3, 2)
	o := &batchObjectFixture{s: s, failAt: 2}
	svc := batchService(s, o)
	last, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err == nil || last.Status != observabilityvo.ArchiveStatusFailed || len(s.rows) != 1 || s.purges != 1 || last.BatchProgress.CleanedInRun != 2 || last.BatchProgress.VerifiedCount != 0 {
		t.Fatalf("failed batch lost: %+v err=%v rows=%d", last, err, len(s.rows))
	}
	o.failAt = 0
	last, err = svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err != nil || len(s.rows) != 0 || !last.BatchProgress.RunComplete {
		t.Fatalf("new request cannot continue: %+v %v", last, err)
	}
}

func TestCleanupRetryUsesFrozenBatchThenContinuesSameRun(t *testing.T) {
	s := newBatchSourceFixture(t, 3, 2)
	s.failCleanup = true
	o := &batchObjectFixture{s: s}
	svc := batchService(s, o)
	stopped, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err != nil || stopped.Status != observabilityvo.ArchiveStatusCleanupIncomplete || o.writes != 1 || s.freezes != 1 || stopped.BatchProgress.CleanedInRun != 0 {
		t.Fatalf("unsafe failure: %+v %v", stopped, err)
	}
	if err := s.store.Create(Job{ID: "newer", Kind: observabilityvo.ArchiveKindLog, Status: observabilityvo.ArchiveStatusFailed, CreatedAt: stopped.CreatedAt.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog); !errors.Is(err, ErrCleanupRequired) {
		t.Fatalf("pending cleanup bypassed: %v", err)
	}
	s.failCleanup = false
	last, err := svc.RetryCleanup(context.Background(), stopped.ID)
	if err != nil || len(s.rows) != 0 || o.writes != 2 || s.purges != 3 || last.BatchProgress.RunID != stopped.BatchProgress.RunID || last.BatchProgress.CleanedInRun != 3 || !last.BatchProgress.RunComplete {
		t.Fatalf("retry failed: %+v err=%v writes=%d purges=%d", last, err, o.writes, s.purges)
	}
	old, _ := s.store.Get(stopped.ID)
	if old.Status != observabilityvo.ArchiveStatusCompleted || old.BatchProgress.CleanedCount != 2 {
		t.Fatalf("original cleanup not recorded: %+v", old)
	}
}

func TestConcurrentCreatesShareKindLockAcrossServiceInstances(t *testing.T) {
	s := newBatchSourceFixture(t, 1, 1)
	s.entered = make(chan struct{})
	s.release = make(chan struct{})
	o := &batchObjectFixture{s: s}
	first := batchService(s, o)
	second := batchService(s, o)
	entered, release := s.entered, s.release
	var wg sync.WaitGroup
	wg.Add(1)
	var errFirst error
	go func() {
		defer wg.Done()
		_, errFirst = first.Create(context.Background(), observabilityvo.ArchiveKindLog)
	}()
	<-entered
	_, err := second.Create(context.Background(), observabilityvo.ArchiveKindLog)
	close(release)
	wg.Wait()
	if !errors.Is(err, ErrArchiveBusy) || errFirst != nil || o.writes != 1 {
		t.Fatalf("duplicate processing: first=%v second=%v writes=%d", errFirst, err, o.writes)
	}
}

func TestEmptyVerifiedManifestCannotDeleteBatch(t *testing.T) {
	s := newBatchSourceFixture(t, 2, 2)
	o := &batchObjectFixture{s: s, emptyManifest: true}
	job, err := batchService(s, o).Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err == nil || job.Status != observabilityvo.ArchiveStatusFailed || s.purges != 0 || len(s.rows) != 2 {
		t.Fatalf("empty manifest accepted: err=%v purges=%d", err, s.purges)
	}
}

func TestRequestCancellationRetainsCompletedBatchAndCanContinue(t *testing.T) {
	s := newBatchSourceFixture(t, 3, 2)
	o := &batchObjectFixture{s: s}
	svc := batchService(s, o)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.cancelAfterFirst = cancel
	stopped, err := svc.Create(ctx, observabilityvo.ArchiveKindLog)
	if !errors.Is(err, context.Canceled) || stopped.Status != observabilityvo.ArchiveStatusCompleted || stopped.BatchProgress.CleanedInRun != 2 || stopped.BatchProgress.RunComplete || len(s.rows) != 1 {
		t.Fatalf("interruption progress lost: err=%v progress=%+v", err, stopped.BatchProgress)
	}
	stored, ok := s.store.Get(stopped.ID)
	if !ok || stored.BatchProgress.CleanedInRun != 2 {
		t.Fatal("completed batch was not durable")
	}
	s.cancelAfterFirst = nil
	last, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err != nil || len(s.rows) != 0 || !last.BatchProgress.RunComplete || last.BatchProgress.RunID == stopped.BatchProgress.RunID {
		t.Fatalf("remaining records cannot continue: %v", err)
	}
}
