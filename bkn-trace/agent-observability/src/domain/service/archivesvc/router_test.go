// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package archivesvc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

func TestTraceBundleOverviewCountsCoreWithoutTechnicalEnrichment(t *testing.T) {
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	core := &countingSource{count: 184}
	technical := &checkingTechnicalStore{}
	service := New(NewMemoryStore(), Router{Trace: TraceBundleSource{Core: core, Technical: technical}}, &fakeObjectStore{}, Options{Now: func() time.Time { return now }})

	overview, err := service.Overview(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil {
		t.Fatalf("trace overview: %v", err)
	}
	if overview.CandidateCount != 184 || core.countCalls != 1 || core.freezeCalls != 0 || technical.enrichCalls != 0 {
		t.Fatalf("overview must count core without materializing traces: overview=%+v core=%+v technical=%+v", overview, core, technical)
	}
}

func TestTraceBundleOverviewRejectsCoreWithoutCandidateCount(t *testing.T) {
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	core := &fakeSource{candidates: []Candidate{{ID: "trace-1"}}}
	technical := &checkingTechnicalStore{}
	service := New(NewMemoryStore(), Router{Trace: TraceBundleSource{Core: core, Technical: technical}}, &fakeObjectStore{}, Options{Now: func() time.Time { return now }})

	_, err := service.Overview(context.Background(), observabilityvo.ArchiveKindTrace)
	if err == nil || !strings.Contains(err.Error(), "candidate count") || technical.enrichCalls != 0 {
		t.Fatalf("overview must reject a trace core without candidate count: err=%v technical=%+v", err, technical)
	}
}

type checkingTechnicalStore struct{ enrichCalls int }

func (store *checkingTechnicalStore) Enrich(_ context.Context, candidates []Candidate) ([]Candidate, error) {
	store.enrichCalls++
	return candidates, nil
}
func (store *checkingTechnicalStore) Purge(_ context.Context, _ []Candidate) error { return nil }

// The workflow must not make an archive job or delete either population when
// technical enrichment cannot prove a complete frozen OpenSearch selection.
func TestTraceArchiveIncompleteTechnicalSelectionDoesNotWriteOrPurge(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	core := &traceArchiveFlowCore{candidates: []Candidate{{ID: "interaction", OccurredAt: now.AddDate(0, 0, -8), Payload: []byte(`{"interaction":{"interaction_id":"interaction"}}`)}}}
	technical := &traceArchiveFlowTechnical{enrichErr: ErrIncompleteCandidates}
	objects := &fakeObjectStore{}
	jobs := NewMemoryStore()
	service := New(jobs, Router{Trace: TraceBundleSource{Core: core, Technical: technical}}, objects, Options{Now: func() time.Time { return now }})
	job, err := service.Create(context.Background(), observabilityvo.ArchiveKindTrace)
	if !errors.Is(err, ErrIncompleteCandidates) || job.ID != "" || objects.verified || core.purges != 0 || technical.purges != 0 || technical.enriches != 1 {
		t.Fatalf("incomplete selection escaped freeze: job=%+v err=%v core=%+v technical=%+v wrote=%v", job, err, core, technical, objects.verified)
	}
	if _, found := jobs.Latest(observabilityvo.ArchiveKindTrace); found {
		t.Fatal("unverified trace selection created a durable job")
	}
}

func TestLegacyTraceCleanupRefusalPreservesFrozenJobAndDownloadAcrossRetry(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	payload := []byte(`{"interaction":{"interaction_id":"legacy-int"},"technical_trace_ids":["trace"],"technical_traces":[]}`)
	candidate := Candidate{ID: "legacy-int", OccurredAt: now.AddDate(0, 0, -8), Payload: payload}
	job := newJob(observabilityvo.ArchiveKindTrace, observabilityvo.ArchiveRange{To: now.AddDate(0, 0, -7)}, []Candidate{candidate}, now)
	job.Status = observabilityvo.ArchiveStatusCleanupIncomplete
	job.ManifestRef = "legacy/manifest.json"
	jobs := NewMemoryStore()
	if err := jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	core := &traceArchiveFlowCore{}
	technical := &traceArchiveFlowTechnical{purgeErr: errors.New("frozen technical document metadata is unavailable")}
	objects := &fakeDownloadObjectStore{objects: archiveFixtureObjects(job)}
	router := Router{Trace: TraceBundleSource{Core: core, Technical: technical}}
	for attempt := 0; attempt < 2; attempt++ {
		// A new Service exercises retry after process-local state has disappeared.
		service := New(jobs, router, objects, Options{Now: func() time.Time { return now }})
		result, err := service.RetryCleanup(context.Background(), job.ID)
		if err != nil || result.Status != observabilityvo.ArchiveStatusCleanupIncomplete {
			t.Fatalf("refusal changed retry contract: %+v %v", result, err)
		}
		restored, found := jobs.Get(job.ID)
		if !found || restored.Status != observabilityvo.ArchiveStatusCleanupIncomplete || restored.ManifestRef != job.ManifestRef || len(restored.Candidates) != 1 || restored.Candidates[0].ID != candidate.ID || !bytes.Equal(restored.Candidates[0].Payload, payload) || restored.CandidateCount != 1 || len(restored.CandidateIDs) != 1 || restored.CandidateIDs[0] != candidate.ID || restored.ErrorMessage != technical.purgeErr.Error() {
			t.Fatalf("legacy recovery data lost: %+v", restored)
		}
		if core.purges != 0 || core.freezes != 0 || technical.enriches != 0 || technical.purges != attempt+1 || objects.verified {
			t.Fatalf("retry refroze/wrote/deleted source: core=%+v technical=%+v", core, technical)
		}
		if _, err := service.Create(context.Background(), observabilityvo.ArchiveKindTrace); !errors.Is(err, ErrCleanupRequired) {
			t.Fatalf("unsafe cleanup job ceased blocking trace archive: %v", err)
		}
	}
	service := New(jobs, router, objects, Options{})
	download, err := service.OpenDownload(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = download.Content.Close() }()
	body, err := io.ReadAll(download.Content)
	if err != nil || !bytes.Equal(body, append(append([]byte(nil), payload...), '\n')) {
		t.Fatalf("legacy verified package became unreadable: %s %v", body, err)
	}
}

type traceArchiveFlowCore struct {
	candidates      []Candidate
	freezes, purges int
}

func (s *traceArchiveFlowCore) Freeze(context.Context, observabilityvo.ArchiveKind, observabilityvo.ArchiveRange) ([]Candidate, error) {
	s.freezes++
	return append([]Candidate(nil), s.candidates...), nil
}
func (s *traceArchiveFlowCore) Purge(context.Context, observabilityvo.ArchiveKind, []Candidate) error {
	s.purges++
	return nil
}

type traceArchiveFlowTechnical struct {
	enrichErr, purgeErr error
	enriches, purges    int
}

func (s *traceArchiveFlowTechnical) Enrich(_ context.Context, candidates []Candidate) ([]Candidate, error) {
	s.enriches++
	return candidates, s.enrichErr
}
func (s *traceArchiveFlowTechnical) Purge(context.Context, []Candidate) error {
	s.purges++
	return s.purgeErr
}
