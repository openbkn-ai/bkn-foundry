// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"strings"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

// ListFirstTraceFailureMetadata selects the same first failed fact as the full
// interaction reader, independently for each trace/conversation/interaction.
// Rank only keys; fetch a terminal payload only for the winning fact.
func (t *transaction) ListFirstTraceFailureMetadata(scopes []isessionstore.TraceMetadataScope) []sessionvo.OperationCallFact {
	if t.err != nil {
		return nil
	}
	placeholders, scopeArgs := traceMetadataScopeSQL(scopes)
	if len(scopeArgs) == 0 {
		return []sessionvo.OperationCallFact{}
	}
	args := append([]any{sessionvo.AttemptFailed}, scopeArgs...)
	rows, err := t.tx.QueryContext(t.ctx, `SELECT f.trace_id,f.conversation_id,f.interaction_id,
 f.operation_id,f.attempt_no,f.started_at,f.status,COALESCE(f.error_payload,'')
 FROM bkn_trace_operation_call_facts f
 JOIN (SELECT operation_id,attempt_no,ROW_NUMBER() OVER (
 PARTITION BY interaction_id,trace_id,conversation_id
 ORDER BY started_at,operation_id,attempt_no) AS failure_rank
 FROM bkn_trace_operation_call_facts WHERE status=? AND (trace_id,conversation_id,interaction_id) IN (`+placeholders+`)) first_failure
 ON f.operation_id=first_failure.operation_id AND f.attempt_no=first_failure.attempt_no
 WHERE first_failure.failure_rank=1
 ORDER BY f.interaction_id,f.trace_id,f.conversation_id`, args...)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	result := []sessionvo.OperationCallFact{}
	for rows.Next() {
		var fact sessionvo.OperationCallFact
		var raw string
		if err := rows.Scan(&fact.TraceID, &fact.ConversationID, &fact.InteractionID,
			&fact.OperationID, &fact.Attempt, &fact.StartedAt, &fact.Status, &raw); err != nil {
			t.err = err
			return nil
		}
		if raw != "" {
			fact.Error, _, err = decodeTerminalPayload(raw)
			if err != nil {
				t.err = err
				return nil
			}
		}
		result = append(result, fact)
	}
	if err := rows.Err(); err != nil {
		t.err = err
		return nil
	}
	return result
}

// ListFirstTraceSourceMetadata deliberately retains separate scope tuples so
// callers supply only authorized interactions before selecting a root source.
func (t *transaction) ListFirstTraceSourceMetadata(scopes []isessionstore.TraceMetadataScope) []sessionvo.OperationCallFact {
	if t.err != nil {
		return nil
	}
	placeholders, args := traceMetadataScopeSQL(scopes)
	if len(args) == 0 {
		return []sessionvo.OperationCallFact{}
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT f.trace_id,f.conversation_id,f.interaction_id,
 f.operation_id,f.attempt_no,f.started_at,f.source_module
 FROM bkn_trace_operation_call_facts f
 JOIN (SELECT operation_id,attempt_no,ROW_NUMBER() OVER (
 PARTITION BY interaction_id,trace_id,conversation_id
 ORDER BY started_at,operation_id,attempt_no) AS source_rank
 FROM bkn_trace_operation_call_facts WHERE source_module<>'' AND (trace_id,conversation_id,interaction_id) IN (`+placeholders+`)) first_source
 ON f.operation_id=first_source.operation_id AND f.attempt_no=first_source.attempt_no
 WHERE first_source.source_rank=1
 ORDER BY f.trace_id,f.conversation_id,f.interaction_id`, args...)
	if err != nil {
		t.err = err
		return nil
	}
	defer func() { _ = rows.Close() }()
	result := []sessionvo.OperationCallFact{}
	for rows.Next() {
		var fact sessionvo.OperationCallFact
		if err := rows.Scan(&fact.TraceID, &fact.ConversationID, &fact.InteractionID,
			&fact.OperationID, &fact.Attempt, &fact.StartedAt, &fact.SourceModule); err != nil {
			t.err = err
			return nil
		}
		result = append(result, fact)
	}
	if err := rows.Err(); err != nil {
		t.err = err
		return nil
	}
	return result
}

func traceMetadataScopeSQL(scopes []isessionstore.TraceMetadataScope) (string, []any) {
	seen := map[isessionstore.TraceMetadataScope]bool{}
	args := []any{}
	for _, scope := range scopes {
		if scope.TraceID != "" && !seen[scope] {
			seen[scope] = true
			args = append(args, scope.TraceID, scope.ConversationID, scope.InteractionID)
		}
	}
	return strings.TrimSuffix(strings.Repeat("(?,?,?),", len(seen)), ","), args
}
