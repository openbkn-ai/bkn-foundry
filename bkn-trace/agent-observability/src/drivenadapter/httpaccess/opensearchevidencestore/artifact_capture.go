// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearchevidencestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

var _ iartifactstore.CaptureReader = (*Store)(nil)

// ReadArtifactForCapture bypasses index creation and preserves stored content
// numbers and timestamps. Hash verification belongs to the capture service.
func (s *Store) ReadArtifactForCapture(ctx context.Context, artifactID string, scope evidencevo.QueryScope, maxResponseBytes int64) (iartifactstore.CaptureReadResult, error) {
	if maxResponseBytes <= 0 || maxResponseBytes == math.MaxInt64 {
		return iartifactstore.CaptureReadResult{}, iartifactstore.ErrInvalidCaptureBudget
	}
	if !evidencevo.ValidArtifactID(artifactID) {
		return iartifactstore.CaptureReadResult{}, errors.New("invalid capture artifact ID")
	}
	document, readBytes, err := s.client.GetDocumentBounded(ctx, s.artifactIndex(), artifactID, maxResponseBytes)
	result := iartifactstore.CaptureReadResult{ReadBytes: readBytes}
	if errors.Is(err, opensearch.ErrDocumentNotFound) {
		return result, nil
	}
	if errors.Is(err, opensearch.ErrResponseReadBudget) {
		return result, iartifactstore.ErrCaptureReadBudget
	}
	if err != nil {
		return result, err
	}
	artifact, err := decodeArtifactForCapture(document.Source)
	if err != nil {
		return result, fmt.Errorf("decode capture artifact: %w", err)
	}
	if artifact.ArtifactID != artifactID {
		return result, iartifactstore.ErrCaptureIdentityMismatch
	}
	if !evidencevo.MatchesArtifactScope(artifact, scope) {
		return result, nil
	}
	result.Artifact, result.Found = artifact, true
	return result, nil
}

// Kept separate from the historical decoder: capture must not normalize source
// metadata or round content numbers before checking the recorded content hash.
func decodeArtifactForCapture(body []byte) (evidencevo.EvidenceArtifact, error) {
	var document artifactDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return evidencevo.EvidenceArtifact{}, err
	}
	artifact := evidencevo.EvidenceArtifact{
		ArtifactID: document.ArtifactID, ArtifactType: document.ArtifactType,
		RequestID: document.RequestID, TraceID: document.TraceID,
		InteractionID: document.InteractionID, OperationID: document.OperationID, ClaimID: document.ClaimID,
		SourceRef: document.SourceRef, BusinessRefs: append([]string(nil), document.BusinessRefs...),
		ContentType: document.ContentType, SchemaVersion: document.SchemaVersion,
		ObservedAt: document.ObservedAt, AsOf: document.AsOf, SourceVersion: document.SourceVersion,
		ContentHash: document.ContentHash, SnapshotRef: document.SnapshotRef,
		AccountID: document.AccountID, AccountType: document.AccountType,
		EffectiveSubjectID: document.EffectiveSubjectID, ApplicationPrincipalID: document.ApplicationPrincipalID,
		KnowledgeNetworkIDs: append([]string(nil), document.KnowledgeNetworkIDs...),
		Initiator:           document.Initiator, AgentOrApp: document.AgentOrApp,
	}
	if document.ContentJSON != "" {
		decoder := json.NewDecoder(strings.NewReader(document.ContentJSON))
		decoder.UseNumber()
		if err := decoder.Decode(&artifact.Content); err != nil {
			return evidencevo.EvidenceArtifact{}, fmt.Errorf("decode capture content_json: %w", err)
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return evidencevo.EvidenceArtifact{}, errors.New("capture content_json contains trailing data")
		}
	}
	return artifact, nil
}
