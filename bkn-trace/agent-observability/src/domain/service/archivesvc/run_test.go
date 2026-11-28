// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

type runClock struct{ now time.Time }

func (c *runClock) Now() time.Time { return c.now }

func runService(source Source, s *batchSourceFixture, o ObjectStore, clock *runClock, options Options) *Service {
	options.Now, options.Location = clock.Now, time.UTC
	return New(s.store, source, o, options)
}

func mustSubmit(t *testing.T, svc *Service, kind observabilityvo.ArchiveKind) RunView {
	t.Helper()
	run, err := svc.Submit(context.Background(), kind)
	if err != nil || run.Status != RunQueued || run.EligibleAtStart == 0 {
		t.Fatalf("submit: %+v %v", run, err)
	}
	return run
}

func runState(t *testing.T, svc *Service, id string) RunView {
	t.Helper()
	result, err := svc.RunResult(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBackgroundRunArchivesEveryBatchAndRecordsProgress(t *testing.T) {
	s := newBatchSourceFixture(t, 5, 2)
	o := &batchObjectFixture{s: s}
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, o, clock, Options{})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	if o.writes != 0 || s.purges != 0 {
		t.Fatal("submission must not run a batch")
	}
	if _, err := svc.Submit(context.Background(), observabilityvo.ArchiveKindTrace); !errors.Is(err, ErrArchiveBusy) {
		t.Fatalf("second active run allowed: %v", err)
	}
	svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	view := runState(t, svc, run.ID)
	if view.Status != RunCompleted || view.ProcessedCount != 5 || view.BatchCount != 3 || view.EligibleAtStart != 5 || view.FinishedAt.IsZero() || len(s.rows) != 0 {
		t.Fatalf("run did not finish every batch: %+v rows=%d", view, len(s.rows))
	}
	overview, err := svc.Overview(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || overview.CurrentRun == nil || overview.CurrentRun.ID != run.ID || overview.CandidateCount != 0 {
		t.Fatalf("overview lost the finished run: %+v %v", overview, err)
	}
}

func TestBackgroundRunRetainsOversizedInteractionAndContinues(t *testing.T) {
	for _, phase := range []string{"core", "response"} {
		t.Run(phase, func(t *testing.T) {
			s := newBatchSourceFixture(t, 4, 4)
			blockedID := s.rows[1].ID
			if phase == "core" {
				s.rows[1].Payload = make([]byte, MaxTraceArchiveCandidateBytes+1)
			}
			o := &batchObjectFixture{s: s}
			clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
			source := TraceBundleSource{Core: s, Technical: &selectiveOversizedArchive{id: blockedID, phase: phase}}
			svc := runService(source, s, o, clock, Options{})
			run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
			svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
			result, err := svc.RunResult(context.Background(), run.ID)
			page, pageErr := svc.RetainedInteractions(context.Background(), run.ID, 0, 0)
			if err != nil || pageErr != nil || result.Status != RunCompleted || result.ProcessedCount != 3 || result.RetainedCount != 1 || page.Total != 1 || len(page.Items) != 1 {
				t.Fatalf("run did not continue past the oversized interaction: %+v %+v %v %v", result, page, err, pageErr)
			}
			kept := page.Items[0]
			if kept.InteractionID != blockedID || kept.RunID != run.ID || kept.PolicyVersion != TraceArchivePolicyVersion() || kept.Budget == 0 || kept.Measured <= kept.Budget {
				t.Fatalf("retained record incomplete: %+v", kept)
			}
			if len(s.rows) != 1 || s.rows[0].ID != blockedID {
				t.Fatalf("oversized interaction must stay online and only it: %+v", s.rows)
			}
			overview, err := svc.Overview(context.Background(), observabilityvo.ArchiveKindTrace)
			if err != nil || overview.CandidateCount != 0 || overview.RetainedCount != 1 {
				t.Fatalf("retained interaction still counted as a candidate: %+v %v", overview, err)
			}
			if _, err := svc.Submit(context.Background(), observabilityvo.ArchiveKindTrace); !errors.Is(err, ErrNothingToArchive) {
				t.Fatalf("retained interaction would start an empty run: %v", err)
			}
		})
	}
}

func TestRetainedInteractionIsEvaluatedAgainUnderANewPolicy(t *testing.T) {
	s := newBatchSourceFixture(t, 1, 1)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, &batchObjectFixture{s: s}, clock, Options{})
	runs := s.store.(RunStore)
	if err := runs.RetainInteraction(context.Background(), RetainedInteraction{InteractionID: s.rows[0].ID, RunID: "old", PolicyVersion: "trace-v0", RetainedAt: clock.now}); err != nil {
		t.Fatal(err)
	}
	overview, err := svc.Overview(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || overview.CandidateCount != 1 || overview.RetainedCount != 0 {
		t.Fatalf("an old-policy retention still excludes the interaction: %+v %v", overview, err)
	}
}

type alwaysFailingObjects struct{ writes int }

func (o *alwaysFailingObjects) WriteAndVerify(context.Context, Job, []Candidate) (string, error) {
	o.writes++
	return "", errors.New("object storage unavailable at https://private.test/?signature=x")
}

func TestBackgroundRunRetriesTransientFailuresThenFails(t *testing.T) {
	s := newBatchSourceFixture(t, 2, 2)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	objects := &alwaysFailingObjects{}
	svc := runService(s, s, objects, clock, Options{})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	for attempt := 0; attempt <= MaxRunRetries; attempt++ {
		svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
		view := runState(t, svc, run.ID)
		if attempt < MaxRunRetries {
			if view.Status != RunRetryWait || view.RetryCount != attempt+1 || view.LastFailureCode != RunFailureTemporary || !view.NextAttemptAt.Equal(clock.now.Add(runRetryDelays[attempt])) {
				t.Fatalf("attempt %d: %+v", attempt, view)
			}
			svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
			if objects.writes != attempt+1 {
				t.Fatal("retried before its scheduled time")
			}
			clock.now = view.NextAttemptAt
			continue
		}
		if view.Status != RunFailed || view.RetryCount != MaxRunRetries || view.LastFailureCode != RunFailureTemporary || view.FinishedAt.IsZero() {
			t.Fatalf("retries not bounded: %+v", view)
		}
	}
	if objects.writes != MaxRunRetries+1 || len(s.rows) != 2 || s.purges != 0 {
		t.Fatal("failed writes must keep every source record")
	}
}

func TestBackgroundRunRecoversFromTransientFailureAndCleanupInterruption(t *testing.T) {
	s := newBatchSourceFixture(t, 4, 2)
	s.failCleanup = true
	o := &batchObjectFixture{s: s, failAt: 1}
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, o, clock, Options{})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	if view := runState(t, svc, run.ID); view.Status != RunRetryWait || view.LastFailureCode != RunFailureTemporary {
		t.Fatalf("write failure: %+v", view)
	}
	clock.now = clock.now.Add(time.Hour)
	svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	if view := runState(t, svc, run.ID); view.Status != RunRetryWait || view.LastFailureCode != RunFailureCleanupPending || view.RetryCount != 2 {
		t.Fatalf("partial cleanup: %+v", view)
	}
	s.failCleanup = false
	clock.now = clock.now.Add(time.Hour)
	svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	view := runState(t, svc, run.ID)
	if view.Status != RunCompleted || view.ProcessedCount != 4 || view.RetryCount != 2 || len(s.rows) != 0 {
		t.Fatalf("run did not resume after reverified cleanup: %+v rows=%d", view, len(s.rows))
	}
}

func TestBackgroundRunWithInvalidConfigurationFailsWithoutRetry(t *testing.T) {
	s := newBatchSourceFixture(t, 2, 2)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, &batchObjectFixture{s: s}, clock, Options{LogLimits: func(context.Context) (LogLimits, error) {
		return LogLimits{}, errors.New("configuration unreadable")
	}})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindLog)
	svc.processDue(context.Background(), observabilityvo.ArchiveKindLog)
	if view := runState(t, svc, run.ID); view.Status != RunFailed || view.LastFailureCode != RunFailureConfigurationInvalid || view.RetryCount != 0 || len(s.rows) != 2 {
		t.Fatalf("configuration error should fail at once: %+v", view)
	}
}

func TestStoppedWorkerFinishesBatchAndAnotherAttemptResumes(t *testing.T) {
	s := newBatchSourceFixture(t, 4, 2)
	o := &batchObjectFixture{s: s}
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, o, clock, Options{})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	s.entered, s.release = make(chan struct{}), make(chan struct{})
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.processDue(ctx, observabilityvo.ArchiveKindTrace); close(done) }()
	<-s.entered
	stop()
	close(s.release)
	<-done
	view := runState(t, svc, run.ID)
	if view.Status != RunRunning || view.ProcessedCount != 2 || len(s.rows) != 2 {
		t.Fatalf("stop should end after the current batch and keep the run: %+v", view)
	}
	restarted := runService(s, s, o, clock, Options{})
	restarted.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	if view := runState(t, restarted, run.ID); view.Status != RunCompleted || view.ProcessedCount != 4 || view.BatchCount != 2 || len(s.rows) != 0 {
		t.Fatalf("restart did not resume the run: %+v", view)
	}
}

func TestRetryCleanupRunsInBackgroundAndContinuesTheOriginalRun(t *testing.T) {
	s := newBatchSourceFixture(t, 4, 2)
	s.failCleanup = true
	o := &batchObjectFixture{s: s}
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, o, clock, Options{})
	job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		t.Fatalf("fixture: %+v %v", job, err)
	}
	if _, err := svc.Submit(context.Background(), observabilityvo.ArchiveKindTrace); !errors.Is(err, ErrCleanupRequired) {
		t.Fatalf("pending cleanup must block a new run: %v", err)
	}
	s.failCleanup = false
	run, err := svc.SubmitRetryCleanup(context.Background(), job.ID)
	if err != nil || run.Trigger != RunTriggerRetryCleanup || run.BatchRunID != job.BatchProgress.RunID || run.EligibleAtStart != 4 {
		t.Fatalf("retry submission: %+v %v", run, err)
	}
	svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	if view := runState(t, svc, run.ID); view.Status != RunCompleted || view.ProcessedCount != 4 || len(s.rows) != 0 {
		t.Fatalf("retry run did not finish the original run: %+v rows=%d", view, len(s.rows))
	}
}

func TestRunWorkerWakesOnSubmitAndStopsWithContext(t *testing.T) {
	s := newBatchSourceFixture(t, 3, 2)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, &batchObjectFixture{s: s}, clock, Options{})
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { svc.RunWorker(ctx, time.Hour); close(stopped) }()
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	deadline := time.Now().Add(5 * time.Second)
	for runState(t, svc, run.ID).Status != RunCompleted {
		if time.Now().After(deadline) {
			t.Fatal("worker did not pick up the submitted run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestClassifyRunOutcome(t *testing.T) {
	for _, tc := range []struct {
		last  Job
		err   error
		code  string
		retry bool
	}{
		{err: nil, code: ""},
		{err: fmt.Errorf("x: %w", ErrNothingToArchive), code: ""},
		{last: Job{Status: observabilityvo.ArchiveStatusCleanupIncomplete}, code: RunFailureCleanupPending, retry: true},
		{err: fmt.Errorf("x: %w", ErrInvalidLogLimits), code: RunFailureConfigurationInvalid},
		{err: &Failure{Stage: "payload_bytes"}, code: RunFailureCapacityExceeded},
		{err: &PayloadBudgetError{InteractionID: "x"}, code: RunFailureOversizedInteraction},
		{err: fmt.Errorf("%w: mismatch", ErrCleanupVerificationFailed), code: RunFailureCleanupVerification},
		{err: ErrCleanupRequired, code: RunFailureCleanupRequired},
		{err: fmt.Errorf("x: %w", ErrIncompleteCandidates), code: RunFailureRecordsNotReady, retry: true},
		{err: errors.New("timeout"), code: RunFailureTemporary, retry: true},
	} {
		if code, retry := classifyRunOutcome(tc.last, tc.err); code != tc.code || retry != tc.retry {
			t.Fatalf("%v: got %q %v", tc.err, code, retry)
		}
	}
}

func TestBackgroundLogRunArchivesEveryBatch(t *testing.T) {
	s := newBatchSourceFixture(t, 5, 2)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, &batchObjectFixture{s: s}, clock, Options{})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindLog)
	svc.processDue(context.Background(), observabilityvo.ArchiveKindLog)
	if view := runState(t, svc, run.ID); view.Status != RunCompleted || view.ProcessedCount != 5 || view.BatchCount != 3 || len(s.rows) != 0 {
		t.Fatalf("log run: %+v rows=%d", view, len(s.rows))
	}
}

func TestRetryCleanupCompletesWhenAnotherCleanupIsStillPending(t *testing.T) {
	s := newBatchSourceFixture(t, 4, 2)
	s.failCleanup = true
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, &batchObjectFixture{s: s}, clock, Options{})
	job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || job.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		t.Fatalf("fixture: %+v %v", job, err)
	}
	other := Job{ID: "older-pending", Kind: observabilityvo.ArchiveKindTrace, Status: observabilityvo.ArchiveStatusCleanupIncomplete, CreatedAt: clock.now.Add(-time.Hour)}
	if err := s.store.Create(other); err != nil {
		t.Fatal(err)
	}
	s.failCleanup = false
	run, err := svc.SubmitRetryCleanup(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	if view := runState(t, svc, run.ID); view.Status != RunCompleted || view.LastFailureCode != "" {
		t.Fatalf("requested cleanup succeeded but run did not complete: %+v", view)
	}
	if cleaned, _ := s.store.Get(job.ID); cleaned.Status != observabilityvo.ArchiveStatusCompleted || len(s.rows) != 2 {
		t.Fatalf("cleanup or continuation wrong: %+v rows=%d", cleaned, len(s.rows))
	}
}

type panickingSource struct{ batchSourceFixture }

func (s *panickingSource) FreezeBatch(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) (Batch, error) {
	panic("defect in archive code")
}

func TestPanickingRunFailsInsteadOfCrashingTheService(t *testing.T) {
	s := newBatchSourceFixture(t, 2, 2)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(&panickingSource{batchSourceFixture: *s}, s, &batchObjectFixture{s: s}, clock, Options{})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace)
	if view := runState(t, svc, run.ID); view.Status != RunFailed || view.LastFailureCode != RunFailureUnexpected {
		t.Fatalf("panic not contained: %+v", view)
	}
	if _, err := svc.Submit(context.Background(), observabilityvo.ArchiveKindTrace); err != nil {
		t.Fatalf("failed run still blocks new runs: %v", err)
	}
}

type hangingSource struct{ batchSourceFixture }

func (s *hangingSource) FreezeBatch(ctx context.Context, _ observabilityvo.ArchiveKind, _ observabilityvo.ArchiveRange) (Batch, error) {
	<-ctx.Done()
	return Batch{}, ctx.Err()
}

func TestHungBatchTimesOutAndReleasesTheKind(t *testing.T) {
	s := newBatchSourceFixture(t, 2, 2)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(&hangingSource{batchSourceFixture: *s}, s, &batchObjectFixture{s: s}, clock, Options{})
	svc.batchTimeout = 20 * time.Millisecond
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	done := make(chan struct{})
	go func() { svc.processDue(context.Background(), observabilityvo.ArchiveKindTrace); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("hung batch held the kind lock")
	}
	if view := runState(t, svc, run.ID); view.Status != RunRetryWait || view.LastFailureCode != RunFailureTemporary {
		t.Fatalf("timed-out batch should wait for a retry: %+v", view)
	}
}

func TestStopGraceCancelsAHungBatchAndKeepsTheRunForResume(t *testing.T) {
	s := newBatchSourceFixture(t, 2, 2)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(&hangingSource{batchSourceFixture: *s}, s, &batchObjectFixture{s: s}, clock, Options{})
	svc.stopGrace = 20 * time.Millisecond
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.processDue(ctx, observabilityvo.ArchiveKindTrace); close(done) }()
	time.Sleep(20 * time.Millisecond)
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited for a hung batch beyond the stop grace")
	}
	if view := runState(t, svc, run.ID); view.Status != RunRunning || view.RetryCount != 0 {
		t.Fatalf("a stop must leave the run for the next start: %+v", view)
	}
}

func TestRetainedInteractionsArePagedWithTotals(t *testing.T) {
	s := newBatchSourceFixture(t, 1, 1)
	clock := &runClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
	svc := runService(s, s, &batchObjectFixture{s: s}, clock, Options{})
	run := mustSubmit(t, svc, observabilityvo.ArchiveKindTrace)
	runs := s.store.(RunStore)
	for i := 0; i < 45; i++ {
		item := RetainedInteraction{InteractionID: fmt.Sprintf("large-%02d", i), RunID: run.ID, PolicyVersion: TraceArchivePolicyVersion(), Budget: MaxTraceArchiveCandidateBytes, Unit: "bytes", RetainedAt: clock.now.Add(time.Duration(i) * time.Second)}
		if i == 44 {
			item.RunID, item.PolicyVersion = "older-run", "trace-v0"
		}
		if err := runs.RetainInteraction(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	first, err := svc.RetainedInteractions(context.Background(), "", 0, 0)
	if err != nil || first.Total != 44 || len(first.Items) != DefaultRetainedPageSize || first.Items[0].InteractionID != "large-43" {
		t.Fatalf("first page of current-policy retention: %+v %v", first, err)
	}
	last, _ := svc.RetainedInteractions(context.Background(), "", 40, 20)
	if last.Total != 44 || len(last.Items) != 4 || last.Items[3].InteractionID != "large-00" {
		t.Fatalf("last page: %+v", last)
	}
	beyond, _ := svc.RetainedInteractions(context.Background(), "", 60, 20)
	if beyond.Total != 44 || len(beyond.Items) != 0 {
		t.Fatalf("page beyond the end: %+v", beyond)
	}
	capped, _ := svc.RetainedInteractions(context.Background(), run.ID, 0, 1000)
	if capped.Total != 44 || len(capped.Items) != 44 {
		t.Fatalf("run filter or page cap wrong: %+v", capped)
	}
	if view := runState(t, svc, run.ID); view.RetainedCount != 44 {
		t.Fatalf("run retained count must be the database total: %+v", view)
	}
	if _, err := svc.RetainedInteractions(context.Background(), "missing", 0, 20); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("unknown run: %v", err)
	}
}
