// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func capacityCompareLegacyBoundaries(t *testing.T, s *Store, now time.Time) map[string]any {
	t.Helper()
	ctx := context.Background()
	base := isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{AccountID: "capacity-owner", AccountType: "service"}, Service: "context-loader", Limit: 20}
	cases := []isessionstore.SummaryPageQuery{base}
	for _, offset := range []int{-1, 1, 2099, 2100, 3000} {
		q := base
		q.Offset = offset
		cases = append(cases, q)
	}
	for _, limit := range []int{-1, 0, 1, 200} {
		q := base
		q.Limit = limit
		cases = append(cases, q)
	}
	for _, service := range []string{"", "other", "missing", "retry-module", "descendant"} {
		q := base
		q.Service = service
		cases = append(cases, q)
	}
	for _, owner := range []string{"foreign-owner", "missing-owner"} {
		q := base
		q.Scope.AccountID = owner
		cases = append(cases, q)
	}
	for _, offset := range []int{0, 200, 400} {
		q := base
		q.From = now.Add(time.Duration(offset) * time.Second)
		q.To = now.Add(210 * time.Second)
		cases = append(cases, q)
	}
	for _, cursor := range []string{now.Add(1000 * time.Second).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), "bad"} {
		q := base
		q.AfterStartedAt = cursor
		q.AfterID = "capacity-00000"
		cases = append(cases, q)
	}
	for i, q := range cases {
		want, we := capacityLegacyTraceIdentities(ctx, s, q)
		got, ge := s.ListTraceSummaryIdentities(ctx, q)
		if (we == nil) != (ge == nil) || !reflect.DeepEqual(want, got) {
			t.Fatalf("window boundary%d mismatch q=%+v\nwant=%+v err=%v\ngot=%+v err=%v", i, q, want, we, got, ge)
		}
	}
	q := base
	q.Limit = 200
	count := 0
	pages := 0
	for {
		want, we := capacityLegacyTraceIdentities(ctx, s, q)
		got, ge := s.ListTraceSummaryIdentities(ctx, q)
		if we != nil || ge != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("window cursor page%d mismatch: %v %v", pages, we, ge)
		}
		count += len(got.Entries)
		pages++
		if !got.HasMore {
			break
		}
		last := got.Entries[len(got.Entries)-1]
		q.AfterStartedAt = last.StartedAt
		q.AfterID = last.ID
		if pages > 12 {
			t.Fatal("window cursor failed to terminate")
		}
	}
	if count != 2100 {
		t.Fatalf("window complete count=%d", count)
	}
	where, args := summaryOwnerWhere("r", base)
	where = append(where, usableSummaryReceiptPredicates("r")...)
	where = append(where, traceSummaryServicePredicate())
	args = append(args, base.Service)
	explainSQL := `SELECT trace_id,started_at,total FROM (SELECT r.trace_id,MIN(r.issued_at) AS started_at,COUNT(*) OVER() AS total FROM bkn_trace_receipts r WHERE ` + strings.Join(where, " AND ") + ` GROUP BY r.trace_id) counted ORDER BY started_at DESC,trace_id ASC LIMIT ? OFFSET ?`
	args = append(args, base.Limit+1, 0)
	var explain string
	if err := s.db.QueryRowContext(ctx, "EXPLAIN FORMAT=JSON "+explainSQL, args...).Scan(&explain); err != nil {
		t.Fatal(err)
	}
	t.Logf("production/frozen-original boundary parity: %d cases, %d cursor pages", len(cases), pages)
	return map[string]any{"production_explain": explain, "production_sql": explainSQL, "cases": len(cases), "cursor_pages": pages, "cursor_entries": count, "scope": "same immutable fixture; concurrency/snapshot visibility not tested", "snapshot_difference": "nonempty production count/page share one statement snapshot; legacy uses two autocommit snapshots; empty fallback remains two statements"}
}
