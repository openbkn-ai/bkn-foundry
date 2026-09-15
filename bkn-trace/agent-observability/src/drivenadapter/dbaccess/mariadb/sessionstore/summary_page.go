// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package sessionstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/isessionstore"
)

func (s *Store) ListTraceSummaryIdentities(ctx context.Context, query isessionstore.SummaryPageQuery) (isessionstore.SummaryIdentityPage, error) {
	where, args := summaryOwnerWhere("r", query)
	where = append(where, usableSummaryReceiptPredicates("r")...)
	if !query.From.IsZero() {
		where, args = append(where, "r.issued_at>=?"), append(args, query.From.UTC())
	}
	if !query.To.IsZero() {
		where, args = append(where, "r.issued_at<=?"), append(args, query.To.UTC())
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

func (s *Store) ListConversationSummaryIdentities(ctx context.Context, query isessionstore.SummaryPageQuery) (isessionstore.SummaryIdentityPage, error) {
	where, args := summaryOwnerWhere("c", query)
	excludedAgentPredicate, excludedAgentArgs := conversationSummaryExcludedAgentPredicate("c", query.ExcludeAgentOrApps)
	if excludedAgentPredicate != "" {
		where, args = append(where, excludedAgentPredicate), append(args, excludedAgentArgs...)
	}
	receiptExists, receiptArgs := conversationSummaryReceiptExists(query)
	where, args = append(where, receiptExists), append(args, receiptArgs...)
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_conversations c WHERE "+clause, args...).Scan(&total); err != nil {
		return isessionstore.SummaryIdentityPage{}, fmt.Errorf("count conversation summary identities: %w", err)
	}

	pageArgs := append([]any{}, args...)
	if query.AfterStartedAt != "" && query.AfterID != "" {
		startedAt, err := time.Parse(time.RFC3339Nano, query.AfterStartedAt)
		if err != nil {
			return isessionstore.SummaryIdentityPage{}, fmt.Errorf("parse conversation summary cursor: %w", err)
		}
		clause += " AND (c.created_at<? OR (c.created_at=? AND c.conversation_id>?))"
		pageArgs = append(pageArgs, startedAt.UTC(), startedAt.UTC(), query.AfterID)
	}
	limit := normalizeSummaryPageLimit(query.Limit)
	pageArgs = append(pageArgs, limit+1, normalizeSummaryPageOffset(query.Offset))
	rows, err := s.db.QueryContext(ctx, `SELECT c.conversation_id, c.created_at
		FROM bkn_trace_conversations c WHERE `+clause+`
		ORDER BY c.created_at DESC, c.conversation_id ASC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return isessionstore.SummaryIdentityPage{}, fmt.Errorf("list conversation summary identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	entries, err := scanSummaryIdentities(rows, limit)
	if err != nil {
		return isessionstore.SummaryIdentityPage{}, fmt.Errorf("scan conversation summary identities: %w", err)
	}
	return isessionstore.SummaryIdentityPage{Entries: entries.values, Total: total, HasMore: entries.hasMore}, nil
}

func conversationSummaryExcludedAgentPredicate(alias string, values []string) (string, []any) {
	unique := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	if len(unique) == 0 {
		return "", nil
	}

	asciiValues := make([]string, 0, len(unique))
	for _, value := range unique {
		if isASCII(value) {
			asciiValues = append(asciiValues, value)
		}
	}

	allPlaceholders := strings.TrimSuffix(strings.Repeat("?,", len(unique)), ",")
	predicates := []string{
		alias + ".agent_name IS NULL OR " + alias + ".agent_name NOT IN (" + allPlaceholders + ")",
	}
	args := stringsToAny(unique)
	if len(asciiValues) > 0 {
		asciiPlaceholders := strings.TrimSuffix(strings.Repeat(asciiBinaryParameter()+",", len(asciiValues)), ",")
		identityPredicates := []string{
			alias + ".application_principal_id IS NULL OR " + alias + ".application_principal_id NOT IN (" + asciiPlaceholders + ")",
			alias + ".effective_subject_id IS NULL OR " + alias + ".effective_subject_id NOT IN (" + asciiPlaceholders + ")",
		}
		predicates = append(predicates, identityPredicates...)
		for range identityPredicates {
			args = append(args, stringsToAny(asciiValues)...)
		}
	}
	return "(" + strings.Join(predicates, ") AND (") + ")", args
}

func isASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] > 0x7f {
			return false
		}
	}
	return true
}

func stringsToAny(values []string) []any {
	args := make([]any, len(values))
	for index := range values {
		args[index] = values[index]
	}
	return args
}

func conversationSummaryReceiptExists(query isessionstore.SummaryPageQuery) (string, []any) {
	where := []string{
		"r.conversation_id=c.conversation_id",
	}
	where = append(where, usableSummaryReceiptPredicates("r")...)
	args := make([]any, 0, 2)
	if !query.From.IsZero() {
		where, args = append(where, "r.issued_at>=?"), append(args, query.From.UTC())
	}
	if !query.To.IsZero() {
		where, args = append(where, "r.issued_at<=?"), append(args, query.To.UTC())
	}
	return "EXISTS (SELECT 1 FROM bkn_trace_receipts r WHERE " + strings.Join(where, " AND ") + ")", args
}

func usableSummaryReceiptPredicates(alias string) []string {
	return []string{
		alias + ".trace_id IS NOT NULL", alias + ".trace_id<>''",
		alias + ".request_id IS NOT NULL", alias + ".request_id<>''",
	}
}

func summaryOwnerWhere(alias string, query isessionstore.SummaryPageQuery) ([]string, []any) {
	scope := query.Scope
	where := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if scope.AccessProfile == nil {
		where = append(where,
			alias+".effective_subject_type="+asciiBinaryParameter(),
			alias+".effective_subject_id="+asciiBinaryParameter(),
		)
		args = append(args, scope.AccountType, scope.AccountID)
		return where, args
	}
	profile := *scope.AccessProfile
	if evidencevo.HasGlobalTraceAccess(profile) {
		return where, args
	}
	owner := make([]string, 0, 2)
	if profile.EffectiveSubjectID != "" {
		owner, args = append(owner, alias+".effective_subject_id="+asciiBinaryParameter()), append(args, profile.EffectiveSubjectID)
	}
	if profile.ApplicationPrincipalID != "" {
		owner, args = append(owner, alias+".application_principal_id="+asciiBinaryParameter()), append(args, profile.ApplicationPrincipalID)
	}
	if len(owner) > 0 {
		where = append(where, "("+strings.Join(owner, " OR ")+")")
	} else {
		where = append(where, "1=0")
	}
	return where, args
}

func asciiBinaryParameter() string {
	return "CONVERT(? USING ascii) COLLATE ascii_bin"
}

type scannedSummaryIdentities struct {
	values  []isessionstore.SummaryIdentity
	hasMore bool
}

func scanSummaryIdentities(rows *sql.Rows, limit int) (scannedSummaryIdentities, error) {
	values := make([]isessionstore.SummaryIdentity, 0, limit+1)
	for rows.Next() {
		var id string
		var startedAt time.Time
		if err := rows.Scan(&id, &startedAt); err != nil {
			return scannedSummaryIdentities{}, err
		}
		values = append(values, isessionstore.SummaryIdentity{ID: id, StartedAt: startedAt.UTC().Format(time.RFC3339Nano)})
	}
	if err := rows.Err(); err != nil {
		return scannedSummaryIdentities{}, err
	}
	hasMore := len(values) > limit
	if hasMore {
		values = values[:limit]
	}
	return scannedSummaryIdentities{values: values, hasMore: hasMore}, nil
}

func normalizeSummaryPageLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	return limit
}

func normalizeSummaryPageOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
