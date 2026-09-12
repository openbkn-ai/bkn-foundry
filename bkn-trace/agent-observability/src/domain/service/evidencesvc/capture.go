// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iartifactstore"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// CaptureLimits bound artifact reads and retained content, not the preceding session snapshot.
type CaptureLimits struct {
	MaxReads         int
	MaxResponseBytes int64
	MaxReadBytes     int64
	MaxContentBytes  int
	MaxCapturedBytes int
}

// ArtifactCaptureSource locates an explicit reference relative to Snapshot.
// OperationID/Attempt identify the referrer, not necessarily the content producer.
type ArtifactCaptureSource struct {
	Pointer     string
	OperationID string
	Attempt     uint32
	RequestID   string
}

type CapturedArtifact struct {
	Reference           string
	Sources             []ArtifactCaptureSource
	ArtifactType        evidencevo.ArtifactType
	SourceRequestID     string
	SourceInteractionID string
	SourceOperationID   string
	Content             evidencevo.ArtifactContentCapture
}

// CaptureManifest is an owned, in-memory build input. It is not persisted and does
// not claim a transaction spanning the session and artifact stores.
type CaptureManifest struct {
	Snapshot       sessionvo.EvidenceSnapshot
	Artifacts      []CapturedArtifact
	PartialReasons []string
	ReadAttempts   int
	ReadBytes      int64
	CapturedBytes  int
}

type CaptureService struct {
	sessions  isessionstore.EvidenceSnapshotReader
	artifacts iartifactstore.CaptureReader
}

func NewCaptureService(sessions isessionstore.EvidenceSnapshotReader, artifacts iartifactstore.CaptureReader) *CaptureService {
	return &CaptureService{sessions: sessions, artifacts: artifacts}
}

// Capture reads an interaction already resolved by the existing provenance entry.
// It must not be wired directly to an unrestricted public ID lookup. The snapshot
// reader owns its consistent copy; artifact reads happen after that read returns.
func (s *CaptureService) Capture(ctx context.Context, interactionID string, scope evidencevo.QueryScope, limits CaptureLimits) (CaptureManifest, bool, error) {
	if limits.MaxReads <= 0 || limits.MaxResponseBytes <= 0 || limits.MaxResponseBytes == math.MaxInt64 || limits.MaxReadBytes <= 0 || limits.MaxContentBytes <= 0 || limits.MaxCapturedBytes <= 0 {
		return CaptureManifest{}, false, iartifactstore.ErrInvalidCaptureBudget
	}
	if err := ctx.Err(); err != nil {
		return CaptureManifest{}, false, err
	}
	snapshot, found, err := s.sessions.ReadEvidenceSnapshot(ctx, interactionID)
	if err != nil || !found {
		return CaptureManifest{}, found, err
	}
	if snapshot.Interaction.ID != interactionID || interactionID == "" {
		return CaptureManifest{}, false, isessionstore.ErrEvidenceIdentityMismatch
	}
	manifest := CaptureManifest{Snapshot: snapshot, Artifacts: []CapturedArtifact{}, PartialReasons: []string{}}
	operations := captureOperationIDs(snapshot)
	manifest.Artifacts = collectCaptureReferences(snapshot, &manifest, operations)
	for i := range manifest.Artifacts {
		entry := &manifest.Artifacts[i]
		id, valid := evidencevo.ArtifactIDFromReference(entry.Reference)
		entry.Content.ArtifactID = id
		switch {
		case ctx.Err() != nil:
			entry.Content.State = evidencevo.ArtifactContentCanceled
			entry.Content.Reason = "canceled"
		case !valid:
			entry.Content.State = evidencevo.ArtifactContentRejected
			entry.Content.Reason = "unsupported_artifact_reference"
		case manifest.ReadAttempts >= limits.MaxReads:
			entry.Content.State = evidencevo.ArtifactContentOverBudget
			entry.Content.Reason = "read_count_limit"
		case limits.MaxReadBytes-manifest.ReadBytes <= 1:
			entry.Content.State = evidencevo.ArtifactContentOverBudget
			entry.Content.Reason = "read_bytes_limit"
		case manifest.CapturedBytes >= limits.MaxCapturedBytes:
			entry.Content.State = evidencevo.ArtifactContentOverBudget
			entry.Content.Reason = "captured_bytes_limit"
		default:
			// Reserve the reader's one-byte overflow probe within the total read budget.
			budget := min(limits.MaxResponseBytes, limits.MaxReadBytes-manifest.ReadBytes-1)
			manifest.ReadAttempts++
			read, readErr := s.artifacts.ReadArtifactForCapture(ctx, id, scope, budget)
			if read.ReadBytes < 0 || read.ReadBytes > budget+1 {
				return manifest, true, errors.New("artifact capture reader violated byte accounting contract")
			}
			manifest.ReadBytes += read.ReadBytes
			switch {
			case ctx.Err() != nil || errors.Is(readErr, context.Canceled) || errors.Is(readErr, context.DeadlineExceeded):
				entry.Content.State = evidencevo.ArtifactContentCanceled
				entry.Content.Reason = "canceled"
			case errors.Is(readErr, iartifactstore.ErrCaptureReadBudget):
				entry.Content.State = evidencevo.ArtifactContentOverBudget
				entry.Content.Reason = "response_bytes_limit"
			case errors.Is(readErr, iartifactstore.ErrCaptureIdentityMismatch):
				entry.Content.State = evidencevo.ArtifactContentRejected
				entry.Content.Reason = "artifact_id_mismatch"
			case readErr != nil:
				entry.Content.State = evidencevo.ArtifactContentReadError
				entry.Content.Reason = "read_error"
			case !read.Found:
				entry.Content.State = evidencevo.ArtifactContentMissing
				entry.Content.Reason = "not_found_or_not_visible"
			default:
				reason := captureSourceMismatch(id, snapshot.Interaction.ID, read.Artifact, entry.Sources, operations)
				if reason != "" {
					entry.Content.State = evidencevo.ArtifactContentRejected
					entry.Content.Reason = reason
				} else {
					entry.ArtifactType = read.Artifact.ArtifactType
					entry.SourceRequestID = read.Artifact.RequestID
					entry.SourceInteractionID = read.Artifact.InteractionID
					entry.SourceOperationID = read.Artifact.OperationID
					remaining := min(limits.MaxContentBytes, limits.MaxCapturedBytes-manifest.CapturedBytes)
					entry.Content = evidencevo.CaptureArtifactContent(read.Artifact, remaining)
					manifest.CapturedBytes += len(entry.Content.CanonicalJSON)
				}
			}
		}
		if entry.Content.State != evidencevo.ArtifactContentCaptured {
			reason := entry.Content.Reason
			if reason == "" {
				reason = string(entry.Content.State)
			}
			manifest.addReason(reason)
		}
	}
	return manifest, true, ctx.Err()
}

func (m *CaptureManifest) addReason(reason string) {
	for _, existing := range m.PartialReasons {
		if existing == reason {
			return
		}
	}
	m.PartialReasons = append(m.PartialReasons, reason)
}

// Referrer identity is not the producer identity. A material result may be reused
// by another operation in the same captured interaction without changing its source.
func captureSourceMismatch(id, interactionID string, a evidencevo.EvidenceArtifact, sources []ArtifactCaptureSource, operations map[string]bool) string {
	if a.ArtifactID != id {
		return "artifact_id_mismatch"
	}
	if a.InteractionID != "" && a.InteractionID != interactionID {
		return "interaction_mismatch"
	}
	if a.OperationID != "" && !operations[a.OperationID] {
		return "operation_not_in_snapshot"
	}
	if a.InteractionID == "" {
		for _, source := range sources {
			if source.RequestID != "" && source.RequestID == a.RequestID {
				return ""
			}
		}
		return "unverified_source_scope"
	}
	return ""
}

func captureOperationIDs(snapshot sessionvo.EvidenceSnapshot) map[string]bool {
	ids := map[string]bool{}
	for _, op := range snapshot.Operations {
		if op.InteractionID == snapshot.Interaction.ID && op.ConversationID == snapshot.Interaction.ConversationID {
			ids[op.ID] = true
		}
	}
	for _, r := range snapshot.Receipts {
		if r.InteractionID == snapshot.Interaction.ID && r.ConversationID == snapshot.Interaction.ConversationID {
			ids[r.OperationID] = true
		}
	}
	for _, f := range snapshot.CallFacts {
		if f.InteractionID == snapshot.Interaction.ID && f.ConversationID == snapshot.Interaction.ConversationID {
			ids[f.OperationID] = true
		}
	}
	return ids
}

func collectCaptureReferences(snapshot sessionvo.EvidenceSnapshot, manifest *CaptureManifest, operations map[string]bool) []CapturedArtifact {
	byRef := map[string][]ArtifactCaptureSource{}
	add := func(ref string, source ArtifactCaptureSource) {
		if ref != "" {
			byRef[ref] = append(byRef[ref], source)
		}
	}
	if closure := snapshot.Interaction.ClosureManifest; closure != nil && closure.AnswerArtifactRef != "" {
		add(closure.AnswerArtifactRef, ArtifactCaptureSource{Pointer: "/interaction/closure_manifest/answer_artifact_ref"})
	} else {
		manifest.addReason("answer_reference_not_recorded")
	}
	for i, r := range snapshot.Receipts {
		if r.InteractionID != snapshot.Interaction.ID || r.ConversationID != snapshot.Interaction.ConversationID {
			manifest.addReason("receipt_scope_mismatch")
			continue
		}
		for j, ref := range r.ArtifactRefs {
			add(ref, ArtifactCaptureSource{Pointer: fmt.Sprintf("/receipts/%d/artifact_refs/%d", i, j), OperationID: r.OperationID, Attempt: r.Attempt, RequestID: r.RequestID})
		}
	}
	for i, f := range snapshot.CallFacts {
		if f.InteractionID != snapshot.Interaction.ID || f.ConversationID != snapshot.Interaction.ConversationID {
			manifest.addReason("call_fact_scope_mismatch")
			continue
		}
		for _, field := range []struct {
			name    string
			payload *sessionvo.PayloadEnvelope
		}{{"input", &f.Input}, {"output", f.Output}, {"error", f.Error}} {
			if field.payload != nil && field.payload.Mode == sessionvo.PayloadReferenced {
				if field.payload.Ref == "" {
					manifest.addReason("payload_reference_missing")
					continue
				}
				add(field.payload.Ref, ArtifactCaptureSource{Pointer: fmt.Sprintf("/call_facts/%d/%s/ref", i, field.name), OperationID: f.OperationID, Attempt: f.Attempt, RequestID: f.RequestID})
			}
		}
	}
	if snapshot.Ledger == nil {
		manifest.addReason("ledger_not_read")
	} else {
		for i, record := range snapshot.Ledger.Events {
			var event ledgervo.Event
			if err := json.Unmarshal(record.Envelope, &event); err != nil {
				manifest.addReason("ledger_metadata_unreadable")
				continue
			}
			if event.EventID != record.EventID || event.InteractionID != snapshot.Interaction.ID || event.ConversationID != snapshot.Interaction.ConversationID {
				manifest.addReason("ledger_scope_mismatch")
				continue
			}
			// A valid ledger row can be the only surviving operation observation.
			// It establishes membership without synthesizing an Operation or Receipt.
			if event.OperationID != "" {
				operations[event.OperationID] = true
			}
			// Read the explicitly typed question reference used by interaction start.
			// Historical producers may not repeat it in artifact_refs.
			if event.EventType == "agent.interaction.started" {
				var envelope struct {
					Payload struct {
						Reference string `json:"question_artifact_ref"`
					} `json:"payload"`
				}
				if json.Unmarshal(event.Envelope, &envelope) == nil && envelope.Payload.Reference != "" {
					add(envelope.Payload.Reference, ArtifactCaptureSource{Pointer: fmt.Sprintf("/ledger/events/%d/envelope/envelope/payload/question_artifact_ref", i), RequestID: event.RequestID})
				}
			}
			// Read explicit ledger artifact_refs only; never recursively scan arbitrary text.
			for j, ref := range event.ArtifactRefs {
				add(ref, ArtifactCaptureSource{Pointer: fmt.Sprintf("/ledger/events/%d/envelope/artifact_refs/%d", i, j), OperationID: event.OperationID, Attempt: event.Attempt, RequestID: event.RequestID})
			}
		}
	}
	refs := make([]string, 0, len(byRef))
	for ref := range byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	entries := make([]CapturedArtifact, 0, len(refs))
	for _, ref := range refs {
		entries = append(entries, CapturedArtifact{Reference: ref, Sources: byRef[ref]})
	}
	return entries
}
