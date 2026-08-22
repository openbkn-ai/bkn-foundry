// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func (s *Store) ProjectionHighWatermark(ctx context.Context) (uint64, error) {
	var value uint64
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(outbox_id), 0) FROM bkn_trace_projection_outbox",
	).Scan(&value)
	return value, err
}

func (s *Store) ScanProjectionHistory(
	ctx context.Context,
	after uint64,
	through uint64,
	limit int,
) ([]iprojectionoutbox.Item, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT outbox_id, aggregate_type, aggregate_id, aggregate_version, event_type, event_id,
			payload, attempts, created_at
		FROM bkn_trace_projection_outbox
		WHERE outbox_id>? AND outbox_id<=?
		ORDER BY outbox_id LIMIT ?`, after, through, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []iprojectionoutbox.Item
	for rows.Next() {
		var item iprojectionoutbox.Item
		if err := rows.Scan(
			&item.ID, &item.AggregateType, &item.AggregateID, &item.AggregateVersion,
			&item.EventType,
			&item.EventID, &item.Payload, &item.Attempts, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ScanAuthoritativeProjection(
	ctx context.Context,
	afterAggregateType string,
	afterAggregateID string,
	limit int,
) ([]iprojectionoutbox.Item, error) {
	if limit <= 0 {
		limit = 500
	}
	var result []iprojectionoutbox.Item
	scanners := []struct {
		aggregateType string
		scan          func(context.Context, string, int) ([]iprojectionoutbox.Item, error)
	}{
		{"assembly_revision", s.scanAssemblyRevisionProjection},
		{"conversation", s.scanConversationProjection},
		{"evidence_event", s.scanEvidenceEventProjection},
		{"interaction", s.scanInteractionProjection},
		{"operation", s.scanOperationProjection},
		{"receipt", s.scanReceiptProjection},
	}
	for _, scanner := range scanners {
		if scanner.aggregateType < afterAggregateType {
			continue
		}
		afterID := ""
		if scanner.aggregateType == afterAggregateType {
			afterID = afterAggregateID
		}
		items, err := scanner.scan(ctx, afterID, limit-len(result))
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func projectionItem(
	aggregateType, aggregateID string,
	aggregateVersion uint64,
	value any,
) (iprojectionoutbox.Item, error) {
	projectionValue := value
	if receipt, ok := value.(sessionvo.Receipt); ok {
		projectionValue = sessionvo.NewReceiptProjectionDocument(receipt)
	}
	payload, err := json.Marshal(projectionValue)
	if err != nil {
		return iprojectionoutbox.Item{}, err
	}
	return iprojectionoutbox.Item{
		AggregateType: aggregateType, AggregateID: aggregateID,
		AggregateVersion: aggregateVersion,
		EventType:        aggregateType + ".snapshot",
		EventID:          aggregateType + ":" + aggregateID,
		Payload:          payload,
	}, nil
}

func (s *Store) scanConversationProjection(
	ctx context.Context, afterID string, limit int,
) ([]iprojectionoutbox.Item, error) {
	rows, err := s.db.QueryContext(
		ctx, conversationSelect+` WHERE conversation_id>? ORDER BY conversation_id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []iprojectionoutbox.Item
	for rows.Next() {
		value, err := scanConversationRows(rows)
		if err != nil {
			return nil, err
		}
		item, err := projectionItem("conversation", value.ID, value.RowVersion, value)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) scanInteractionProjection(
	ctx context.Context, afterID string, limit int,
) ([]iprojectionoutbox.Item, error) {
	rows, err := s.db.QueryContext(
		ctx, interactionSelect+` WHERE interaction_id>? ORDER BY interaction_id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []iprojectionoutbox.Item
	for rows.Next() {
		value, err := scanInteractionRows(rows)
		if err != nil {
			return nil, err
		}
		item, err := projectionItem("interaction", value.ID, value.RowVersion, value)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) scanOperationProjection(
	ctx context.Context, afterID string, limit int,
) ([]iprojectionoutbox.Item, error) {
	rows, err := s.db.QueryContext(
		ctx, operationSelect+` WHERE operation_id>? ORDER BY operation_id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []iprojectionoutbox.Item
	for rows.Next() {
		value, err := scanOperationRows(rows)
		if err != nil {
			return nil, err
		}
		item, err := projectionItem("operation", value.ID, value.RowVersion, value)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) scanReceiptProjection(
	ctx context.Context, afterID string, limit int,
) ([]iprojectionoutbox.Item, error) {
	rows, err := s.db.QueryContext(
		ctx, receiptSelect+` WHERE receipt_id>? ORDER BY receipt_id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []iprojectionoutbox.Item
	for rows.Next() {
		value, err := scanReceiptRows(rows)
		if err != nil {
			return nil, err
		}
		item, err := projectionItem("receipt", value.ID, value.RowVersion, value)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) scanEvidenceEventProjection(
	ctx context.Context, afterID string, limit int,
) ([]iprojectionoutbox.Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, ingest_sequence, envelope
		FROM bkn_trace_evidence_event_ledger
		WHERE event_id>? ORDER BY event_id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []iprojectionoutbox.Item
	for rows.Next() {
		var item iprojectionoutbox.Item
		if err := rows.Scan(&item.AggregateID, &item.AggregateVersion, &item.Payload); err != nil {
			return nil, err
		}
		item.AggregateType = "evidence_event"
		item.EventType = "evidence_event.snapshot"
		item.EventID = "evidence_event:" + item.AggregateID
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) scanAssemblyRevisionProjection(
	ctx context.Context, afterID string, limit int,
) ([]iprojectionoutbox.Item, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT revision_id, revision_no, COALESCE(parent_revision_id, ''), interaction_id,
			completion_manifest_version, included_receipt_ids, included_event_ids,
			artifact_manifest_hash, assembly_completeness, COALESCE(partial_reasons, ''),
			trigger_type, created_at
		FROM bkn_trace_assembly_revisions
		WHERE revision_id>? ORDER BY revision_id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []iprojectionoutbox.Item
	for rows.Next() {
		var value sessionvo.AssemblyRevision
		var receipts, events, reasons string
		if err := rows.Scan(
			&value.ID, &value.RevisionNo, &value.ParentRevisionID, &value.InteractionID,
			&value.CompletionManifestVersion, &receipts, &events,
			&value.ArtifactManifestHash, &value.Completeness, &reasons,
			&value.Trigger, &value.CreatedAt,
		); err != nil {
			return nil, err
		}
		unmarshalJSON(receipts, &value.IncludedReceiptIDs)
		unmarshalJSON(events, &value.IncludedEventIDs)
		unmarshalJSON(reasons, &value.PartialReasons)
		item, err := projectionItem(
			"assembly_revision", value.ID, value.RevisionNo, value,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) CountAuthoritativeProjection(ctx context.Context) (uint64, error) {
	var value uint64
	err := s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM bkn_trace_conversations) +
			(SELECT COUNT(*) FROM bkn_trace_interactions) +
			(SELECT COUNT(*) FROM bkn_trace_operations) +
			(SELECT COUNT(*) FROM bkn_trace_receipts) +
			(SELECT COUNT(*) FROM bkn_trace_evidence_event_ledger) +
			(SELECT COUNT(*) FROM bkn_trace_assembly_revisions)`,
	).Scan(&value)
	if err != nil {
		return 0, fmt.Errorf("count authoritative BKN Trace projection: %w", err)
	}
	return value, err
}

func (s *Store) LoadProjectionCheckpoint(
	ctx context.Context,
	projectorID string,
	indexVersion string,
) (uint64, error) {
	var value uint64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(last_outbox_id), 0)
		FROM bkn_trace_projection_checkpoints
		WHERE projector_id=? AND index_version=?`,
		projectorID, indexVersion,
	).Scan(&value)
	return value, err
}

func (s *Store) SaveProjectionCheckpoint(
	ctx context.Context,
	projectorID string,
	indexVersion string,
	lastOutboxID uint64,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bkn_trace_projection_checkpoints (
			projector_id, index_version, last_outbox_id, updated_at
		) VALUES (?, ?, ?, UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE
			last_outbox_id=GREATEST(last_outbox_id, VALUES(last_outbox_id)),
			updated_at=UTC_TIMESTAMP(6)`,
		projectorID, indexVersion, lastOutboxID,
	)
	return err
}
