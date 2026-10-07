// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/evidencevo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/observabilityvo"
)

// QueryLogPage counts the filtered monthly ledgers, then reads only the target
// slice. Months have disjoint occurred_at ranges, so descending month order is
// also global time order. Database round trips scale with the bounded time window, not Page;
// a deep OFFSET still costs work inside the database.
func (reader *Reader) QueryLogPage(ctx context.Context, query observabilityvo.LogQuery, profile evidencevo.AccessProfile) (auditsvc.Page, int64, error) {
	if query.TimeFrom == nil || query.TimeTo == nil || query.TimeTo.Sub(*query.TimeFrom) > 30*24*time.Hour || query.Limit <= 0 || query.Limit > 200 || query.Page < 1 {
		return auditsvc.Page{}, 0, errors.New("invalid numbered log query")
	}
	if q := query.PageBefore; q != nil {
		query.Page = 1
	}
	if len(query.AuthorizedCategories) == 0 {
		return auditsvc.Page{Records: []auditsvc.Record{}}, 0, nil
	}
	tables, err := auditQueryTables(*query.TimeFrom, *query.TimeTo)
	if err != nil {
		return auditsvc.Page{}, 0, err
	}
	type monthCount struct {
		table string
		count int64
		where string
		args  []any
	}
	months := make([]monthCount, 0, len(tables))
	var total int64
	for index := len(tables) - 1; index >= 0; index-- {
		where, args := logPageWhere(query, profile)
		table := tables[index]
		var count int64
		err := reader.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_audit."+table+where, args...).Scan(&count)
		if isMissingMonthlyTable(err) {
			continue
		}
		if err != nil {
			return auditsvc.Page{}, 0, fmt.Errorf("count Audit ledger %s: %w", table, err)
		}
		total += count
		months = append(months, monthCount{table, count, where, args})
	}
	page := auditsvc.Page{Records: []auditsvc.Record{}}
	// Compare before multiplying, including MaxInt page numbers.
	if total == 0 || int64(query.Page-1) > (total-1)/int64(query.Limit) {
		return page, total, nil
	}
	offset := int64(query.Page-1) * int64(query.Limit)
	for _, month := range months {
		if offset >= month.count {
			offset -= month.count
			continue
		}
		remaining := query.Limit + 1 - len(page.Records)
		statement := "SELECT event_id, source_id, payload, occurred_at, broker_received_at, recorded_at FROM bkn_audit." + month.table + month.where + " ORDER BY occurred_at DESC, event_id DESC LIMIT ? OFFSET ?"
		args := append(append([]any(nil), month.args...), remaining, offset)
		rows, err := reader.db.QueryContext(ctx, statement, args...)
		if err != nil {
			return auditsvc.Page{}, 0, fmt.Errorf("page Audit ledger %s: %w", month.table, err)
		}
		for rows.Next() {
			var eventID, sourceID string
			var payload []byte
			var occurred, observed, recorded time.Time
			if err := rows.Scan(&eventID, &sourceID, &payload, &occurred, &observed, &recorded); err != nil {
				_ = rows.Close()
				return auditsvc.Page{}, 0, err
			}
			record, err := decodeAuditRecord(eventID, sourceID, occurred.UTC(), observed.UTC(), recorded.UTC(), payload)
			if err != nil {
				_ = rows.Close()
				return auditsvc.Page{}, 0, err
			}
			page.Records = append(page.Records, record)
		}
		rowsErr := rows.Err()
		closeErr := rows.Close()
		if rowsErr != nil {
			return auditsvc.Page{}, 0, rowsErr
		}
		if closeErr != nil {
			return auditsvc.Page{}, 0, closeErr
		}
		if len(page.Records) > query.Limit {
			break
		}
		offset = 0
	}
	if len(page.Records) > query.Limit {
		page.Records = page.Records[:query.Limit]
		last := page.Records[len(page.Records)-1]
		page.Next = &auditsvc.Position{OccurredAt: last.OccurredAt, EventID: last.EventID}
	}
	return page, total, nil
}

// logPageWhere applies the same public filters and per-record authorization as
// logsvc before both COUNT and OFFSET. In particular, actor-name searches and
// network-builder scope cannot be applied after slicing a raw page.
func logPageWhere(q observabilityvo.LogQuery, profile evidencevo.AccessProfile) (string, []any) {
	query := auditsvc.Query{Categories: q.AuthorizedCategories, From: *q.TimeFrom, To: *q.TimeTo, SourceID: q.SourceID, BusinessModule: q.BusinessModule, ActorID: q.ActorID, TargetType: q.TargetType, TargetID: q.TargetID, Action: q.Action, Outcomes: q.Outcomes}
	if q.ObservedBefore != nil {
		query.ObservedBefore = *q.ObservedBefore
	}
	if q.PageBefore != nil {
		query.Cursor = &auditsvc.Position{OccurredAt: q.PageBefore.EventTimestamp, EventID: q.PageBefore.LogID}
	}
	selectSQL, args := auditSelect("unused", query)
	where := selectSQL[strings.Index(selectSQL, " WHERE "):strings.Index(selectSQL, " ORDER BY ")]
	args = args[:len(args)-1]
	jsonValue := func(path string) string { return "JSON_UNQUOTE(JSON_EXTRACT(payload, '" + path + "'))" }
	equal := func(path, value string) {
		if value != "" {
			where += " AND " + jsonValue(path) + " = ?"
			args = append(args, value)
		}
	}
	set := func(expr string, values []string) {
		if len(values) > 0 {
			where += " AND " + expr + " IN (" + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + ")"
			for _, v := range values {
				args = append(args, v)
			}
		}
	}
	equal("$.scope.application_id", q.ApplicationID)
	equal("$.correlation.trace_id", q.TraceID)
	equal("$.correlation.request_id", q.RequestID)
	equal("$.facts.operation_id", q.OperationID)
	// The audit projection has no span/conversation/interaction identity.
	if q.SpanID != "" || q.ConversationID != "" || q.InteractionID != "" || q.SeverityMinimum > 9 {
		where += " AND 1=0"
	}
	set("source_id", q.Services)
	set(jsonValue("$.scope.environment"), q.Environments)
	set(jsonValue("$.event_name"), q.EventNames)
	if q.FailedOnly {
		where += " AND " + jsonValue("$.outcome") + " IN ('failure','denied')"
	}
	if q.Query != "" {
		where += " AND LOCATE(LOWER(?), LOWER(" + jsonValue("$.summary") + ")) > 0"
		args = append(args, q.Query)
	}
	if actor := strings.TrimSpace(q.ActorQuery); actor != "" {
		where += " AND (" + jsonValue("$.actor.id") + " = ? OR LOCATE(LOWER(?), LOWER(" + jsonValue("$.actor.display_name_snapshot") + ")) > 0)"
		args = append(args, actor, actor)
	}
	// Only valid operation-audit projections are part of the numbered dataset.
	for _, path := range []string{"$.event_name", "$.scope.environment", "$.facts.action", "$.target.type", "$.target.id", "$.target.name", "$.actor.id", "$.actor.display_name_snapshot", "$.actor.auth_method", "$.request_context.source_channel"} {
		where += " AND COALESCE(" + jsonValue(path) + ", '') <> ''"
	}
	where += " AND source_id <> '' AND event_id <> '' AND OCTET_LENGTH(COALESCE(" + jsonValue("$.summary") + ", '')) <= 2048"
	where += " AND " + jsonValue("$.outcome") + " IN ('success','failure','denied','canceled','unknown')"
	// Values are the fixed server-owned module vocabulary, never request input.
	where += " AND " + jsonValue("$.scope.business_module") + " IN ('" + strings.Join(observabilityvo.AllBusinessModules, "','") + "')"
	hasRole := func(role string) bool {
		for _, r := range profile.Roles {
			if r == role {
				return true
			}
		}
		return false
	}
	if q.IsAssociatedDrilldown() && !observabilityvo.CapabilitiesFor(profile).GlobalLogSearch {
		where += " AND " + jsonValue("$.actor.id") + " = ?"
		args = append(args, profile.EffectiveSubjectID)
	} else if !hasRole("admin") && !hasRole("super_admin") {
		clauses := []string{jsonValue("$.actor.id") + " = ?"}
		args = append(args, profile.EffectiveSubjectID)
		if profile.AccountActive {
			if profile.EffectiveSubjectID != "" {
				clauses = append(clauses, jsonValue("$.actor.effective_subject")+" = ?")
				args = append(args, profile.EffectiveSubjectID)
			}
			if profile.ApplicationPrincipalID != "" {
				clauses = append(clauses, jsonValue("$.scope.application_id")+" = ?")
				args = append(args, profile.ApplicationPrincipalID)
			}
			if hasRole("network_builder") {
				for _, network := range profile.ManagedKnowledgeNetworkIDs {
					if network != "" {
						clauses = append(clauses, "JSON_CONTAINS(JSON_EXTRACT(payload, '$.scope.knowledge_network_ids'), JSON_QUOTE(?))")
						args = append(args, network)
					}
				}
			}
		}
		where += " AND (" + strings.Join(clauses, " OR ") + ")"
	}
	return where, args
}
