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
	where = append(where, "r.trace_id IS NOT NULL", "r.trace_id<>''")
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
	where = append(where, "EXISTS (SELECT 1 FROM bkn_trace_receipts r WHERE r.conversation_id=c.conversation_id)")
	if !query.From.IsZero() {
		where, args = append(where, "c.created_at>=?"), append(args, query.From.UTC())
	}
	if !query.To.IsZero() {
		where, args = append(where, "c.created_at<=?"), append(args, query.To.UTC())
	}
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

func summaryOwnerWhere(alias string, query isessionstore.SummaryPageQuery) ([]string, []any) {
	scope := query.Scope
	domain := scope.BusinessDomain
	where := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if scope.TenantID != "" {
		where, args = append(where, alias+".tenant_id=?"), append(args, scope.TenantID)
	}
	if domain != "" {
		where, args = append(where, alias+".business_domain_id=?"), append(args, domain)
	}
	if scope.AccessProfile == nil {
		where = append(where, alias+".effective_subject_type=?", alias+".effective_subject_id=?")
		args = append(args, scope.AccountType, scope.AccountID)
		return where, args
	}
	profile := *scope.AccessProfile
	if evidencevo.HasTenantWideTraceAccess(profile) {
		return where, args
	}
	owner := make([]string, 0, 2)
	if profile.EffectiveSubjectID != "" {
		owner, args = append(owner, alias+".effective_subject_id=?"), append(args, profile.EffectiveSubjectID)
	}
	if profile.ApplicationPrincipalID != "" {
		owner, args = append(owner, alias+".application_principal_id=?"), append(args, profile.ApplicationPrincipalID)
	}
	if len(owner) > 0 {
		where = append(where, "("+strings.Join(owner, " OR ")+")")
	} else {
		where = append(where, "1=0")
	}
	return where, args
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
