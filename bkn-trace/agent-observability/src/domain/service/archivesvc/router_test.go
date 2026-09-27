// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package archivesvc

import (
	"context"
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

func TestTraceBundleOverviewFallsBackToCoreFreezeWithoutTechnicalEnrichment(t *testing.T) {
	now := time.Date(2026, time.September, 27, 8, 0, 0, 0, time.UTC)
	core := &fakeSource{candidates: []Candidate{{ID: "trace-1"}}}
	technical := &checkingTechnicalStore{}
	service := New(NewMemoryStore(), Router{Trace: TraceBundleSource{Core: core, Technical: technical}}, &fakeObjectStore{}, Options{Now: func() time.Time { return now }})

	overview, err := service.Overview(context.Background(), observabilityvo.ArchiveKindTrace)
	if err != nil || overview.CandidateCount != 1 || technical.enrichCalls != 0 {
		t.Fatalf("overview must fall back to core freeze without technical enrichment: overview=%+v err=%v technical=%+v", overview, err, technical)
	}
}

type checkingTechnicalStore struct{ enrichCalls int }

func (store *checkingTechnicalStore) Enrich(_ context.Context, candidates []Candidate) ([]Candidate, error) {
	store.enrichCalls++
	return candidates, nil
}
func (store *checkingTechnicalStore) Purge(_ context.Context, _ []Candidate) error { return nil }
