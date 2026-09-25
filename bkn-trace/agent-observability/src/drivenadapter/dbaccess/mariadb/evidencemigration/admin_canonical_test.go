// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import "testing"

func TestManifestEntryQuoteEscapesJSONWithoutEscapingUTF8(t *testing.T) {
	got, err := quote(nil, "quote:\" slash:\\ control:\n 中文")
	if err != nil {
		t.Fatal(err)
	}
	const want = "\"quote:\\\" slash:\\\\ control:\\n 中文\""
	if string(got) != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}

func TestManifestEntryQuoteUsesJSONControlEscapes(t *testing.T) {
	got, err := quote(nil, "\x01\b\t\n\f\r")
	if err != nil {
		t.Fatal(err)
	}
	const want = "\"\\u0001\\b\\t\\n\\f\\r\""
	if string(got) != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}

func TestEntriesDigestMatchesC1Golden(t *testing.T) {
	entry := frozenEntry{
		Classification: "publish", ClassificationReason: "pending",
		EventID: "evt-migration-active-manifest", ManifestID: "mig-openbkn-020-golden-001",
		PayloadHash:   "1d54498ae8e7e7335c9841bdc2c664744369cf22ece4ad5b2434d3066e5a391c",
		ProducerEpoch: "3", ProducerID: "bkn-backend", ProducerSequence: "101",
		ProducerStreamID: "bkn-backend", SourcePrimaryKey: "1001", SourceService: "bkn-backend",
		SourceStatus: "pending", SourceTable: "bkn_backend_trace_outbox",
	}
	got, err := entriesDigest([]frozenEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	const want = "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995"
	if got != want {
		t.Fatalf("C1 entries digest = %s, want %s", got, want)
	}
}

func TestEntriesDigestSortsFrozenSourceCursor(t *testing.T) {
	first := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "2", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	second := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "ontology-query", SourceStatus: "dlq", SourceTable: "ontology_query_trace_outbox"}
	forward, err := entriesDigest([]frozenEntry{first, second})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := entriesDigest([]frozenEntry{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if forward != reversed {
		t.Fatalf("digest must be independent of input order: %s != %s", forward, reversed)
	}
}

func TestCoverageGapCanonicalEntryKeepsNullableIdentityAsNull(t *testing.T) {
	entry := frozenEntry{Classification: "coverage_gap", ClassificationReason: "bad_payload", ManifestID: "mig-1", SourcePrimaryKey: "1", SourceService: "bkn-backend", SourceStatus: "dlq", SourceTable: "bkn_backend_trace_outbox"}
	document, err := canonicalEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"event_id", "payload_hash", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence"} {
		value, found := document[field]
		if !found || value != nil {
			t.Fatalf("%s must be explicit null, got present=%v value=%#v", field, found, value)
		}
	}
	if len(document) != 13 {
		t.Fatalf("C1 immutable field count = %d, want 13", len(document))
	}
}
