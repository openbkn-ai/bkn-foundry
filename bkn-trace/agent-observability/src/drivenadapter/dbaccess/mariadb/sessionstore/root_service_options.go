// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// A source fact must belong to a receipt tuple visible to this owner. Global
// readers retain their full trace namespace; roots are never time-windowed.
func rootServiceSQL(traceColumn string, q isessionstore.SummaryPageQuery) (string, []any) {
	clause := "f.trace_id=" + traceColumn + " AND f.source_module<>''"
	var args []any
	if q.Scope.AccessProfile == nil || !evidencevo.HasGlobalTraceAccess(*q.Scope.AccessProfile) {
		where, a := summaryOwnerWhere("rs", q)
		where = append(where, usableSummaryReceiptPredicates("rs")...)
		where = append(where, "rs.trace_id=f.trace_id", "rs.conversation_id=f.conversation_id", "rs.interaction_id=f.interaction_id")
		clause += " AND EXISTS (SELECT 1 FROM bkn_trace_receipts rs WHERE " + strings.Join(where, " AND ") + ")"
		args = a
	}
	return "(SELECT f.source_module FROM bkn_trace_operation_call_facts f WHERE " + clause + " ORDER BY f.started_at,f.operation_id,f.attempt_no LIMIT 1)", args
}
func traceSummaryScopedServicePredicate(q isessionstore.SummaryPageQuery) (string, []any) {
	expr, args := rootServiceSQL("r.trace_id", q)
	return expr + "=?", append(args, q.Service)
}
func (s *Store) ListTraceRootServiceOptions(ctx context.Context, q isessionstore.SummaryPageQuery, search string) ([]string, bool, error) {
	if q.Scope.AccessProfile != nil && !q.Scope.AccessProfile.AccountActive {
		return []string{}, false, nil
	}
	where, args := summaryOwnerWhere("r", q)
	where = append(where, usableSummaryReceiptPredicates("r")...)
	if !q.From.IsZero() {
		where = append(where, "r.issued_at>=?")
		args = append(args, q.From.UTC())
	}
	if !q.To.IsZero() {
		where = append(where, "r.issued_at<=?")
		args = append(args, q.To.UTC())
	}
	if q.Tool != "" {
		predicate, a := rootToolPredicate(q)
		where = append(where, predicate)
		args = append(args, a...)
	}
	expr, rootArgs := rootServiceSQL("selected.trace_id", q)
	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	statement := "SELECT DISTINCT CAST(root_service AS BINARY) FROM (SELECT " + expr + " AS root_service FROM (SELECT DISTINCT r.trace_id FROM bkn_trace_receipts r WHERE " + strings.Join(where, " AND ") + ") selected) roots WHERE root_service IS NOT NULL AND root_service<>'' AND LOCATE(CAST(LOWER(?) AS BINARY),CAST(LOWER(root_service) AS BINARY))>0 ORDER BY CAST(root_service AS BINARY) LIMIT ?"
	values := append(rootArgs, args...)
	values = append(values, strings.TrimSpace(search), limit+1)
	rows, err := s.db.QueryContext(ctx, rootServiceBudgetStatement(statement), values...)
	if err != nil {
		return nil, false, rootServiceQueryError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, false, rootServiceQueryError(err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		return nil, false, rootServiceQueryError(err)
	}
	more := len(result) > limit
	if more {
		result = result[:limit]
	}
	return result, more, nil
}

// Scope the database execution limit to discovery only; canceling a driver
// connection does not itself establish that server execution has stopped.
func rootServiceBudgetStatement(statement string) string {
	return "SET STATEMENT max_statement_time=3 FOR " + statement
}

func rootServiceQueryError(err error) error {
	var dbErr *mysql.MySQLError
	if errors.As(err, &dbErr) && dbErr.Number == 1969 {
		return errors.Join(context.DeadlineExceeded, err)
	}
	return err
}
