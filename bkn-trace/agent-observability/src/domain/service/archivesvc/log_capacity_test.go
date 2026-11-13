// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"strings"
	"testing"
	"time"
)

func logCapacityFixture(n, size int) Job {
	now := time.Now().UTC()
	candidates := make([]Candidate, n)
	for i := range candidates {
		raw, _ := json.Marshal(strings.Repeat("x", size))
		candidates[i] = Candidate{ID: string(rune('a' + i)), OccurredAt: now, Payload: raw}
	}
	job := newJob(observabilityvo.ArchiveKindLog, observabilityvo.ArchiveRange{To: now}, candidates, now)
	job.BatchProgress = &BatchProgress{EligibleAtSelection: n}
	return job
}
func TestLogCheckpointFitsCountPayloadAndBase64Capacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits LogLimits
		wire   int64
		want   int
	}{
		{"record count", LogLimits{1, 10000}, 1 << 20, 1},
		{"raw JSONL bytes", LogLimits{10000, 1024}, 1 << 20, 1},
		{"Base64 and SQL envelope", LogLimits{10000, 10000}, 3000, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job, err := FitLogCheckpoint(logCapacityFixture(3, 700), tc.limits, tc.wire)
			if err != nil || job.CandidateCount != tc.want || !job.BatchProgress.CheckpointPrepared || job.BatchProgress.RemainingAtSelection != 3-tc.want {
				t.Fatalf("fit=%+v err=%v", job, err)
			}
			if job.BatchProgress.VerifiedCount != 0 || job.ManifestRef != "" || job.Status != observabilityvo.ArchiveStatusFailed {
				t.Fatal("preparation incorrectly authorized cleanup")
			}
		})
	}
}
func TestSingleLogTooLargeRetainsIdentityAndExactBoundary(t *testing.T) {
	job := logCapacityFixture(1, 1021) // JSON quotes + JSONL newline = 1024.
	if _, err := FitLogCheckpoint(job, LogLimits{1, 1024}, 1<<20); err != nil {
		t.Fatal(err)
	}
	job = logCapacityFixture(1, 1022)
	_, err := FitLogCheckpoint(job, LogLimits{1, 1024}, 1<<20)
	var detail *Failure
	if !errors.As(err, &detail) || detail.CandidateID != "a" || detail.Stage != "payload_bytes" || detail.Actual != 1025 || detail.Limit != 1024 {
		t.Fatalf("bad refusal: %v", err)
	}
	if len(job.Candidates) != 1 {
		t.Fatal("source candidate changed")
	}
}
func TestLogLimitsChangeOnlyAtNextBatchBoundary(t *testing.T) {
	s := newBatchSourceFixture(t, 4, 4)
	o := &changingLimitsObjects{batchObjectFixture: batchObjectFixture{s: s}}
	limits := LogLimits{MaxCandidates: 2, MaxPayloadBytes: 1024}
	o.change = func() { limits.MaxCandidates = 1 }
	svc := New(s.store, s, o, Options{LogLimits: func(context.Context) (LogLimits, error) { return limits, nil }})
	job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if err != nil || len(s.rows) != 0 || o.writes != 3 || job.BatchProgress.CleanedInRun != 4 || !job.BatchProgress.RunComplete {
		t.Fatalf("dynamic limits: %+v %v", job, err)
	}
	jobs := s.store.List(observabilityvo.ArchiveKindLog, 10)
	counts := []int{}
	for _, j := range jobs {
		counts = append(counts, j.CandidateCount)
	}
	if len(counts) != 3 || counts[0] != 1 || counts[1] != 1 || counts[2] != 2 {
		t.Fatalf("frozen batch changed: %v", counts)
	}
}

type changingLimitsObjects struct {
	batchObjectFixture
	change func()
}

func (o *changingLimitsObjects) WriteAndVerify(ctx context.Context, j Job, c []Candidate) (string, error) {
	frozen, ok := o.s.store.Get(j.ID)
	if !ok || len(frozen.Candidates) != len(c) || frozen.Status != observabilityvo.ArchiveStatusFailed {
		o.s.t.Fatal("object upload preceded durable frozen checkpoint")
	}
	ref, err := o.batchObjectFixture.WriteAndVerify(ctx, j, c)
	if o.writes == 1 {
		o.change()
	}
	return ref, err
}

func TestInvalidNextBatchLimitsKeepCompletedPrefix(t *testing.T) {
	s := newBatchSourceFixture(t, 4, 4)
	limits := LogLimits{2, 1024}
	o := &changingLimitsObjects{batchObjectFixture: batchObjectFixture{s: s}, change: func() { limits.MaxCandidates = 0 }}
	svc := New(s.store, s, o, Options{LogLimits: func(context.Context) (LogLimits, error) { return limits, nil }})
	job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
	if !errors.Is(err, ErrInvalidLogLimits) || len(s.rows) != 2 || o.writes != 1 || job.Status != observabilityvo.ArchiveStatusCompleted || job.BatchProgress.RunComplete || job.BatchProgress.CleanedInRun != 2 {
		t.Fatalf("invalid update corrupted progress: %+v %v", job, err)
	}
}

type logCheckpointFaultStore struct {
	*memoryStore
	status observabilityvo.ArchiveStatus
	fail   bool
}

func (s *logCheckpointFaultStore) Update(job Job) error {
	if s.fail && job.Status == s.status {
		s.fail = false
		return errors.New("https://object.invalid/?signed=secret")
	}
	return s.memoryStore.Update(job)
}
func TestPreparedLogCheckpointSurvivesTransitionAndFinalizeFailures(t *testing.T) {
	for _, stage := range []string{"checkpoint", "finalize", "partial-cleanup"} {
		t.Run(stage, func(t *testing.T) {
			source := newBatchSourceFixture(t, 2, 2)
			store := &logCheckpointFaultStore{memoryStore: NewMemoryStore().(*memoryStore), fail: stage != "partial-cleanup", status: observabilityvo.ArchiveStatusCleanupIncomplete}
			if stage == "finalize" {
				store.status = observabilityvo.ArchiveStatusCompleted
			}
			source.store = store
			source.failCleanup = stage == "partial-cleanup"
			objects := &batchObjectFixture{s: source}
			svc := New(store, source, objects, Options{})
			job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
			saved, ok := store.Get(job.ID)
			if !ok || len(saved.Candidates) != 2 || !saved.BatchProgress.CheckpointPrepared || strings.Contains(saved.ErrorMessage, "signed=secret") {
				t.Fatalf("recovery input lost: %+v %v", saved, err)
			}
			if stage == "checkpoint" {
				if err == nil || len(source.rows) != 2 || source.purges != 0 || saved.Status != observabilityvo.ArchiveStatusFailed || saved.ManifestRef == "" || saved.BatchProgress.Failure.Stage != "verified_checkpoint" {
					t.Fatalf("unsafe checkpoint transition: %+v %v", saved, err)
				}
				return
			}
			if stage == "finalize" && err == nil {
				t.Fatal("finalization error hidden")
			}
			if stage == "partial-cleanup" && (err != nil || len(source.rows) != 1) {
				t.Fatal("partial cleanup progress changed")
			}
			source.failCleanup = false
			restarted := New(store, source, objects, Options{})
			recovered, err := restarted.RetryCleanup(context.Background(), job.ID)
			if err != nil || recovered.Status != observabilityvo.ArchiveStatusCompleted || len(source.rows) != 0 || objects.writes != 1 || recovered.BatchProgress.CleanedInRun != 2 {
				t.Fatalf("recovery not idempotent: %+v %v", recovered, err)
			}
		})
	}
}

type signedErrorObjects struct{}

func (signedErrorObjects) WriteAndVerify(context.Context, Job, []Candidate) (string, error) {
	return "", errors.New("https://object.invalid/?signed=secret")
}
func TestLogUploadFailureKeepsFrozenInputAndScrubsSignedURL(t *testing.T) {
	source := newBatchSourceFixture(t, 2, 2)
	job, err := New(source.store, source, signedErrorObjects{}, Options{}).Create(context.Background(), observabilityvo.ArchiveKindLog)
	saved, ok := source.store.Get(job.ID)
	if err == nil || !ok || len(saved.Candidates) != 2 || len(source.rows) != 2 || source.purges != 0 || saved.BatchProgress.Failure.Stage != "upload_verify" || strings.Contains(saved.ErrorMessage, "signed=secret") {
		t.Fatalf("unsafe upload failure: %+v %v", saved, err)
	}
}

func TestPayloadBudgetHotReloadKeepsFrozenSnapshot(t *testing.T) {
	for _, grow := range []bool{false, true} {
		s := newBatchSourceFixture(t, 3, 3)
		for i := range s.rows {
			raw, _ := json.Marshal(strings.Repeat("x", 700))
			s.rows[i].Payload = raw
		}
		first, next := 2048, 1024
		wantFirst, wantLast := 2, 1
		if grow {
			first, next = 1024, 2048
			wantFirst, wantLast = 1, 2
		}
		limits := LogLimits{10000, first}
		objects := &changingLimitsObjects{batchObjectFixture: batchObjectFixture{s: s}, change: func() { limits.MaxPayloadBytes = next }}
		svc := New(s.store, s, objects, Options{LogLimits: func(context.Context) (LogLimits, error) { return limits, nil }})
		last, err := svc.Create(context.Background(), observabilityvo.ArchiveKindLog)
		jobs := s.store.List(observabilityvo.ArchiveKindLog, 10)
		if err != nil || len(jobs) != 2 || last.CandidateCount != wantLast || jobs[1].CandidateCount != wantFirst || jobs[1].BatchProgress.LogLimits.MaxPayloadBytes != first || last.BatchProgress.LogLimits.MaxPayloadBytes != next {
			t.Fatalf("payload hot reload changed frozen contract: grow=%v jobs=%+v err=%v", grow, jobs, err)
		}
	}
}
