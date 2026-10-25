// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

type acceptedArtifactWriter struct{ service *Service }

func (s *Service) AcceptedArtifactWriter() iartifactstore.Writer { return acceptedArtifactWriter{s} }

// Accepted work retains the same normalization, trace ownership and role checks
// as ingestion, but does not re-evaluate admission policy after its SQL commit.
func (writer acceptedArtifactWriter) StoreArtifact(ctx context.Context, artifact evidencevo.EvidenceArtifact) (bool, error) {
	normalized, validation := evidencevo.NormalizeArtifact(artifact)
	if len(validation) > 0 {
		return false, iartifactstore.ErrInvalidAcceptedArtifact
	}
	s := writer.service
	if s.artifactStore == nil {
		return false, errors.New("artifact store unavailable")
	}
	if normalized.TraceID != "" {
		existing, err := s.store.GetEvidenceHistoryByTraceID(ctx, normalized.TraceID)
		if err != nil {
			return false, err
		}
		for _, trace := range existing {
			if !artifactMatchesTrace(normalized, trace) {
				return false, iartifactstore.ErrInvalidAcceptedArtifact
			}
		}
		if len(validateArtifactRoleAgainstTraces(normalized, existing)) > 0 {
			return false, iartifactstore.ErrInvalidAcceptedArtifact
		}
	}
	return s.artifactStore.StoreArtifact(ctx, normalized)
}
