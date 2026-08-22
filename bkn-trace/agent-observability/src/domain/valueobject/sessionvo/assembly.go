// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import "time"

type AssemblyRevision struct {
	ID                        string         `json:"revision_id"`
	RevisionNo                uint64         `json:"revision_no"`
	ParentRevisionID          string         `json:"parent_revision_id,omitempty"`
	InteractionID             string         `json:"interaction_id"`
	CompletionManifestVersion string         `json:"completion_manifest_version"`
	IncludedReceiptIDs        []string       `json:"included_receipt_ids"`
	IncludedEventIDs          []string       `json:"included_event_ids"`
	ArtifactManifestHash      string         `json:"artifact_manifest_hash"`
	Completeness              EvidenceStatus `json:"assembly_completeness"`
	PartialReasons            []string       `json:"partial_reasons,omitempty"`
	Trigger                   string         `json:"trigger"`
	CreatedAt                 time.Time      `json:"created_at"`
}
