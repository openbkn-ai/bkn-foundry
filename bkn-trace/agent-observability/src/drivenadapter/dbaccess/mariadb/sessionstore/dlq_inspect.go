// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func (s *Store) InspectProjectionDLQ(ctx context.Context, id uint64) (iprojectionoutbox.DLQEntry, error) {
	var entry iprojectionoutbox.DLQEntry
	rows, err := s.db.QueryContext(ctx, `SELECT d.dlq_id,d.source_kind,d.source_id,d.failure_class,d.last_error_code,d.failed_at,d.replayed_at,
 COALESCE(o.outbox_id,0),COALESCE(o.status,''),COALESCE(o.aggregate_type,''),COALESCE(o.aggregate_id,''),
 COALESCE(o.aggregate_version,0),COALESCE(o.event_type,''),d.attempts
 FROM bkn_trace_dlq d LEFT JOIN bkn_trace_projection_outbox o
 ON d.source_kind='projection' AND o.event_id=d.source_id WHERE d.dlq_id=? LIMIT 2`, id)
	if err != nil {
		return entry, err
	}
	defer func() { _ = rows.Close() }()
	found := false
	for rows.Next() {
		if found {
			entry.Ambiguous = true
			continue
		}
		var replayed sql.NullTime
		if err := rows.Scan(&entry.ID, &entry.SourceKind, &entry.EventID, &entry.FailureClass, &entry.LastErrorCode, &entry.FailedAt, &replayed, &entry.OutboxID, &entry.OutboxStatus, &entry.AggregateType, &entry.AggregateID, &entry.AggregateVersion, &entry.EventType, &entry.FailedAttempts); err != nil {
			return iprojectionoutbox.DLQEntry{}, err
		}
		if replayed.Valid {
			entry.ReplayedAt = &replayed.Time
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return iprojectionoutbox.DLQEntry{}, err
	}
	if !found {
		return entry, sql.ErrNoRows
	}
	return entry, nil
}
