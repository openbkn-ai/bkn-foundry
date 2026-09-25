// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package evidencemigration

import "testing"

func TestClosureDigestMatchesC1Golden(t *testing.T) {
	header := closureHeader{
		ActivatedAt: "2026-09-22T08:30:00.000Z", ClosedAt: "2026-09-22T09:30:00.000Z",
		ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", EntriesDigest: "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995",
		EntryCount: "1", ManifestID: "mig-openbkn-020-golden-001", SourceSnapshotAt: "2026-09-22T08:00:00.000Z",
		TerminalCounts: closureCounts{Conflict: "0", CoverageGap: "0", LedgerCommitted: "1", Rejected: "0", VerifiedDelivered: "0"},
	}
	results := []closureResult{{
		Adjudication: "ledger_committed", EntryID: "entry-0001", FirstObservation: "accepted", FirstObservedAt: "2026-09-22T09:00:00.000Z",
		KafkaOffset: stringPointer("41"), KafkaPartition: stringPointer("2"), KafkaTopic: stringPointer("openbkn.evidence.v1"),
		LastObservation: "deduplicated", LastObservedAt: "2026-09-22T09:00:05.000Z", LedgerIngestSequence: stringPointer("9001"),
		ManifestID: "mig-openbkn-020-golden-001",
	}}
	got, err := closureDigest(header, results)
	if err != nil {
		t.Fatal(err)
	}
	const want = "cff9384b03fc081f0217245075997d19e3e0d2ec57d9d0d8549a50f7d3931674"
	if got != want {
		t.Fatalf("closure digest = %s, want %s", got, want)
	}
}

func TestClosureDigestSortsResultsByEntryID(t *testing.T) {
	header := closureHeader{
		ActivatedAt: "2026-09-22T08:30:00.000Z", ClosedAt: "2026-09-22T09:30:00.000Z",
		ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", EntriesDigest: "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995",
		EntryCount: "2", ManifestID: "mig-1", SourceSnapshotAt: "2026-09-22T08:00:00.000Z",
		TerminalCounts: closureCounts{Conflict: "0", CoverageGap: "0", LedgerCommitted: "2", Rejected: "0", VerifiedDelivered: "0"},
	}
	first := closureResult{Adjudication: "ledger_committed", EntryID: "a", FirstObservation: "accepted", FirstObservedAt: "2026-09-22T09:00:00.000Z", LastObservation: "accepted", LastObservedAt: "2026-09-22T09:00:00.000Z", ManifestID: "mig-1"}
	second := first
	second.EntryID = "b"
	forward, err := closureDigest(header, []closureResult{first, second})
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := closureDigest(header, []closureResult{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if forward != reversed {
		t.Fatalf("closure digest depends on input order: %s != %s", forward, reversed)
	}
}

func TestClosureDigestRejectsInvalidFrozenDomain(t *testing.T) {
	header := closureHeader{
		ActivatedAt: "2026-09-22T08:30:00.000Z", ClosedAt: "2026-09-22T09:30:00.000Z",
		ContractSHA: "0016ad359b11d162e04bb11a78784c33fad0ec8d", EntriesDigest: "0d7573d407a7b8fec1868ac5d6a8085761c44c292e1589d80d866a9558371995",
		EntryCount: "01", ManifestID: "mig-1", SourceSnapshotAt: "2026-09-22T08:00:00.000Z",
		TerminalCounts: closureCounts{Conflict: "0", CoverageGap: "0", LedgerCommitted: "1", Rejected: "0", VerifiedDelivered: "0"},
	}
	if _, err := closureDigest(header, nil); err == nil {
		t.Fatal("noncanonical decimal count must be rejected")
	}
	header.EntryCount = "1"
	result := closureResult{Adjudication: "ledger_committed", EntryID: "entry-1", FirstObservation: "accepted", FirstObservedAt: "2026-09-22T09:00:00.000Z", LastObservation: "accepted", LastObservedAt: "2026-09-22T09:00:00.000Z", ManifestID: "mig-1", ReasonCode: stringPointer("中文")}
	if _, err := closureDigest(header, []closureResult{result}); err == nil {
		t.Fatal("non-ASCII result value must be rejected")
	}
}

func stringPointer(value string) *string { return &value }
