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
