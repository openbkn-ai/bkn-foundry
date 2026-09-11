// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"io"
)

var ErrArchivedRevisionUnavailable = errors.New("archive has no sealed input for requested revision")

// ReadArchivedRevision requires a trusted existing archive Job; it does not scan
// job history or infer archival from a missing hot row. Bundle and seal hashes
// are verified independently. Legacy rows are never converted into fake seals.
func ReadArchivedRevision(ctx context.Context, store archivesvc.ReadStore, job archivesvc.Job, interactionID, revisionID string, maxRecords, maxBytes int) (sessionvo.EvidenceSnapshot, string, error) {
	fail := func(err error) (sessionvo.EvidenceSnapshot, string, error) {
		return sessionvo.EvidenceSnapshot{}, "", err
	}
	if job.Kind != observabilityvo.ArchiveKindTrace || interactionID == "" || revisionID == "" || maxRecords <= 0 || len(job.CandidateIDs) > maxRecords {
		return fail(errors.New("invalid archived revision request"))
	}
	members := map[string]bool{}
	for _, id := range job.CandidateIDs {
		if id == "" || members[id] {
			return fail(errors.New("invalid archive candidate identities"))
		}
		members[id] = true
	}
	if !members[interactionID] {
		return fail(ErrArchivedRevisionUnavailable)
	}
	body, err := archivesvc.ReadVerifiedBundle(ctx, store, job, maxBytes)
	if err != nil {
		return fail(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	seen := map[string]bool{}
	var selected *sessionvo.SealedRevisionInput
	var declared *sessionvo.AssemblyRevision
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		var row struct {
			Interaction sessionvo.EvidenceInteraction `json:"interaction"`
			Evidence    *struct {
				Revisions []sessionvo.AssemblyRevision    `json:"revisions"`
				Inputs    []sessionvo.SealedRevisionInput `json:"inputs"`
			} `json:"revision_evidence"`
		}
		err := decoder.Decode(&row)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
		id := row.Interaction.ID
		if !members[id] || seen[id] {
			return fail(errors.New("archive candidate scope or duplicate mismatch"))
		}
		seen[id] = true
		if id != interactionID || row.Evidence == nil {
			continue
		}
		if len(row.Evidence.Inputs) > maxRecords || len(row.Evidence.Revisions) > maxRecords {
			return fail(errors.New("archive revision record budget exceeded"))
		}
		for _, r := range row.Evidence.Revisions {
			if r.ID == revisionID {
				if declared != nil || r.InteractionID != id {
					return fail(errors.New("archive revision identity conflict"))
				}
				v := r
				declared = &v
			}
		}
		for _, input := range row.Evidence.Inputs {
			if input.RevisionID == revisionID {
				if selected != nil || input.InteractionID != id {
					return fail(errors.New("archive seal identity conflict"))
				}
				v := input
				selected = &v
			}
		}
	}
	if len(seen) != len(members) {
		return fail(errors.New("archive candidate count mismatch"))
	}
	if selected == nil || declared == nil {
		return fail(ErrArchivedRevisionUnavailable)
	}
	snapshot, err := DecodeRevisionInput(selected.Package, selected.Hash, interactionID, revisionID, maxRecords, maxBytes)
	if err != nil {
		return fail(err)
	}
	want, _ := json.Marshal(declared)
	got, _ := json.Marshal(snapshot.Revisions[0])
	if !bytes.Equal(want, got) {
		return fail(errors.New("archive revision declaration differs from seal"))
	}
	return snapshot, selected.Hash, nil
}
