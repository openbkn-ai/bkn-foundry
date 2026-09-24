// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package auditstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/auditsvc"
)

// Reader is the read-only MariaDB adapter for the center Audit ledger. Table
// names are derived exclusively from the validated UTC query window.
type Reader struct{ db *sql.DB }

func NewReader(db *sql.DB) (*Reader, error) {
	if db == nil {
		return nil, errors.New("audit reader database is required")
	}
	return &Reader{db: db}, nil
}

func (reader *Reader) Query(ctx context.Context, query auditsvc.Query) (auditsvc.Page, error) {
	tables, err := auditQueryTables(query.From, query.To)
	if err != nil {
		return auditsvc.Page{}, err
	}
	records := make([]auditsvc.Record, 0, query.Limit+1)
	for _, table := range tables {
		statement, args := auditSelect(table, query)
		rows, err := reader.db.QueryContext(ctx, statement, args...)
		if err != nil {
			return auditsvc.Page{}, fmt.Errorf("query Audit ledger %s: %w", table, err)
		}
		for rows.Next() {
			var eventID, sourceID string
			var payload []byte
			var occurredAt time.Time
			if err := rows.Scan(&eventID, &sourceID, &payload, &occurredAt); err != nil {
				_ = rows.Close()
				return auditsvc.Page{}, fmt.Errorf("scan Audit ledger row: %w", err)
			}
			record, err := decodeAuditRecord(eventID, sourceID, occurredAt.UTC(), payload)
			if err != nil {
				_ = rows.Close()
				return auditsvc.Page{}, err
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return auditsvc.Page{}, fmt.Errorf("iterate Audit ledger rows: %w", err)
		}
		if err := rows.Close(); err != nil {
			return auditsvc.Page{}, fmt.Errorf("close Audit ledger rows: %w", err)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].OccurredAt.Equal(records[j].OccurredAt) {
			return records[i].EventID > records[j].EventID
		}
		return records[i].OccurredAt.After(records[j].OccurredAt)
	})
	page := auditsvc.Page{Records: records}
	if len(records) > query.Limit {
		page.Records = records[:query.Limit]
		last := page.Records[len(page.Records)-1]
		page.Next = &auditsvc.Position{OccurredAt: last.OccurredAt, EventID: last.EventID}
	}
	return page, nil
}

func auditQueryTables(from, to time.Time) ([]string, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return nil, errors.New("Audit query window is invalid")
	}
	month := time.Date(from.UTC().Year(), from.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(to.UTC().Year(), to.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	tables := make([]string, 0, 3)
	for !month.After(last) {
		table := "audit_event_" + month.Format("200601")
		if !tableNamePattern.MatchString(table) {
			return nil, ErrInvalidMonth
		}
		tables = append(tables, table)
		month = month.AddDate(0, 1, 0)
	}
	return tables, nil
}

func auditSelect(table string, query auditsvc.Query) (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(query.Categories)), ",")
	statement := "SELECT event_id, source_id, payload, occurred_at FROM bkn_audit." + table +
		" WHERE occurred_at >= ? AND occurred_at < ? AND JSON_UNQUOTE(JSON_EXTRACT(payload, '$.category')) IN (" + placeholders + ")"
	args := make([]any, 0, 2+len(query.Categories)+10)
	args = append(args, query.From.UTC(), query.To.UTC())
	for _, category := range query.Categories {
		args = append(args, category)
	}
	addJSONFilter := func(path, value string) {
		if value != "" {
			statement += " AND JSON_UNQUOTE(JSON_EXTRACT(payload, '" + path + "')) = ?"
			args = append(args, value)
		}
	}
	if query.SourceID != "" {
		statement += " AND source_id = ?"
		args = append(args, query.SourceID)
	}
	addJSONFilter("$.scope.business_module", query.BusinessModule)
	addJSONFilter("$.actor.id", query.ActorID)
	addJSONFilter("$.target.type", query.TargetType)
	addJSONFilter("$.target.id", query.TargetID)
	addJSONFilter("$.facts.action", query.Action)
	addJSONFilter("$.outcome", query.Outcome)
	if query.Cursor != nil {
		statement += " AND (occurred_at < ? OR (occurred_at = ? AND event_id < ?))"
		args = append(args, query.Cursor.OccurredAt.UTC(), query.Cursor.OccurredAt.UTC(), query.Cursor.EventID)
	}
	statement += " ORDER BY occurred_at DESC, event_id DESC LIMIT ?"
	args = append(args, query.Limit+1)
	return statement, args
}

type storedAuditPayload struct {
	EventID   string `json:"event_id"`
	SourceID  string `json:"source_id"`
	Category  string `json:"category"`
	EventName string `json:"event_name"`
	Occurred  string `json:"occurred_at"`
	Actor     struct {
		ID string `json:"id"`
	} `json:"actor"`
	Target struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"target"`
	Scope struct {
		BusinessModule string `json:"business_module"`
	} `json:"scope"`
	Facts struct {
		Action string `json:"action"`
	} `json:"facts"`
	Outcome string `json:"outcome"`
}

func decodeAuditRecord(eventID, sourceID string, occurredAt time.Time, payload []byte) (auditsvc.Record, error) {
	var stored storedAuditPayload
	if err := json.Unmarshal(payload, &stored); err != nil {
		return auditsvc.Record{}, fmt.Errorf("decode Audit ledger payload: %w", err)
	}
	if stored.EventID != eventID || stored.SourceID != sourceID || stored.Category == "" || stored.EventName == "" || stored.Actor.ID == "" || stored.Target.Type == "" || stored.Target.ID == "" || stored.Scope.BusinessModule == "" || stored.Outcome == "" {
		return auditsvc.Record{}, errors.New("Audit ledger payload does not match its stored record")
	}
	parsedOccurredAt, err := time.Parse(time.RFC3339Nano, stored.Occurred)
	if err != nil || !parsedOccurredAt.UTC().Equal(occurredAt.UTC()) {
		return auditsvc.Record{}, errors.New("Audit ledger payload occurred_at does not match its stored record")
	}
	return auditsvc.Record{EventID: eventID, OccurredAt: occurredAt.UTC(), SourceID: sourceID, Category: stored.Category,
		EventName: stored.EventName, ActorID: stored.Actor.ID, TargetType: stored.Target.Type, TargetID: stored.Target.ID,
		BusinessModule: stored.Scope.BusinessModule, Action: stored.Facts.Action, Outcome: stored.Outcome}, nil
}
