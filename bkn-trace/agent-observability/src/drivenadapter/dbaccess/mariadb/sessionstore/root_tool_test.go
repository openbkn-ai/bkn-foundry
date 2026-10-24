// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
	"strings"
	"testing"
	"unicode"
)

func TestRootToolWhitespaceExactlyMatchesGoTrimSpace(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.IsSpace(r) != strings.ContainsRune(rootToolWhitespace, r) {
			t.Fatalf("whitespace contract differs at U+%04X", r)
		}
	}
}
func TestRootToolPredicateUsesAuthorizedWholeTraceAndBinaryComparison(t *testing.T) {
	sql, args := rootToolPredicate(isessionstore.SummaryPageQuery{Scope: evidencevo.QueryScope{AccountType: "app", AccountID: "owner"}, Tool: "Exact工具"})
	for _, part := range []string{"rt.effective_subject_type=", "rt.effective_subject_id=", "rt.trace_id=r.trace_id", "rt.request_id<>''", "rt.tool_name REGEXP ", "ORDER BY rt.issued_at,rt.receipt_id LIMIT 1", "AS BINARY)=CAST(? AS BINARY)"} {
		if !strings.Contains(sql, part) {
			t.Fatalf("missing %q: %s", part, sql)
		}
	}
	if len(args) != 3 || args[0] != "app" || args[1] != "owner" || args[2] != "Exact工具" {
		t.Fatalf("scope/tool args=%v", args)
	}
	if strings.Contains(sql, "issued_at>=") || strings.Contains(sql, "issued_at<=") {
		t.Fatal("candidate time bounds must not clip root history")
	}
}
