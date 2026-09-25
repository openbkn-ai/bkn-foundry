// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import "testing"

func TestEntriesDigestMatchesC1Golden(t *testing.T) {
	entry := frozenEntry{
		Classification: "publish", ClassificationReason: "pending",
		EventID: "evt-migration-active-manifest", ManifestID: "mig-openbkn-020-golden-001",
		PayloadHash: "1d54498ae8e7e7335c9841bdc2c664744369cf22ece4ad5b2434d3066e5a391c",
		ProducerEpoch: "3", ProducerID: "bkn-backend", ProducerSequence: "101",
		ProducerStreamID: "bkn-backend", SourcePrimaryKey: "1001", SourceService: "bkn-backend",
		SourceStatus: "pending", SourceTable: "bkn_backend_trace_outbox",
	}
	got, err := entriesDigest([]frozenEntry{entry})
	if err != nil { t.Fatal(err) }
	const want = "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995"
	if got != want { t.Fatalf("C1 entries digest = %s, want %s", got, want) }
}

func TestEntriesDigestSortsFrozenSourceCursor(t *testing.T) {
	first := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "2", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	second := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "ontology-query", SourceStatus: "dlq", SourceTable: "ontology_query_trace_outbox"}
	forward, err := entriesDigest([]frozenEntry{first, second})
	if err != nil { t.Fatal(err) }
	reversed, err := entriesDigest([]frozenEntry{second, first})
	if err != nil { t.Fatal(err) }
	if forward != reversed { t.Fatalf("digest must be independent of input order: %s != %s", forward, reversed) }
}
