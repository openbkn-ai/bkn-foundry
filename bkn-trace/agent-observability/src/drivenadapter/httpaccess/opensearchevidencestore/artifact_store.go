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

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/infra/opensearch"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
)

const artifactIndexMapping = `{"settings":{"index.mapping.total_fields.limit":200},"mappings":{"dynamic":false,"properties":{"artifact_id":{"type":"keyword"},"artifact_type":{"type":"keyword"},"trace_id":{"type":"keyword"},"interaction_id":{"type":"keyword"},"operation_id":{"type":"keyword"},"claim_id":{"type":"keyword"},"source_ref":{"type":"keyword"},"business_refs":{"type":"keyword"},"content_type":{"type":"keyword"},"schema_version":{"type":"keyword"},"observed_at":{"type":"date"},"as_of":{"type":"date"},"source_version":{"type":"keyword"},"content_hash":{"type":"keyword"},"content_json":{"type":"keyword","index":false,"doc_values":false},"snapshot_ref":{"type":"keyword"},"effective_subject_id":{"type":"keyword"},"application_principal_id":{"type":"keyword"},"knowledge_network_ids":{"type":"keyword"},"initiator":{"type":"keyword"},"agent_or_app":{"type":"keyword"},"bkn":{"properties":{"tenant":{"properties":{"id":{"type":"keyword"}}},"account":{"properties":{"id":{"type":"keyword"},"type":{"type":"keyword"}}},"request":{"properties":{"id":{"type":"keyword"}}}}}}}}`

type artifactDocument struct {
	ArtifactID             string                  `json:"artifact_id"`
	ArtifactType           evidencevo.ArtifactType `json:"artifact_type"`
	RequestID              string                  `json:"bkn.request.id"`
	TraceID                string                  `json:"trace_id,omitempty"`
	InteractionID          string                  `json:"interaction_id,omitempty"`
	OperationID            string                  `json:"operation_id,omitempty"`
	ClaimID                string                  `json:"claim_id,omitempty"`
	SourceRef              string                  `json:"source_ref,omitempty"`
	BusinessRefs           []string                `json:"business_refs,omitempty"`
	ContentType            string                  `json:"content_type"`
	SchemaVersion          string                  `json:"schema_version"`
	ObservedAt             string                  `json:"observed_at"`
	AsOf                   string                  `json:"as_of,omitempty"`
	SourceVersion          string                  `json:"source_version,omitempty"`
	ContentHash            string                  `json:"content_hash"`
	ContentJSON            string                  `json:"content_json,omitempty"`
	SnapshotRef            string                  `json:"snapshot_ref,omitempty"`
	TenantID               string                  `json:"bkn.tenant.id,omitempty"`
	AccountID              string                  `json:"bkn.account.id"`
	AccountType            string                  `json:"bkn.account.type"`
	EffectiveSubjectID     string                  `json:"effective_subject_id,omitempty"`
	ApplicationPrincipalID string                  `json:"application_principal_id,omitempty"`
	KnowledgeNetworkIDs    []string                `json:"knowledge_network_ids,omitempty"`
	Initiator              string                  `json:"initiator,omitempty"`
	AgentOrApp             string                  `json:"agent_or_app,omitempty"`
}

func (s *Store) StoreArtifact(ctx context.Context, artifact evidencevo.EvidenceArtifact) (bool, error) {
	if err := s.ensureArtifactIndex(ctx); err != nil {
		return false, err
	}
	document, err := toArtifactDocument(artifact)
	if err != nil {
		return false, err
	}
	body, err := json.Marshal(document)
	if err != nil {
		return false, fmt.Errorf("marshal evidence artifact: %w", err)
	}
	_, err = s.client.CreateDocument(ctx, s.artifactIndex(), artifact.ArtifactID, body)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, opensearch.ErrVersionConflict) {
		return false, err
	}
	stored, getErr := s.client.GetDocument(ctx, s.artifactIndex(), artifact.ArtifactID)
	if getErr != nil {
		return false, getErr
	}
	existing, err := decodeArtifactDocument(stored.Source)
	if err != nil {
		return false, fmt.Errorf("decode evidence artifact: %w", err)
	}
	existingFingerprint, err := evidencevo.ArtifactFingerprint(existing)
	if err != nil {
		return false, err
	}
	incomingFingerprint, err := evidencevo.ArtifactFingerprint(artifact)
	if err != nil {
		return false, err
	}
	if existingFingerprint != incomingFingerprint {
		return false, iartifactstore.ErrArtifactIDConflict
	}
	return false, nil
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string, scope evidencevo.QueryScope) (evidencevo.EvidenceArtifact, bool, error) {
	if err := s.ensureArtifactIndex(ctx); err != nil {
		return evidencevo.EvidenceArtifact{}, false, err
	}
	stored, err := s.client.GetDocument(ctx, s.artifactIndex(), artifactID)
	if errors.Is(err, opensearch.ErrDocumentNotFound) {
		return evidencevo.EvidenceArtifact{}, false, nil
	}
	if err != nil {
		return evidencevo.EvidenceArtifact{}, false, err
	}
	artifact, err := decodeArtifactDocument(stored.Source)
	if err != nil {
		return evidencevo.EvidenceArtifact{}, false, fmt.Errorf("decode evidence artifact: %w", err)
	}
	if !evidencevo.MatchesArtifactScope(artifact, scope) {
		return evidencevo.EvidenceArtifact{}, false, nil
	}
	return artifact, true, nil
}

func (s *Store) ListArtifactsByRequestID(ctx context.Context, requestID string, options iartifactstore.QueryOptions) (iartifactstore.QueryResult, error) {
	if err := s.ensureArtifactIndex(ctx); err != nil {
		return iartifactstore.QueryResult{}, err
	}
	scope := options.Scope
	limit := options.Limit
	if limit <= 0 || limit > iartifactstore.MaxArtifactQueryLimit {
		limit = iartifactstore.MaxArtifactQueryLimit
	}
	must := []map[string]any{{"bool": exactTermQuery("bkn.request.id", requestID)}}
	must = append(must, scopeCandidateMust(scope)...)
	query, err := json.Marshal(map[string]any{
		"size":  limit + 1,
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"sort": []map[string]any{
			{"observed_at": map[string]any{"order": "asc"}},
			{"artifact_id": map[string]any{"order": "asc"}},
		},
	})
	if err != nil {
		return iartifactstore.QueryResult{}, fmt.Errorf("marshal artifact search query: %w", err)
	}
	body, err := s.client.Search(ctx, s.artifactIndex(), query)
	if err != nil {
		return iartifactstore.QueryResult{}, err
	}
	var response struct {
		Hits struct {
			Hits []struct {
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return iartifactstore.QueryResult{}, fmt.Errorf("decode artifact search response: %w", err)
	}
	artifacts := make([]evidencevo.EvidenceArtifact, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		artifact, err := decodeArtifactDocument(hit.Source)
		if err != nil {
			return iartifactstore.QueryResult{}, fmt.Errorf("decode artifact search hit: %w", err)
		}
		if artifact.RequestID == requestID && evidencevo.MatchesArtifactScope(artifact, scope) {
			artifacts = append(artifacts, artifact)
		}
	}
	truncated := len(artifacts) > limit
	if truncated {
		artifacts = artifacts[:limit]
	}
	return iartifactstore.QueryResult{Entries: artifacts, Truncated: truncated}, nil
}

func toArtifactDocument(artifact evidencevo.EvidenceArtifact) (artifactDocument, error) {
	document := artifactDocument{
		ArtifactID: artifact.ArtifactID, ArtifactType: artifact.ArtifactType,
		RequestID: artifact.RequestID, TraceID: artifact.TraceID,
		InteractionID: artifact.InteractionID, OperationID: artifact.OperationID, ClaimID: artifact.ClaimID,
		SourceRef: artifact.SourceRef, BusinessRefs: append([]string(nil), artifact.BusinessRefs...),
		ContentType: artifact.ContentType, SchemaVersion: artifact.SchemaVersion,
		ObservedAt: artifact.ObservedAt, AsOf: artifact.AsOf, SourceVersion: artifact.SourceVersion,
		ContentHash: artifact.ContentHash, SnapshotRef: artifact.SnapshotRef,
		TenantID:  artifact.TenantID,
		AccountID: artifact.AccountID, AccountType: artifact.AccountType,
		EffectiveSubjectID: artifact.EffectiveSubjectID, ApplicationPrincipalID: artifact.ApplicationPrincipalID,
		KnowledgeNetworkIDs: append([]string(nil), artifact.KnowledgeNetworkIDs...),
		Initiator:           artifact.Initiator, AgentOrApp: artifact.AgentOrApp,
	}
	if artifact.Content != nil {
		content, err := json.Marshal(artifact.Content)
		if err != nil {
			return artifactDocument{}, fmt.Errorf("marshal artifact content: %w", err)
		}
		document.ContentJSON = string(content)
	}
	return document, nil
}

func decodeArtifactDocument(body []byte) (evidencevo.EvidenceArtifact, error) {
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
		TenantID:  document.TenantID,
		AccountID: document.AccountID, AccountType: document.AccountType,
		EffectiveSubjectID: document.EffectiveSubjectID, ApplicationPrincipalID: document.ApplicationPrincipalID,
		KnowledgeNetworkIDs: append([]string(nil), document.KnowledgeNetworkIDs...),
		Initiator:           document.Initiator, AgentOrApp: document.AgentOrApp,
	}
	if document.ContentJSON != "" {
		if err := json.Unmarshal([]byte(document.ContentJSON), &artifact.Content); err != nil {
			return evidencevo.EvidenceArtifact{}, fmt.Errorf("decode artifact content_json: %w", err)
		}
	}
	if observedAt, ok := evidencevo.CanonicalArtifactTimestamp(artifact.ObservedAt); ok {
		artifact.ObservedAt = observedAt
	}
	if artifact.AsOf != "" {
		if asOf, ok := evidencevo.CanonicalArtifactTimestamp(artifact.AsOf); ok {
			artifact.AsOf = asOf
		}
	}
	return artifact, nil
}

func (s *Store) artifactIndex() string {
	return s.index + "-artifacts"
}

func (s *Store) ensureArtifactIndex(ctx context.Context) error {
	s.artifactEnsureMu.Lock()
	defer s.artifactEnsureMu.Unlock()
	if s.artifactIndexEnsured {
		return nil
	}
	if err := s.client.EnsureIndex(ctx, s.artifactIndex(), []byte(artifactIndexMapping)); err != nil {
		return err
	}
	s.artifactIndexEnsured = true
	return nil
}
