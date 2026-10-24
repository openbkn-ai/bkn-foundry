// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// Enumerate Go unicode.IsSpace exactly. PCRE's generic space class and SQL TRIM
// have different Unicode contracts. Hex encoding avoids SQL-mode backslash rules.
const rootToolWhitespace = "\t\n\v\f\r \u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

func normalizedRootToolSQL(alias string) string {
	pattern := "^[" + rootToolWhitespace + "]+|[" + rootToolWhitespace + "]+$"
	return "REGEXP_REPLACE(" + alias + ".tool_name, CONVERT(0x" + hex.EncodeToString([]byte(pattern)) + " USING utf8mb4), '')"
}

// Existence needs a character-class match, not a rewritten copy of the name.
// Use exactly the same whitespace set as TrimSpace and the filter predicate.
func nonBlankRootToolSQL(alias string) string {
	pattern := "[^" + rootToolWhitespace + "]"
	return alias + ".tool_name REGEXP CONVERT(0x" + hex.EncodeToString([]byte(pattern)) + " USING utf8mb4)"
}

// The subquery sees the authorized trace's entire lifetime. The outer identity
// query alone owns the time window. Never match an arbitrary child tool.
func rootToolPredicate(query isessionstore.SummaryPageQuery) (string, []any) {
	where, args := summaryOwnerWhere("rt", query)
	where = append(where, usableSummaryReceiptPredicates("rt")...)
	tool := normalizedRootToolSQL("rt")
	where = append(where, "rt.trace_id=r.trace_id", nonBlankRootToolSQL("rt"))
	return "CAST((SELECT " + tool + " FROM bkn_trace_receipts rt WHERE " + strings.Join(where, " AND ") + " ORDER BY rt.issued_at,rt.receipt_id LIMIT 1) AS BINARY)=CAST(? AS BINARY)", append(args, query.Tool)
}

func (s *Store) listToolTraceIdentityPage(ctx context.Context, query isessionstore.SummaryPageQuery) (isessionstore.SummaryIdentityPage, error) {
	where, args := summaryOwnerWhere("r", query)
	where = append(where, usableSummaryReceiptPredicates("r")...)
	if !query.From.IsZero() {
		where = append(where, "r.issued_at>=?")
		args = append(args, query.From.UTC())
	}
	if !query.To.IsZero() {
		where = append(where, "r.issued_at<=?")
		args = append(args, query.To.UTC())
	}
	predicate, rootArgs := rootToolPredicate(query)
	where = append(where, predicate)
	args = append(args, rootArgs...)
	if query.Service != "" {
		where = append(where, traceSummaryServicePredicate())
		args = append(args, query.Service)
	}
	// Reuse the existing total-before-cursor statement and empty-page fallback.
	return s.listServiceTraceIdentityPage(ctx, query, strings.Join(where, " AND "), args)
}

// ListCanonicalTraceRootsByTraceIDs combines root service and scoped tool
// metadata in the existing single batch roundtrip. No payload columns are read.
func (t *transaction) ListCanonicalTraceRootsByTraceIDs(traceIDs []string, scope evidencevo.QueryScope) map[string]isessionstore.CanonicalTraceRoot {
	result := map[string]isessionstore.CanonicalTraceRoot{}
	if t.err != nil || len(traceIDs) == 0 {
		return result
	}
	selects := []string{}
	idArgs := []any{}
	seen := map[string]bool{}
	for _, id := range traceIDs {
		if id != "" && isASCII(id) && !seen[id] {
			seen[id] = true
			selects = append(selects, "SELECT "+asciiBinaryParameter()+" AS trace_id")
			idArgs = append(idArgs, id)
		}
	}
	if len(selects) == 0 {
		return result
	}
	where, args := summaryOwnerWhere("rt", isessionstore.SummaryPageQuery{Scope: scope})
	where = append(where, usableSummaryReceiptPredicates("rt")...)
	where = append(where, "rt.trace_id=selected.trace_id")
	// Let the trace/issued_at/receipt_id index stop at the first nonblank
	// receipt, without sorting every receipt by a computed blank flag.
	// The cheap fallback distinguishes all-blank receipts from no receipts.
	// Select the original name and trim it once in Go; SQL need only detect
	// whether it contains a non-whitespace character.
	rootWhere := strings.Join(where, " AND ")
	statement := `SELECT selected.trace_id,
 (SELECT f.source_module FROM bkn_trace_operation_call_facts f WHERE f.trace_id=selected.trace_id AND f.source_module<>'' ORDER BY f.started_at,f.operation_id,f.attempt_no LIMIT 1),
 COALESCE((SELECT rt.tool_name FROM bkn_trace_receipts rt WHERE ` + rootWhere + ` AND ` + nonBlankRootToolSQL("rt") + ` ORDER BY rt.issued_at,rt.receipt_id LIMIT 1),
 (SELECT '' FROM bkn_trace_receipts rt WHERE ` + rootWhere + ` LIMIT 1))
 FROM (` + strings.Join(selects, " UNION ALL ") + `) selected`
	args = append(args, args...)
	args = append(args, idArgs...)
	rows, err := t.tx.QueryContext(t.ctx, statement, args...)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var module, tool sql.NullString
		if err := rows.Scan(&id, &module, &tool); err != nil {
			t.err = err
			return nil
		}
		result[id] = isessionstore.CanonicalTraceRoot{SourceModule: module.String, ToolName: strings.TrimSpace(tool.String), HasTool: tool.Valid}
	}
	if err := rows.Close(); err != nil {
		t.err = err
		return nil
	}
	t.err = rows.Err()
	return result
}
