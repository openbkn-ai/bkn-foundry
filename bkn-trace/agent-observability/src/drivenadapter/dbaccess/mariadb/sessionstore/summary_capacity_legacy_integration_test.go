// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"fmt"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"strings"
	"time"
)

// Frozen pre-window reference, independent of the current production method.
func capacityLegacyTraceIdentities(ctx context.Context, s *Store, query isessionstore.SummaryPageQuery) (isessionstore.SummaryIdentityPage, error) {
	where, args := summaryOwnerWhere("r", query)
	where = append(where, usableSummaryReceiptPredicates("r")...)
	if !query.From.IsZero() {
		where, args = append(where, "r.issued_at>=?"), append(args, query.From.UTC())
	}
	if !query.To.IsZero() {
		where, args = append(where, "r.issued_at<=?"), append(args, query.To.UTC())
	}
	if query.Service != "" {
		where = append(where, traceSummaryServicePredicate())
		args = append(args, query.Service)
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(DISTINCT r.trace_id) FROM bkn_trace_receipts r WHERE "+clause, args...).Scan(&total); err != nil {
		return isessionstore.SummaryIdentityPage{}, fmt.Errorf("count trace summary identities: %w", err)
	}

	having := ""
	pageArgs := append([]any{}, args...)
	if query.AfterStartedAt != "" && query.AfterID != "" {
		startedAt, err := time.Parse(time.RFC3339Nano, query.AfterStartedAt)
		if err != nil {
			return isessionstore.SummaryIdentityPage{}, fmt.Errorf("parse trace summary cursor: %w", err)
		}
		having = " HAVING MIN(r.issued_at)<? OR (MIN(r.issued_at)=? AND r.trace_id>?)"
		pageArgs = append(pageArgs, startedAt.UTC(), startedAt.UTC(), query.AfterID)
	}
	limit := normalizeSummaryPageLimit(query.Limit)
	pageArgs = append(pageArgs, limit+1, normalizeSummaryPageOffset(query.Offset))
	rows, err := s.db.QueryContext(ctx, `SELECT r.trace_id, MIN(r.issued_at)
		FROM bkn_trace_receipts r WHERE `+clause+`
		GROUP BY r.trace_id`+having+`
		ORDER BY MIN(r.issued_at) DESC, r.trace_id ASC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return isessionstore.SummaryIdentityPage{}, fmt.Errorf("list trace summary identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	entries, err := scanSummaryIdentities(rows, limit)
	if err != nil {
		return isessionstore.SummaryIdentityPage{}, fmt.Errorf("scan trace summary identities: %w", err)
	}
	return isessionstore.SummaryIdentityPage{Entries: entries.values, Total: total, HasMore: entries.hasMore}, nil
}
