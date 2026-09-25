// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import "testing"

func TestManifestArtifactConvertsOnlyValidFrozenC1Entries(t *testing.T) {
	entry := ManifestArtifactEntry{
		Classification: "coverage_gap", ClassificationReason: "abandoned", ManifestID: "mig-1",
		SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "abandoned", SourceTable: "bkn_backend_trace_outbox",
	}
	frozen := frozenEntry{Classification: entry.Classification, ClassificationReason: entry.ClassificationReason,
		ManifestID: entry.ManifestID, SourcePrimaryKey: entry.SourcePrimaryKey, SourceService: entry.SourceService,
		SourceStatus: entry.SourceStatus, SourceTable: entry.SourceTable}
	digest, err := entriesDigest([]frozenEntry{frozen})
	if err != nil {
		t.Fatal(err)
	}
	artifact := ManifestArtifact{
		ManifestID: "mig-1", ContractSHA: frozenContractSHA, SourceSnapshotAt: "2026-09-25T10:00:00.000Z",
		EntryCount: "1", EntriesDigest: digest, Entries: []ManifestArtifactEntry{entry},
	}
	input, err := artifact.adminInput("owner")
	if err != nil {
		t.Fatal(err)
	}
	if input.ManifestID != artifact.ManifestID || input.EntriesDigest != digest || len(input.Entries) != 1 || input.Entries[0] != frozen {
		t.Fatalf("admin input = %+v", input)
	}
	if input.SourceSnapshotAt != artifact.SourceSnapshotAt {
		t.Fatalf("admin snapshot time = %q, want canonical RFC3339 %q", input.SourceSnapshotAt, artifact.SourceSnapshotAt)
	}
}

func TestManifestArtifactRejectsCountDigestAndClassificationDrift(t *testing.T) {
	entry := ManifestArtifactEntry{
		Classification: "coverage_gap", ClassificationReason: "abandoned", ManifestID: "mig-1",
		SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "abandoned", SourceTable: "bkn_backend_trace_outbox",
	}
	frozen := frozenEntry{Classification: entry.Classification, ClassificationReason: entry.ClassificationReason,
		ManifestID: entry.ManifestID, SourcePrimaryKey: entry.SourcePrimaryKey, SourceService: entry.SourceService,
		SourceStatus: entry.SourceStatus, SourceTable: entry.SourceTable}
	digest, err := entriesDigest([]frozenEntry{frozen})
	if err != nil {
		t.Fatal(err)
	}
	base := ManifestArtifact{
		ManifestID: "mig-1", ContractSHA: frozenContractSHA, SourceSnapshotAt: "2026-09-25T10:00:00.000Z",
		EntryCount: "1", EntriesDigest: digest, Entries: []ManifestArtifactEntry{entry},
	}
	for name, invalid := range map[string]ManifestArtifact{
		"count":  func() ManifestArtifact { value := base; value.EntryCount = "2"; return value }(),
		"digest": func() ManifestArtifact { value := base; value.EntriesDigest = "bad"; return value }(),
		"classification": func() ManifestArtifact {
			value := base
			value.Entries = append([]ManifestArtifactEntry(nil), base.Entries...)
			value.Entries[0].Classification = "publish-anything"
			return value
		}(),
		"manifest id": func() ManifestArtifact {
			value := base
			value.Entries = append([]ManifestArtifactEntry(nil), base.Entries...)
			value.Entries[0].ManifestID = "other"
			return value
		}(),
		"timestamp form": func() ManifestArtifact {
			value := base
			value.SourceSnapshotAt = "2026-09-25T10:00:00+00:00"
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := invalid.adminInput("owner"); err == nil {
				t.Fatal("expected invalid artifact to be rejected")
			}
		})
	}
}

func TestManifestArtifactRejectsClassificationIdentityDriftEvenWithMatchingDigest(t *testing.T) {
	validEntry := frozenEntry{
		Classification: "publish", ClassificationReason: "pending", EventID: "evt-1", ManifestID: "mig-1",
		PayloadHash:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProducerEpoch: "1", ProducerID: "bkn-backend", ProducerSequence: "1",
		ProducerStreamID: "bkn-backend", SourcePrimaryKey: "1", SourceService: "bkn-backend",
		SourceStatus: "pending", SourceTable: "bkn_backend_trace_outbox",
	}
	missingIdentity := validEntry
	missingIdentity.EventID = ""
	digestFor := func(entry frozenEntry) string {
		t.Helper()
		digest, err := entriesDigest([]frozenEntry{entry})
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	for name, entry := range map[string]frozenEntry{
		"publish missing identity": missingIdentity,
		"coverage gap carries identity": func() frozenEntry {
			value := validEntry
			value.Classification = "coverage_gap"
			value.ClassificationReason = "bad_payload"
			return value
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			artifact := ManifestArtifact{
				ManifestID: "mig-1", ContractSHA: frozenContractSHA, SourceSnapshotAt: "2026-09-25T10:00:00.000Z",
				EntryCount: "1", EntriesDigest: digestFor(entry), Entries: []ManifestArtifactEntry{{
					Classification: entry.Classification, ClassificationReason: entry.ClassificationReason,
					EventID: entry.EventID, ManifestID: entry.ManifestID, PayloadHash: entry.PayloadHash,
					ProducerEpoch: entry.ProducerEpoch, ProducerID: entry.ProducerID, ProducerSequence: entry.ProducerSequence,
					ProducerStreamID: entry.ProducerStreamID, SourcePrimaryKey: entry.SourcePrimaryKey,
					SourceService: entry.SourceService, SourceStatus: entry.SourceStatus, SourceTable: entry.SourceTable,
				}},
			}
			if _, err := artifact.adminInput("owner"); err == nil {
				t.Fatal("expected classification identity invariant to be rejected")
			}
		})
	}
}
