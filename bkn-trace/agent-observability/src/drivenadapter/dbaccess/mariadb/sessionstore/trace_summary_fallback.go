// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

var _ isessionstore.TraceSummaryFallbackStore = (*Store)(nil)

// LoadTraceSummaryFallback aggregates only receipt metadata for selected IDs.
// The aggregate sees the entire authorized Trace lifetime, matching projection
// enrichment; the identity query alone owns the caller's time range.
func (s *Store) LoadTraceSummaryFallback(ctx context.Context, ids []string, scope evidencevo.QueryScope) ([]evidencevo.TraceSummary, error) {
	result := []evidencevo.TraceSummary{}
	if len(ids) == 0 {
		return result, nil
	}
	if len(ids) > normalizeSummaryPageLimit(len(ids)) {
		return nil, fmt.Errorf("trace fallback exceeds page limit")
	}
	if scope.AccessProfile != nil && !scope.AccessProfile.AccountActive {
		return result, nil
	}
	placeholders := []string{}
	idArgs := []any{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || !isASCII(id) {
			return nil, fmt.Errorf("invalid Trace fallback identity")
		}
		if !seen[id] {
			seen[id] = true
			placeholders = append(placeholders, asciiBinaryParameter())
			idArgs = append(idArgs, id)
		}
	}
	where, args := summaryOwnerWhere("r", isessionstore.SummaryPageQuery{Scope: scope})
	where = append(where, usableSummaryReceiptPredicates("r")...)
	where = append(where, "r.trace_id IN ("+strings.Join(placeholders, ",")+")")
	args = append(args, idArgs...)
	firstWhere, firstArgs := summaryOwnerWhere("f", isessionstore.SummaryPageQuery{Scope: scope})
	firstWhere = append(firstWhere, usableSummaryReceiptPredicates("f")...)
	firstWhere = append(firstWhere, "f.trace_id=g.trace_id")
	args = append(args, firstArgs...)
	// Match BuildExecutionSummaries: all terminal receipts before the first
	// failure, and every failed receipt, advance completion. Later successes
	// cannot change an already failed Trace's completion timestamp.
	statement := `SELECT g.trace_id,first_receipt.request_id,first_receipt.conversation_id,g.interaction_id,
 first_receipt.application_principal_id,first_receipt.effective_subject_id,
 g.started_at,g.completed_at,g.has_failure
 FROM (SELECT visible.trace_id,MIN(visible.issued_at) AS started_at,
 MAX(CASE WHEN visible.failures_seen=0 OR visible.has_failure THEN visible.completed_at END) AS completed_at,
 MAX(visible.has_failure) AS has_failure,
 CASE WHEN COUNT(DISTINCT NULLIF(visible.interaction_id,''))=1 AND MAX(visible.interaction_id)<>'-'
 THEN MAX(visible.interaction_id) ELSE '' END AS interaction_id
 FROM (SELECT r.trace_id,r.interaction_id,r.issued_at,COALESCE(r.terminal_at,r.issued_at) AS completed_at,
 LOWER(r.receipt_status) IN ('failed','error','failure') AS has_failure,
 SUM(LOWER(r.receipt_status) IN ('failed','error','failure')) OVER (
 PARTITION BY r.trace_id ORDER BY r.issued_at,r.receipt_id ROWS UNBOUNDED PRECEDING) AS failures_seen
 FROM bkn_trace_receipts r WHERE ` + strings.Join(where, " AND ") + `) visible
 GROUP BY visible.trace_id) g
 JOIN bkn_trace_receipts first_receipt ON first_receipt.receipt_id=(
 SELECT f.receipt_id FROM bkn_trace_receipts f WHERE ` + strings.Join(firstWhere, " AND ") + `
 ORDER BY f.issued_at,f.receipt_id LIMIT 1)`
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query Trace fallback metadata: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var row evidencevo.TraceSummary
		var start, end time.Time
		var failed bool
		if err := rows.Scan(&row.TraceID, &row.RequestID, &row.ConversationID, &row.InteractionID, &row.ApplicationPrincipalID, &row.EffectiveSubjectID, &start, &end, &failed); err != nil {
			return nil, fmt.Errorf("scan Trace fallback metadata: %w", err)
		}
		row.AgentOrApp = row.ApplicationPrincipalID
		row.StartedAt = start.UTC().Format(time.RFC3339Nano)
		row.CompletedAt = end.UTC().Format(time.RFC3339Nano)
		if !end.Before(start) {
			row.DurationMS = end.Sub(start).Milliseconds()
		}
		row.Status = "completed"
		if failed {
			row.Status = "error"
		}
		row.SpanCountStatus = "unavailable"
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Trace fallback metadata: %w", err)
	}
	return result, nil
}
