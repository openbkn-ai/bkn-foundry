// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package evidencesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/archivesvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"io"
	"strings"
	"testing"
)

type archiveRevisionStore map[string]string

func (s archiveRevisionStore) Read(_ context.Context, k string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s[k])), nil
}
func TestReadArchivedRevision(t *testing.T) {
	for _, name := range []string{"valid", "legacy", "wrong_scope", "duplicate", "corrupt_seal", "missing_revision"} {
		t.Run(name, func(t *testing.T) {
			s := revisionInputFixture()
			raw, hash, err := EncodeRevisionInput(s, 100, 100000)
			if err != nil {
				t.Fatal(err)
			}
			input := sessionvo.SealedRevisionInput{RevisionID: "r", InteractionID: "i", Hash: hash, Package: raw}
			if name == "wrong_scope" {
				input.InteractionID = "other"
			}
			if name == "corrupt_seal" {
				input.Package = []byte(`{}`)
			}
			inputs := []sessionvo.SealedRevisionInput{input}
			if name == "duplicate" {
				inputs = append(inputs, input)
			}
			value := map[string]any{"interaction": s.Interaction, "revision_evidence": map[string]any{"revisions": s.Revisions, "inputs": inputs}}
			if name == "legacy" {
				delete(value, "revision_evidence")
			}
			body, _ := json.Marshal(value)
			sum := sha256.Sum256(body)
			manifest, _ := json.Marshal(map[string]any{"archive_job_id": "job", "archive_kind": "trace", "candidate_count": 1, "data_key": "data", "sha256": hex.EncodeToString(sum[:])})
			job := archivesvc.Job{ID: "job", Kind: observabilityvo.ArchiveKindTrace, Status: observabilityvo.ArchiveStatusCompleted, ManifestRef: "manifest", CandidateCount: 1, CandidateIDs: []string{"i"}}
			rev := "r"
			if name == "missing_revision" {
				rev = "absent"
			}
			got, h, err := ReadArchivedRevision(context.Background(), archiveRevisionStore{"manifest": string(manifest), "data": string(body)}, job, "i", rev, 100, 100000)
			if name == "valid" {
				if err != nil || h != hash || got.Revisions[0].Completeness != sessionvo.EvidencePartial {
					t.Fatalf("read: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
}
