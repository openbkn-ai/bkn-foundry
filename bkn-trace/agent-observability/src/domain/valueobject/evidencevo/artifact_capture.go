// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencevo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type ArtifactContentState string

const (
	ArtifactContentCaptured      ArtifactContentState = "captured"
	ArtifactContentReferenceOnly ArtifactContentState = "reference_only"
	ArtifactContentMissing       ArtifactContentState = "missing"
	ArtifactContentRejected      ArtifactContentState = "rejected"
	ArtifactContentOverBudget    ArtifactContentState = "over_budget"
	ArtifactContentReadError     ArtifactContentState = "read_error"
	ArtifactContentCanceled      ArtifactContentState = "canceled"
)

// ArtifactContentCapture is an internal read result, not a wire DTO or a full
// evidence snapshot. DeclaredHash is source metadata, not proof of verification.
// CanonicalJSON is present only after a successful hash and size check.
type ArtifactContentCapture struct {
	ArtifactID    string
	DeclaredHash  string
	SnapshotRef   string
	State         ArtifactContentState
	Reason        string
	CanonicalJSON json.RawMessage
}

// CaptureArtifactContent freezes already-read content using the existing artifact
// encoding. It does not normalize the record, resolve references, check interaction
// membership or certify a report's claims. Callers must establish source scope.
// maxCapturedBytes limits retained canonical JSON, not upstream I/O or peak memory:
// the legacy store and JSON encoder may already have loaded/allocated the body.
// A successful check cannot detect precision loss that occurred before the stored
// hash was computed. No source field is repaired or written back here.
func CaptureArtifactContent(artifact EvidenceArtifact, maxCapturedBytes int) ArtifactContentCapture {
	result := ArtifactContentCapture{
		ArtifactID: artifact.ArtifactID, DeclaredHash: artifact.ContentHash,
		SnapshotRef: artifact.SnapshotRef, State: ArtifactContentRejected,
	}
	switch {
	case maxCapturedBytes <= 0:
		result.Reason = "invalid_budget"
		return result
	case !ValidArtifactID(artifact.ArtifactID):
		result.Reason = "invalid_artifact_id"
		return result
	case artifact.SchemaVersion != ArtifactContractVersion:
		result.Reason = "unsupported_schema"
		return result
	case artifact.Content != nil && artifact.SnapshotRef != "":
		result.Reason = "ambiguous_content"
		return result
	case artifact.Content == nil && artifact.SnapshotRef == "":
		result.State = ArtifactContentMissing
		return result
	case !artifactHashPattern.MatchString(artifact.ContentHash):
		result.Reason = "invalid_hash"
		return result
	}
	if artifact.Content == nil {
		if !opaqueArtifactLocationPattern.MatchString(artifact.SnapshotRef) {
			result.Reason = "invalid_reference"
			return result
		}
		result.State = ArtifactContentReferenceOnly
		return result
	}
	canonical, err := marshalCanonicalArtifactContent(artifact.Content)
	if err != nil {
		result.Reason = "invalid_content"
		return result
	}
	if len(canonical) > maxCapturedBytes {
		result.State = ArtifactContentOverBudget
		return result
	}
	sum := sha256.Sum256(canonical)
	if "sha256:"+hex.EncodeToString(sum[:]) != artifact.ContentHash {
		result.Reason = "hash_mismatch"
		return result
	}
	result.State = ArtifactContentCaptured
	result.CanonicalJSON = canonical
	return result
}
