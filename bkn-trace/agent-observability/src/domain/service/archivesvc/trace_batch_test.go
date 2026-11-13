// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestTraceArchiveProcessesOnlyVerifiedBatches(t *testing.T) {
	s := newBatchSourceFixture(t, 3, 2)
	o := &batchObjectFixture{s: s}
	svc := New(s.store, TraceBundleSource{Core: s}, o, Options{})
	last, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || len(s.rows) != 0 || o.writes != 2 || s.purges != 2 || last.BatchProgress.CleanedInRun != 3 || !last.BatchProgress.RunComplete {
		t.Fatalf("trace batches did not finish: %+v %v", last, err)
	}
	if last.BatchProgress.BatchNumber != 2 {
		t.Fatal("missing trace batch progress")
	}
}

func TestTraceArchiveEnrichmentByteBoundary(t *testing.T) {
	s := newBatchSourceFixture(t, 3, 3)
	technical := &sizedTechnicalArchive{payload: bytes.Repeat([]byte{'x'}, MaxTraceArchiveBatchBytes/2-1)}
	batch, err := (TraceBundleSource{Core: s, Technical: technical}).FreezeBatch(context.Background(), observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{})
	if err != nil || len(batch.Candidates) != 2 || batch.EligibleCount != 3 || !batch.Batched || technical.largestCall != 1 {
		t.Fatalf("unbounded enrichment: count=%d batch=%+v err=%v", len(batch.Candidates), batch, err)
	}
	encoded, err := json.Marshal(batch.Candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) >= (16<<20)-(64<<10) {
		t.Fatalf("batch recovery encoding leaves no default database packet headroom: %d", len(encoded))
	}
}

func TestOversizedTraceArchiveNeverWritesOrPurges(t *testing.T) {
	s := newBatchSourceFixture(t, 1, 1)
	technical := &sizedTechnicalArchive{payload: make([]byte, MaxTraceArchiveCandidateBytes+1)}
	o := &fakeObjectStore{}
	svc := New(s.store, TraceBundleSource{Core: s, Technical: technical}, o, Options{})
	_, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if !errors.Is(err, ErrArchivePayloadTooLarge) || o.verified || s.purges != 0 {
		t.Fatalf("oversized trace reached cleanup: %v", err)
	}
}

type sizedTechnicalArchive struct {
	payload     []byte
	largestCall int
}

func (s *sizedTechnicalArchive) Enrich(_ context.Context, c []Candidate) ([]Candidate, error) {
	s.largestCall = max(s.largestCall, len(c))
	out := append([]Candidate(nil), c...)
	for i := range out {
		out[i].Payload = s.payload
	}
	return out, nil
}
func (s *sizedTechnicalArchive) Purge(context.Context, []Candidate) error { return nil }

// A large candidate must not discard the verified prefix or silently skip the
// suffix. A new Service models process restart over the same durable store.
func TestTraceArchivePersistsBlockageAfterSafePrefix(t *testing.T) {
	for _, phase := range []string{"core", "enrichment", "response"} {
		t.Run(phase, func(t *testing.T) {
			s := newBatchSourceFixture(t, 3, 3)
			blockedID := s.rows[1].ID
			technical := &selectiveOversizedArchive{id: blockedID, phase: phase}
			if phase == "core" {
				s.rows[1].Payload = make([]byte, MaxTraceArchiveCandidateBytes+1)
			}
			o := &batchObjectFixture{s: s}
			source := TraceBundleSource{Core: s, Technical: technical}
			svc := New(s.store, source, o, Options{})
			job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
			var budget *PayloadBudgetError
			if !errors.As(err, &budget) || budget.InteractionID != blockedID {
				t.Fatalf("missing diagnostic: %v", err)
			}
			saved, ok := s.store.Get(job.ID)
			if !ok || saved.Status != observabilityvo.ArchiveStatusFailed || saved.ManifestRef != "" || len(saved.Candidates) != 0 || saved.CandidateCount != 0 || saved.BatchProgress.Blocked == nil || saved.BatchProgress.RunComplete || saved.BatchProgress.CleanedInRun != 1 || saved.BatchProgress.RemainingAtSelection != 2 || saved.BatchProgress.VerifiedCount != 0 {
				t.Fatalf("invalid blocked state: %+v", saved)
			}
			if len(s.rows) != 2 || s.rows[0].ID != blockedID || o.writes != 1 || s.purges != 1 {
				t.Fatal("prefix lost or suffix skipped")
			}
			restarted := New(s.store, source, o, Options{})
			if _, err = restarted.RetryCleanup(context.Background(), job.ID); err == nil {
				t.Fatal("unverified blocked job allowed cleanup")
			}
			next, err := restarted.Create(context.Background(), observabilityvo.ArchiveKindTrace)
			if !errors.As(err, &budget) || next.BatchProgress.RunComplete || o.writes != 1 || s.purges != 1 || len(s.rows) != 2 {
				t.Fatalf("restart bypassed blockage: %v", err)
			}
		})
	}
}

type selectiveOversizedArchive struct{ id, phase string }

func (s *selectiveOversizedArchive) Enrich(_ context.Context, c []Candidate) ([]Candidate, error) {
	out := append([]Candidate(nil), c...)
	if out[0].ID == s.id {
		if s.phase == "response" {
			return nil, &PayloadBudgetError{InteractionID: s.id, Stage: "technical_response", Actual: MaxTraceArchiveResponseBytes + 1, Limit: MaxTraceArchiveResponseBytes, Unit: "bytes", AtLeast: true}
		}
		out[0].Payload = make([]byte, MaxTraceArchiveCandidateBytes+1)
	}
	return out, nil
}
func (*selectiveOversizedArchive) Purge(context.Context, []Candidate) error { return nil }

func TestTraceArchiveDoesNotCommitPrefixOnNonBudgetErrors(t *testing.T) {
	for _, failure := range []error{context.Canceled, ErrIncompleteCandidates, errors.New("dependency unavailable")} {
		s := newBatchSourceFixture(t, 3, 3)
		o := &batchObjectFixture{s: s}
		technical := &failingTechnicalArchive{id: s.rows[1].ID, err: failure}
		svc := New(s.store, TraceBundleSource{Core: s, Technical: technical}, o, Options{})
		_, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
		if !errors.Is(err, failure) || o.writes != 0 || s.purges != 0 || len(s.rows) != 3 {
			t.Fatalf("non-budget error committed partial batch: %v", err)
		}
	}
}

type failingTechnicalArchive struct {
	id  string
	err error
}

func (s *failingTechnicalArchive) Enrich(_ context.Context, c []Candidate) ([]Candidate, error) {
	if c[0].ID == s.id {
		return nil, s.err
	}
	return c, nil
}
func (*failingTechnicalArchive) Purge(context.Context, []Candidate) error { return nil }

type failedBlockageStore struct{ Store }

func (s failedBlockageStore) Create(Job) error { return errors.New("checkpoint unavailable") }
func TestTraceArchiveDoesNotClaimDurableBlockageIfStoreFails(t *testing.T) {
	s := newBatchSourceFixture(t, 1, 1)
	s.rows[0].Payload = make([]byte, MaxTraceArchiveCandidateBytes+1)
	o := &fakeObjectStore{}
	svc := New(failedBlockageStore{s.store}, TraceBundleSource{Core: s}, o, Options{})
	job, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if err == nil || errors.Is(err, ErrArchivePayloadTooLarge) || job.ID != "" || o.verified || s.purges != 0 {
		t.Fatalf("false durable blockage: %+v %v", job, err)
	}
}

func TestCleanupRetryStopsAtDurableBlockageInSameRun(t *testing.T) {
	s := newBatchSourceFixture(t, 3, 3)
	s.rows[1].Payload = make([]byte, MaxTraceArchiveCandidateBytes+1)
	s.failCleanup = true
	o := &batchObjectFixture{s: s}
	svc := New(s.store, TraceBundleSource{Core: s}, o, Options{})
	first, err := svc.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || first.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
		t.Fatalf("missing interrupted prefix: %+v %v", first, err)
	}
	s.failCleanup = false
	blocked, err := svc.RetryCleanup(context.Background(), first.ID)
	if !errors.Is(err, ErrArchivePayloadTooLarge) || blocked.BatchProgress.Blocked == nil || blocked.BatchProgress.RunID != first.BatchProgress.RunID || blocked.BatchProgress.BatchNumber != 2 || blocked.BatchProgress.CleanedInRun != 1 || blocked.BatchProgress.RunComplete || o.writes != 1 || len(s.rows) != 2 {
		t.Fatalf("cleanup continuation lost blocker or progress: %+v %v", blocked, err)
	}
}
