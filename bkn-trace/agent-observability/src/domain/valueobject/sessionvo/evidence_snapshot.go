// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionvo

import (
	"bytes"
	"encoding/json"
	"time"
)

// EvidenceInteraction is the recorded interaction state without lifecycle leases
// or idempotency secrets. It does not recompute completeness or infer success.
type EvidenceInteraction struct {
	ID              string            `json:"interaction_id"`
	ConversationID  string            `json:"conversation_id"`
	Ordinal         uint64            `json:"ordinal"`
	ExecutionStatus InteractionStatus `json:"execution_status"`
	EvidenceStatus  EvidenceStatus    `json:"evidence_status"`
	ClosureManifest *ClosureManifest  `json:"closure_manifest,omitempty"`
	RowVersion      uint64            `json:"row_version"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	TerminalAt      *time.Time        `json:"terminal_at,omitempty"`
}

// EvidenceSnapshot is an owned copy of one session-store read. It is not a full
// evidence manifest: artifact bodies still have a separate source.
// CallFacts and Receipts are deliberately independent, including unmatched ones.
// This is an internal read model, not an extension of an existing wire response.
type EvidenceSnapshot struct {
	Interaction EvidenceInteraction `json:"interaction"`
	Operations  []Operation         `json:"operations"`
	Receipts    []Receipt           `json:"receipts"`
	CallFacts   []OperationCallFact `json:"call_facts"`
	Revisions   []AssemblyRevision  `json:"assembly_revisions"`
	// Nil means the adapter did not read the ledger, not that no events exist.
	// A non-nil empty ledger means this read observed zero events in its scope.
	Ledger *EvidenceLedgerSnapshot `json:"ledger,omitempty"`
}

// EvidenceLedgerSnapshot retains existing ledger rows, without deriving field
// dependencies, resolving artifact references or certifying answer adoption.
type EvidenceLedgerSnapshot struct {
	Events []EvidenceLedgerRecord `json:"events"`
}

type EvidenceLedgerRecord struct {
	EventID        string `json:"event_id"`
	IngestSequence uint64 `json:"ingest_sequence"`
	// Envelope is the full stored event JSON, including its original payload.
	// Keep bytes from storage; do not re-encode through an untyped JSON value.
	Envelope json.RawMessage `json:"envelope"`
}

// CopyEvidenceSnapshot must run inside the source's consistent read boundary.
// The result shares no mutable payload/state with the source. Typed metadata
// round-trips without float64 conversion; recorded payload bytes are cloned
// separately, never passed through InlineJSONPayload or JSON re-encoding.
func CopyEvidenceSnapshot(interaction Interaction, operations []Operation, receipts []Receipt,
	facts []OperationCallFact, revisions []AssemblyRevision) (EvidenceSnapshot, error) {
	metadata := EvidenceSnapshot{
		Interaction: EvidenceInteraction{ID: interaction.ID, ConversationID: interaction.ConversationID,
			Ordinal: interaction.Ordinal, ExecutionStatus: interaction.ExecutionStatus,
			EvidenceStatus: interaction.EvidenceStatus, ClosureManifest: interaction.ClosureManifest,
			RowVersion: interaction.RowVersion, CreatedAt: interaction.CreatedAt,
			UpdatedAt: interaction.UpdatedAt, TerminalAt: interaction.TerminalAt},
		Operations: operations, Receipts: receipts, Revisions: revisions,
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return EvidenceSnapshot{}, err
	}
	var result EvidenceSnapshot
	if err := json.Unmarshal(raw, &result); err != nil {
		return EvidenceSnapshot{}, err
	}
	result.CallFacts = make([]OperationCallFact, len(facts))
	for i, fact := range facts {
		result.CallFacts[i] = fact
		result.CallFacts[i].Input.Inline = bytes.Clone(fact.Input.Inline)
		result.CallFacts[i].Output = copyEvidencePayload(fact.Output)
		result.CallFacts[i].Error = copyEvidencePayload(fact.Error)
		if fact.FinishedAt != nil {
			value := *fact.FinishedAt
			result.CallFacts[i].FinishedAt = &value
		}
	}
	return result, nil
}

func copyEvidencePayload(payload *PayloadEnvelope) *PayloadEnvelope {
	if payload == nil {
		return nil
	}
	result := *payload
	result.Inline = bytes.Clone(payload.Inline)
	return &result
}
