// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

//go:build integration

package sessionstore

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Runs inside TestSummaryCapacityMariaDB's isolated, immutable 2,202-Trace
// fixture. Compare the optimized path with the frozen original SQL, including
// duplicate receipts, tied times, foreign owners and pre-pagination totals.
func capacityGlobalTracePages(t *testing.T, store *Store, now time.Time) map[string]any {
	t.Helper()
	ctx := context.Background()
	base := isessionstore.SummaryPageQuery{Limit: 20, Scope: evidencevo.QueryScope{
		AccessProfile: &evidencevo.AccessProfile{AccountActive: true, Roles: []string{"super_admin"}},
	}}
	compare := func(q isessionstore.SummaryPageQuery) isessionstore.SummaryIdentityPage {
		t.Helper()
		want, wantErr := capacityLegacyTraceIdentities(ctx, store, q)
		got, gotErr := store.ListTraceSummaryIdentities(ctx, q)
		if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
			t.Fatalf("global page mismatch: query=%+v\nwant=%+v error=%v\ngot=%+v error=%v", q, want, wantErr, got, gotErr)
		}
		return got
	}
	cases := 0
	for _, offset := range []int{-1, 0, 20, 40, 2200, 2202, 10000} {
		for _, limit := range []int{0, 1, 20, 200} {
			q := base
			q.Offset, q.Limit = offset, limit
			if page := compare(q); page.Total != 2202 {
				t.Fatalf("global total=%d, want 2202", page.Total)
			}
			cases++
		}
	}
	// Time filters keep the original plan and must retain receipt-time semantics.
	for _, bounds := range [][2]time.Time{{now, {}}, {{}, now}, {now.Add(10 * time.Second), now.Add(19 * time.Second)}, {now.Add(time.Hour), {}}} {
		q := base
		q.From, q.To = bounds[0], bounds[1]
		compare(q)
		cases++
	}
	q := base
	q.Limit = 200
	seen := map[string]bool{}
	for {
		page := compare(q)
		if page.Total != 2202 {
			t.Fatalf("cursor changed full total: %+v", page)
		}
		for _, entry := range page.Entries {
			if seen[entry.ID] {
				t.Fatalf("cursor duplicated Trace %s", entry.ID)
			}
			seen[entry.ID] = true
		}
		if !page.HasMore {
			break
		}
		last := page.Entries[len(page.Entries)-1]
		q.AfterStartedAt, q.AfterID = last.StartedAt, last.ID
	}
	if len(seen) != 2202 {
		t.Fatalf("cursor visited %d Traces, want 2202", len(seen))
	}
	// Re-reading the first page must still match the independent live query.
	compare(base)
	q.AfterStartedAt = "invalid"
	compare(q)
	return map[string]any{"boundary_cases": cases, "cursor_entries": len(seen), "reference": "frozen original SQL; same immutable fixture"}
}
