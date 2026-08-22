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

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/ievidenceledger"
)

func (s *Store) Commit(ctx context.Context, event ledgervo.Event) (ledgervo.DurableAck, error) {
	for attempt := 0; attempt < transactionRetries; attempt++ {
		ack, retry, err := s.commitEvidenceOnce(ctx, event)
		if !retry {
			return ack, err
		}
	}
	return ledgervo.DurableAck{}, errors.New("evidence transaction retry budget exhausted")
}

func (s *Store) ListInteractionEvents(ctx context.Context, owner sessionvo.Owner, interactionID string) ([]ledgervo.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.envelope
		FROM bkn_trace_evidence_event_ledger l
		JOIN bkn_trace_conversations c ON c.conversation_id=l.conversation_id
		WHERE l.tenant_id=? AND l.business_domain_id=? AND l.interaction_id=?
		  AND c.tenant_id=? AND c.business_domain_id=?
		  AND c.application_principal_id=? AND c.effective_subject_type=?
		  AND c.effective_subject_id=? AND c.delegation_id=?
		ORDER BY l.ingest_sequence`,
		owner.TenantID, owner.BusinessDomainID, interactionID,
		owner.TenantID, owner.BusinessDomainID,
		owner.ApplicationPrincipalID, owner.EffectiveSubjectType,
		owner.EffectiveSubjectID, owner.DelegationID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]ledgervo.Event, 0)
	for rows.Next() {
		var envelope []byte
		if err := rows.Scan(&envelope); err != nil {
			return nil, err
		}
		var event ledgervo.Event
		if err := json.Unmarshal(envelope, &event); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (s *Store) commitEvidenceOnce(ctx context.Context, event ledgervo.Event) (ledgervo.DurableAck, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ledgervo.DurableAck{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := verifyEvidenceOwnership(ctx, tx, event); err != nil {
		return ledgervo.DurableAck{}, false, err
	}

	var existingHash string
	var existingSequence uint64
	var ingestedAt time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT immutable_record_hash, ingest_sequence, ingested_at
		FROM bkn_trace_evidence_event_ledger WHERE event_id=? FOR UPDATE`,
		event.EventID,
	).Scan(&existingHash, &existingSequence, &ingestedAt)
	recordHash := ledgervo.ImmutableRecordHash(event)
	switch {
	case err == nil && existingHash == recordHash:
		if err := tx.Commit(); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		return ledgervo.DurableAck{
			EventID: event.EventID, Durable: true, Replayed: true,
			IngestSequence: existingSequence, IngestedAt: ingestedAt.UTC(),
		}, false, nil
	case err == nil:
		if err := insertEvidenceConflict(ctx, tx, event, existingHash); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		if err := tx.Commit(); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		return ledgervo.DurableAck{}, false, ievidenceledger.ErrPayloadConflict
	case !errors.Is(err, sql.ErrNoRows):
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}

	missingCauses, err := verifyCausationScope(ctx, tx, event)
	if err != nil {
		if conflictErr := insertEvidenceConflict(ctx, tx, event, event.PayloadHash); conflictErr != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(conflictErr), conflictErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(commitErr), commitErr
		}
		return ledgervo.DurableAck{}, false, err
	}
	if err := verifyCausationAcyclic(ctx, tx, event); err != nil {
		if conflictErr := insertEvidenceConflict(ctx, tx, event, event.PayloadHash); conflictErr != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(conflictErr), conflictErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(commitErr), commitErr
		}
		return ledgervo.DurableAck{}, false, err
	}
	event.CausalityStatus = "complete"
	if len(missingCauses) > 0 {
		event.CausalityStatus = "causality_missing"
		event.MissingCauseIDs = missingCauses
	}

	var streamEventID, streamHash string
	err = tx.QueryRowContext(ctx, `
		SELECT event_id, payload_hash FROM bkn_trace_evidence_event_ledger
		WHERE tenant_id=? AND producer_stream_id=? AND producer_epoch=? AND producer_sequence=?
		FOR UPDATE`,
		event.Owner.TenantID, event.ProducerStreamID, event.ProducerEpoch, event.ProducerSequence,
	).Scan(&streamEventID, &streamHash)
	if err == nil && streamEventID != event.EventID {
		if err := insertEvidenceConflict(ctx, tx, event, streamHash); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		if err := tx.Commit(); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		return ledgervo.DurableAck{}, false, ievidenceledger.ErrSequenceConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}
	var maxEpoch sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(producer_epoch) FROM bkn_trace_evidence_event_ledger
		WHERE tenant_id=? AND producer_stream_id=? FOR UPDATE`,
		event.Owner.TenantID, event.ProducerStreamID,
	).Scan(&maxEpoch); err != nil {
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}
	if maxEpoch.Valid && event.ProducerEpoch < uint64(maxEpoch.Int64) {
		if err := insertEvidenceConflict(ctx, tx, event, event.PayloadHash); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		if err := tx.Commit(); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		return ledgervo.DurableAck{}, false, ievidenceledger.ErrSequenceConflict
	}
	var maxSequence sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(producer_sequence) FROM bkn_trace_evidence_event_ledger
		WHERE tenant_id=? AND producer_stream_id=? AND producer_epoch=? FOR UPDATE`,
		event.Owner.TenantID, event.ProducerStreamID, event.ProducerEpoch,
	).Scan(&maxSequence); err != nil {
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}
	if maxSequence.Valid && event.ProducerSequence <= uint64(maxSequence.Int64) {
		if err := insertEvidenceConflict(ctx, tx, event, event.PayloadHash); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		if err := tx.Commit(); err != nil {
			return ledgervo.DurableAck{}, retryableTransactionError(err), err
		}
		return ledgervo.DurableAck{}, false, ievidenceledger.ErrSequenceConflict
	}

	envelope, err := json.Marshal(event)
	if err != nil {
		return ledgervo.DurableAck{}, false, err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, "SELECT UTC_TIMESTAMP(6)").Scan(&now); err != nil {
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_evidence_event_ledger (
			event_id, payload_hash, immutable_record_hash, schema_version, event_type,
			tenant_id, business_domain_id,
			conversation_id, interaction_id, operation_id, attempt_no, request_id, trace_id,
			span_id, producer_id, producer_stream_id, producer_epoch, producer_sequence,
			causality_status, missing_causation_event_ids,
				started_at, observed_at, emitted_at, ingested_at, envelope
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, ''),
				NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?)`,
		event.EventID, event.PayloadHash, recordHash, event.SchemaVersion, event.EventType,
		event.Owner.TenantID, event.Owner.BusinessDomainID, event.ConversationID,
		event.InteractionID, event.OperationID, event.Attempt, event.RequestID, event.TraceID,
		event.SpanID, event.ProducerID, event.ProducerStreamID, event.ProducerEpoch,
		event.ProducerSequence, event.CausalityStatus, marshalJSON(event.MissingCauseIDs),
		event.StartedAt, event.ObservedAt, event.EmittedAt, now, envelope,
	)
	if err != nil {
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return ledgervo.DurableAck{}, false, err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_projection_outbox (
			aggregate_type, aggregate_id, aggregate_version, event_type, event_id, payload,
			status, attempts, available_at, created_at
		) VALUES ('evidence_event', ?, ?, 'evidence.project', ?, ?, 'pending', 0, ?, ?)`,
		event.EventID, sequence, event.EventID, envelope, now, now,
	)
	if err != nil {
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}
	if err := tx.Commit(); err != nil {
		return ledgervo.DurableAck{}, retryableTransactionError(err), err
	}
	return ledgervo.DurableAck{
		EventID: event.EventID, Durable: true, IngestSequence: uint64(sequence), IngestedAt: now.UTC(),
	}, false, nil
}

func verifyEvidenceOwnership(ctx context.Context, tx *sql.Tx, event ledgervo.Event) error {
	var tenantID, domainID, applicationID, subjectType, subjectID, delegationID string
	err := tx.QueryRowContext(ctx, `
		SELECT c.tenant_id, c.business_domain_id, c.application_principal_id,
			c.effective_subject_type, c.effective_subject_id, c.delegation_id
		FROM bkn_trace_conversations c
		JOIN bkn_trace_interactions i ON i.conversation_id=c.conversation_id
		WHERE c.conversation_id=? AND i.interaction_id=? FOR UPDATE`,
		event.ConversationID, event.InteractionID,
	).Scan(&tenantID, &domainID, &applicationID, &subjectType, &subjectID, &delegationID)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("evidence interaction does not exist")
	}
	if err != nil {
		return err
	}
	if tenantID != event.Owner.TenantID || domainID != event.Owner.BusinessDomainID ||
		applicationID != event.Owner.ApplicationPrincipalID ||
		subjectType != string(event.Owner.EffectiveSubjectType) ||
		subjectID != event.Owner.EffectiveSubjectID ||
		delegationID != event.Owner.DelegationID {
		return errors.New("evidence owner does not match trusted conversation owner")
	}
	if event.OperationID != "" {
		var count int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM bkn_trace_operations
			WHERE operation_id=? AND interaction_id=?`,
			event.OperationID, event.InteractionID,
		).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("evidence operation does not belong to interaction")
		}
	}
	return nil
}

func verifyCausationScope(ctx context.Context, tx *sql.Tx, event ledgervo.Event) ([]string, error) {
	var missing []string
	for _, causeID := range event.CausationEventIDs {
		var tenantID, domainID, interactionID string
		err := tx.QueryRowContext(ctx, `
			SELECT tenant_id, business_domain_id, interaction_id
			FROM bkn_trace_evidence_event_ledger WHERE event_id=?`,
			causeID,
		).Scan(&tenantID, &domainID, &interactionID)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, causeID)
			continue
		}
		if err != nil {
			return nil, err
		}
		if tenantID != event.Owner.TenantID || domainID != event.Owner.BusinessDomainID ||
			interactionID != event.InteractionID {
			return nil, fmt.Errorf("causation event %s crosses trusted interaction scope", causeID)
		}
	}
	return missing, nil
}

func verifyCausationAcyclic(ctx context.Context, tx *sql.Tx, event ledgervo.Event) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT envelope FROM bkn_trace_evidence_event_ledger
		WHERE tenant_id=? AND business_domain_id=? AND interaction_id=?
		FOR UPDATE`,
		event.Owner.TenantID, event.Owner.BusinessDomainID, event.InteractionID,
	)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	events := []ledgervo.Event{event}
	for rows.Next() {
		var envelope []byte
		if err := rows.Scan(&envelope); err != nil {
			return err
		}
		var existing ledgervo.Event
		if err := json.Unmarshal(envelope, &existing); err != nil {
			return err
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

func insertEvidenceConflict(ctx context.Context, tx *sql.Tx, event ledgervo.Event, existingHash string) error {
	envelope, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_event_conflicts (
			event_id, existing_payload_hash, conflicting_payload_hash,
			tenant_id, business_domain_id, envelope, detected_at
		) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`,
		event.EventID, existingHash, event.PayloadHash,
		event.Owner.TenantID, event.Owner.BusinessDomainID, envelope,
	)
	return err
}
