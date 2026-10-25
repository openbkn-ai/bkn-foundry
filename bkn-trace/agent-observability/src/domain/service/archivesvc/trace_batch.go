// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package archivesvc

import (
	"context"
	"errors"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

const (
	MaxTraceArchiveCandidates     = 100
	MaxTraceArchiveCandidateBytes = 8 << 20
	MaxTraceArchiveBatchBytes     = 16 << 20
)

var ErrArchivePayloadTooLarge = errors.New("trace archive payload exceeds its byte budget")

// FreezeBatch enriches only a bounded SQL batch, one interaction at a time.
// The existing workflow verifies and cleans each batch before asking for more.
func (source TraceBundleSource) FreezeBatch(ctx context.Context, kind observabilityvo.ArchiveKind, window observabilityvo.ArchiveRange) (Batch, error) {
	var batch Batch
	var err error
	if bounded, ok := source.Core.(BatchSource); ok {
		batch, err = bounded.FreezeBatch(ctx, kind, window)
	} else {
		batch.Candidates, err = source.Core.Freeze(ctx, kind, window)
		batch.EligibleCount = len(batch.Candidates)
	}
	if err != nil {
		return Batch{}, err
	}
	if len(batch.Candidates) > MaxTraceArchiveCandidates || batch.EligibleCount < len(batch.Candidates) {
		return Batch{}, ErrIncompleteCandidates
	}
	result := make([]Candidate, 0, len(batch.Candidates))
	size := 0
	for _, candidate := range batch.Candidates {
		if err := ctx.Err(); err != nil {
			return Batch{}, err
		}
		if len(candidate.Payload) > MaxTraceArchiveCandidateBytes {
			return Batch{}, ErrArchivePayloadTooLarge
		}
		if source.Technical != nil {
			enriched, err := source.Technical.Enrich(ctx, []Candidate{candidate})
			if err != nil {
				return Batch{}, err
			}
			if len(enriched) != 1 || enriched[0].ID != candidate.ID || !enriched[0].OccurredAt.Equal(candidate.OccurredAt) {
				return Batch{}, fmt.Errorf("%w: technical enrichment changed candidate identity", ErrIncompleteCandidates)
			}
			candidate = enriched[0]
		}
		if len(candidate.Payload) > MaxTraceArchiveCandidateBytes {
			return Batch{}, ErrArchivePayloadTooLarge
		}
		if size+len(candidate.Payload)+1 > MaxTraceArchiveBatchBytes {
			batch.Batched = true
			break
		}
		size += len(candidate.Payload) + 1
		result = append(result, candidate)
	}
	batch.Candidates = result
	return batch, nil
}
