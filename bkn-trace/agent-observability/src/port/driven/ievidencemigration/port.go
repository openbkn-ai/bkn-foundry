// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

// Package ievidencemigration owns the narrow persistence boundary for the
// one-time Evidence-outbox bridge. It deliberately does not expose mutable
// manifests to the Consumer or bridge.
package ievidencemigration

import "context"

type ManifestState string

const (
	ManifestDraft  ManifestState = "draft"
	ManifestActive ManifestState = "active"
	ManifestClosed ManifestState = "closed"
)

type Admission struct {
	ManifestID           string
	State                ManifestState
	EntryID              string
	EventID              string
	PayloadHash          string
	Classification       string
	ClassificationReason string
}

// AdmissionReader is granted only SELECT on the active manifest/entry facts.
// found=false is intentionally distinct from a database failure: the former
// is a permanent rejection, while the latter must leave the Kafka offset
// uncommitted.
type AdmissionReader interface {
	LookupAdmission(ctx context.Context, manifestID, eventID string) (admission Admission, found bool, err error)
}

type Adjudication string

const (
	AdjudicationLedgerCommitted   Adjudication = "ledger_committed"
	AdjudicationConflict          Adjudication = "conflict"
	AdjudicationRejected          Adjudication = "rejected"
	AdjudicationVerifiedDelivered Adjudication = "verified_delivered"
	AdjudicationCoverageGap       Adjudication = "coverage_gap"
)

type ConsumerResult struct {
	ManifestID     string
	EntryID        string
	Adjudication   Adjudication
	Observation    string // accepted|deduplicated|conflict|rejected
	ReasonCode     string
	Topic          string
	Partition      int
	Offset         int64
	IngestSequence uint64
}

// ConsumerResultWriter is intentionally separate from AdmissionReader so a
// read-only Consumer identity cannot accidentally obtain write permission.
type ConsumerResultWriter interface {
	RecordConsumerResult(context.Context, ConsumerResult) error
}

type ReconcilerResult struct {
	ManifestID, EntryID string
	Adjudication        Adjudication
	ReasonCode          string
}

// ReconcilerResultWriter only records terminal conclusions for immutable
// verify_delivered and coverage_gap manifest classifications.
type ReconcilerResultWriter interface {
	RecordReconcilerResult(context.Context, ReconcilerResult) error
}
