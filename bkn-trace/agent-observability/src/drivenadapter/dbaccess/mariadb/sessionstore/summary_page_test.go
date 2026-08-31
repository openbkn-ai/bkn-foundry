// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func TestSummaryOwnerWherePreservesLegacySubjectBoundary(t *testing.T) {
	where, args := summaryOwnerWhere("r", isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{
		AccountID: "subject-1", AccountType: "service",
	}})
	wantWhere := []string{
		"r.effective_subject_type=?", "r.effective_subject_id=?",
	}
	wantArgs := []any{"service", "subject-1"}
	if !reflect.DeepEqual(where, wantWhere) || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("where=%v args=%v", where, args)
	}
}

func TestConversationSummaryReceiptExistsExcludesPendingAndUsesReceiptTime(t *testing.T) {
	from := time.Date(2026, 8, 19, 8, 30, 0, 0, time.UTC)
	to := time.Date(2026, 8, 19, 9, 30, 0, 0, time.UTC)
	clause, args := conversationSummaryReceiptExists(isessionstore.SummaryPageQuery{From: from, To: to})
	for _, required := range []string{
		"r.trace_id IS NOT NULL", "r.trace_id<>''", "r.request_id IS NOT NULL", "r.request_id<>''",
		"r.issued_at>=?", "r.issued_at<=?",
	} {
		if !strings.Contains(clause, required) {
			t.Fatalf("receipt predicate is missing %q: %s", required, clause)
		}
	}
	if strings.Contains(clause, "c.created_at") || !reflect.DeepEqual(args, []any{from, to}) {
		t.Fatalf("clause=%s args=%v", clause, args)
	}
}

func TestUsableSummaryReceiptRequiresProjectedRequestAndTraceIdentity(t *testing.T) {
	want := []string{
		"r.trace_id IS NOT NULL", "r.trace_id<>''", "r.request_id IS NOT NULL", "r.request_id<>''",
	}
	if got := usableSummaryReceiptPredicates("r"); !reflect.DeepEqual(got, want) {
		t.Fatalf("predicates=%v", got)
	}
}
