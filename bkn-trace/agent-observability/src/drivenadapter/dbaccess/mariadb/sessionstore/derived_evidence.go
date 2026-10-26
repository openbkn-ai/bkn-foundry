// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/ledgersvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.DerivedEvidenceTransaction = (*transaction)(nil)

const maxDerivedCausationEvents = 4096

// AppendDerivedEvidence is deliberately restricted to the Core's formal Claim
// producer. It participates in the lifecycle transaction and cannot commit an
// orphan event when revision or projection persistence subsequently fails.
func (t *transaction) AppendDerivedEvidence(event ledgervo.Event) (ledgervo.DurableAck, error) {
	if t.err != nil {
		return ledgervo.DurableAck{}, t.err
	}
	if event.ProducerID != "agent-observability-core" || event.EventType != "claim.created" || event.SchemaVersion != "3.0.0" || event.OperationID == "" || event.Attempt == 0 || len(event.Claims) == 0 || len(event.EvidenceRefs) == 0 {
		return ledgervo.DurableAck{}, errors.New("derived evidence must be a scoped Core Claim event")
	}
	if err := ledgersvc.ValidateHistoricalEvent(event); err != nil {
		return ledgervo.DurableAck{}, err
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return ledgervo.DurableAck{}, err
	}
	if len(encoded) > sessionvo.MaxInlinePayloadBytes {
		return ledgervo.DurableAck{}, isessionstore.ErrEvidenceConfirmationLimit
	}
	if err = verifyEvidenceOwnership(t.ctx, t.tx, event); err != nil {
		if !errors.Is(err, ievidenceledger.ErrOwnerMismatch) && !errors.Is(err, ievidenceledger.ErrMissingScope) {
			t.err = err
		}
		return ledgervo.DurableAck{}, err
	}
	var attemptExists int
	if err = t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM bkn_trace_operation_call_facts WHERE operation_id=? AND attempt_no=? AND interaction_id=?`, event.OperationID, event.Attempt, event.InteractionID).Scan(&attemptExists); err != nil {
		t.err = err
		return ledgervo.DurableAck{}, err
	}
	if attemptExists != 1 {
		return ledgervo.DurableAck{}, ievidenceledger.ErrMissingScope
	}
	var hash string
	var sequence uint64
	var ingestedAt time.Time
	err = t.tx.QueryRowContext(t.ctx, `SELECT immutable_record_hash, ingest_sequence, ingested_at FROM bkn_trace_evidence_event_ledger WHERE event_id=? FOR UPDATE`, event.EventID).Scan(&hash, &sequence, &ingestedAt)
	immutableHash := ledgervo.ImmutableRecordHash(event)
	if err == nil {
		if hash != immutableHash {
			return ledgervo.DurableAck{}, ievidenceledger.ErrPayloadConflict
		}
		return ledgervo.DurableAck{EventID: event.EventID, Durable: true, Replayed: true, IngestSequence: sequence, IngestedAt: ingestedAt.UTC()}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.err = err
		return ledgervo.DurableAck{}, err
	}
	missing, err := verifyCausationScope(t.ctx, t.tx, event)
	if err != nil {
		if !errors.Is(err, ievidenceledger.ErrCausalityConflict) {
			t.err = err
		}
		return ledgervo.DurableAck{}, err
	}
	if err = verifyDerivedCausationAcyclic(t.ctx, t.tx, event); err != nil {
		if !errors.Is(err, ievidenceledger.ErrCausalityConflict) && !errors.Is(err, isessionstore.ErrEvidenceConfirmationLimit) && !errors.Is(err, isessionstore.ErrInvalidEvidenceJSON) {
			t.err = err
		}
		return ledgervo.DurableAck{}, err
	}
	event.CausalityStatus = "complete"
	event.MissingCauseIDs = missing
	if len(missing) > 0 {
		event.CausalityStatus = "causality_missing"
	}
	var occupiedEvent string
	err = t.tx.QueryRowContext(t.ctx, `SELECT event_id FROM bkn_trace_evidence_event_ledger WHERE producer_stream_id=? AND producer_epoch=? AND producer_sequence=? FOR UPDATE`, event.ProducerStreamID, event.ProducerEpoch, event.ProducerSequence).Scan(&occupiedEvent)
	if err == nil {
		return ledgervo.DurableAck{}, ievidenceledger.ErrSequenceConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.err = err
		return ledgervo.DurableAck{}, err
	}
	var epoch, head sql.NullInt64
	if err = t.tx.QueryRowContext(t.ctx, `SELECT MAX(producer_epoch) FROM bkn_trace_evidence_event_ledger WHERE producer_stream_id=? FOR UPDATE`, event.ProducerStreamID).Scan(&epoch); err != nil {
		t.err = err
		return ledgervo.DurableAck{}, err
	}
	if epoch.Valid && event.ProducerEpoch < uint64(epoch.Int64) {
		return ledgervo.DurableAck{}, ievidenceledger.ErrSequenceConflict
	}
	if err = t.tx.QueryRowContext(t.ctx, `SELECT MAX(producer_sequence) FROM bkn_trace_evidence_event_ledger WHERE producer_stream_id=? AND producer_epoch=? FOR UPDATE`, event.ProducerStreamID, event.ProducerEpoch).Scan(&head); err != nil {
		t.err = err
		return ledgervo.DurableAck{}, err
	}
	if head.Valid && event.ProducerSequence <= uint64(head.Int64) {
		return ledgervo.DurableAck{}, ievidenceledger.ErrSequenceConflict
	}
	ack, err := insertAcceptedEvidence(t.ctx, t.tx, event, immutableHash)
	if err != nil {
		t.err = err
	}
	return ack, err
}

// The generic ingestion path predates bounded revision materialization. For the
// Core-only producer, cycle detection reads identities and causal links, never
// large tool output envelopes. Bounds match existing evidence-confirmation
// bytes and interaction-reference capacity, and fail closed when exceeded.
func verifyDerivedCausationAcyclic(ctx context.Context, tx *sql.Tx, event ledgervo.Event) error {
	rows, err := tx.QueryContext(ctx, `SELECT event_id,
 CASE WHEN SUM(OCTET_LENGTH(COALESCE(JSON_EXTRACT(envelope,'$.causation_event_ids'),'[]'))) OVER () <= ?
 THEN COALESCE(JSON_EXTRACT(envelope,'$.causation_event_ids'),'[]') ELSE NULL END
 FROM bkn_trace_evidence_event_ledger WHERE interaction_id=? ORDER BY event_id LIMIT ? FOR UPDATE`, sessionvo.MaxEvidenceConfirmationBytes, event.InteractionID, maxDerivedCausationEvents+1)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	events := []ledgervo.Event{event}
	for rows.Next() {
		var existing ledgervo.Event
		var causes []byte
		if err := rows.Scan(&existing.EventID, &causes); err != nil {
			return err
		}
		if len(events) > maxDerivedCausationEvents || causes == nil {
			return isessionstore.ErrEvidenceConfirmationLimit
		}
		if err := json.Unmarshal(causes, &existing.CausationEventIDs); err != nil {
			return fmt.Errorf("%w: derived.causation", isessionstore.ErrInvalidEvidenceJSON)
		}
		events = append(events, existing)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if ledgervo.HasCausationCycle(events) {
		return ievidenceledger.ErrCausalityConflict
	}
	return nil
}
