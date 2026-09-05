// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	tableName          = "t_operation_audit"
	currentSchema      = "1.0"
	defaultPageSize    = 50
	maximumPageSize    = 500
	maximumRequestID   = 128
	maximumSummarySize = 4096
)

var (
	allowedActions = map[string]struct{}{
		"create": {}, "update": {}, "delete": {}, "import": {},
		"add_members": {}, "remove_members": {}, "enable": {}, "disable": {},
		"attach": {}, "detach": {},
	}
	allowedTargetTypes = map[string]struct{}{
		"knowledge_network": {}, "object_type": {}, "relation_type": {}, "action_type": {},
		"metric": {}, "risk_type": {}, "concept_group": {}, "action_schedule": {},
		"kn_capability_binding": {},
	}
	allowedOutcomes    = map[string]struct{}{"success": {}, "failure": {}, "denied": {}}
	allowedSummaryKeys = map[string]struct{}{"changed_fields": {}, "status": {}}
)

// Entry is one bounded management fact emitted by BKN Backend.
// It intentionally excludes request bodies, response bodies and credentials.
type Entry struct {
	EventID            string
	EventTime          time.Time
	RecordedAt         time.Time
	KnowledgeNetworkID string
	ActorID            string
	ActorName          string
	ActorType          string
	AuthMethod         string
	CredentialID       string
	RequestID          string
	SourceChannel      string
	Method             string
	Action             string
	TargetType         string
	TargetID           string
	TargetName         string
	Outcome            string
	FailureCode        string
	FailureMessage     string
	ChangeSummary      map[string]any
	SchemaVersion      string
}

type Filter struct {
	KnowledgeNetworkIDs []string
	ActorID             string
	Action              string
	TargetType          string
	TargetID            string
	Outcome             string
	From                time.Time
	To                  time.Time
	BeforeTime          time.Time
	BeforeEventID       string
	Limit               int
}

type Scope struct {
	KnowledgeNetworkIDs []string
	ActorID             string
}

type Page struct {
	Entries           []Entry
	HasMore           bool
	NextBeforeTime    time.Time
	NextBeforeEventID string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB, _ string) *Store {
	return &Store{db: db}
}

func (s *Store) Record(ctx context.Context, entry Entry) error {
	if s == nil || s.db == nil {
		return errors.New("operation audit store is not configured")
	}
	if err := validateEntry(entry); err != nil {
		return err
	}
	if entry.SchemaVersion == "" {
		entry.SchemaVersion = currentSchema
	}
	summary, err := json.Marshal(entry.ChangeSummary)
	if err != nil {
		return fmt.Errorf("encode change summary: %w", err)
	}
	if string(summary) == "null" {
		summary = []byte("{}")
	}
	if len(summary) > maximumSummarySize {
		return fmt.Errorf("change summary exceeds %d bytes", maximumSummarySize)
	}

	query := "INSERT INTO " + tableName + " (event_id,event_time,recorded_at,knowledge_network_id,actor_id,actor_name,actor_type,auth_method,credential_id,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message,change_summary,schema_version) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE event_id = VALUES(event_id)"
	arguments := []any{
		entry.EventID, entry.EventTime, entry.RecordedAt, entry.KnowledgeNetworkID,
		entry.ActorID, entry.ActorName, entry.ActorType, entry.AuthMethod, entry.CredentialID, entry.RequestID, entry.SourceChannel,
		entry.Method, entry.Action, entry.TargetType, entry.TargetID, entry.TargetName, entry.Outcome, entry.FailureCode,
		entry.FailureMessage, string(summary), entry.SchemaVersion,
	}
	_, err = s.db.ExecContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("insert operation audit event: %w", err)
	}
	return nil
}

func (s *Store) List(ctx context.Context, filter Filter) (Page, error) {
	if s == nil || s.db == nil {
		return Page{}, errors.New("operation audit store is not configured")
	}
	if filter.From.IsZero() || filter.To.IsZero() || !filter.From.Before(filter.To) {
		return Page{}, errors.New("valid time range is required")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maximumPageSize {
		limit = maximumPageSize
	}

	query := "SELECT " + selectColumns + " FROM " + tableName + " WHERE event_time >= ? AND event_time < ?"
	args := []any{filter.From, filter.To}
	networkIDs := normalizedIDs(filter.KnowledgeNetworkIDs)
	if len(networkIDs) > 0 {
		query += " AND knowledge_network_id IN (" + placeholders(len(networkIDs)) + ")"
		for _, networkID := range networkIDs {
			args = append(args, networkID)
		}
	}
	if filter.ActorID != "" {
		query += " AND actor_id = ?"
		args = append(args, filter.ActorID)
	}
	for _, criterion := range []struct {
		column string
		value  string
	}{
		{"action", filter.Action}, {"target_type", filter.TargetType},
		{"target_id", filter.TargetID}, {"outcome", filter.Outcome},
	} {
		if criterion.value != "" {
			query += " AND " + criterion.column + " = ?"
			args = append(args, criterion.value)
		}
	}
	if !filter.BeforeTime.IsZero() {
		if filter.BeforeEventID == "" {
			return Page{}, errors.New("cursor event id is required with cursor time")
		}
		query += " AND (event_time < ? OR (event_time = ? AND event_id > ?))"
		args = append(args, filter.BeforeTime, filter.BeforeTime, filter.BeforeEventID)
	}
	query += " ORDER BY event_time DESC, event_id ASC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("query operation audit events: %w", err)
	}
	defer rows.Close()

	entries := make([]Entry, 0, limit+1)
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return Page{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate operation audit events: %w", err)
	}

	page := Page{Entries: entries}
	if len(entries) > limit {
		page.HasMore = true
		page.Entries = entries[:limit]
	}
	if page.HasMore && len(page.Entries) > 0 {
		last := page.Entries[len(page.Entries)-1]
		page.NextBeforeTime = last.EventTime
		page.NextBeforeEventID = last.EventID
	}
	return page, nil
}

func (s *Store) Get(ctx context.Context, eventID string, scope Scope) (Entry, bool, error) {
	if s == nil || s.db == nil {
		return Entry{}, false, errors.New("operation audit store is not configured")
	}
	if eventID == "" {
		return Entry{}, false, errors.New("event id is required")
	}
	query := "SELECT " + selectColumns + " FROM " + tableName + " WHERE event_id = ?"
	args := []any{eventID}
	networkIDs := normalizedIDs(scope.KnowledgeNetworkIDs)
	if len(networkIDs) > 0 {
		query += " AND knowledge_network_id IN (" + placeholders(len(networkIDs)) + ")"
		for _, networkID := range networkIDs {
			args = append(args, networkID)
		}
	}
	if scope.ActorID != "" {
		query += " AND actor_id = ?"
		args = append(args, scope.ActorID)
	}
	entry, err := scanEntry(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func validateEntry(entry Entry) error {
	if entry.EventID == "" || entry.EventTime.IsZero() || entry.RecordedAt.IsZero() ||
		entry.KnowledgeNetworkID == "" || entry.ActorID == "" || entry.ActorName == "" ||
		entry.RequestID == "" || entry.TargetID == "" || entry.TargetName == "" {
		return errors.New("operation audit entry is missing required fields")
	}
	if _, ok := allowedActions[entry.Action]; !ok {
		return fmt.Errorf("unregistered action %q", entry.Action)
	}
	if _, ok := allowedTargetTypes[entry.TargetType]; !ok {
		return fmt.Errorf("unregistered target type %q", entry.TargetType)
	}
	if _, ok := allowedOutcomes[entry.Outcome]; !ok {
		return fmt.Errorf("invalid outcome %q", entry.Outcome)
	}
	if len(entry.RequestID) > maximumRequestID || !printableASCII(entry.RequestID) {
		return errors.New("request id is not bounded printable ASCII")
	}
	for key := range entry.ChangeSummary {
		if _, ok := allowedSummaryKeys[key]; !ok {
			return fmt.Errorf("change summary key %q is not allowed", key)
		}
	}
	return nil
}

func printableASCII(value string) bool {
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func normalizedIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

const selectColumns = "event_id,event_time,recorded_at,knowledge_network_id,actor_id,actor_name,actor_type,auth_method,credential_id,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message,change_summary,schema_version"

type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(row scanner) (Entry, error) {
	var entry Entry
	var summary []byte
	err := row.Scan(
		&entry.EventID,
		&entry.EventTime,
		&entry.RecordedAt,
		&entry.KnowledgeNetworkID,
		&entry.ActorID,
		&entry.ActorName,
		&entry.ActorType,
		&entry.AuthMethod,
		&entry.CredentialID,
		&entry.RequestID,
		&entry.SourceChannel,
		&entry.Method,
		&entry.Action,
		&entry.TargetType,
		&entry.TargetID,
		&entry.TargetName,
		&entry.Outcome,
		&entry.FailureCode,
		&entry.FailureMessage,
		&summary,
		&entry.SchemaVersion,
	)
	if err != nil {
		return Entry{}, err
	}
	if len(summary) > 0 {
		if err := json.Unmarshal(summary, &entry.ChangeSummary); err != nil {
			return Entry{}, fmt.Errorf("decode operation audit change summary: %w", err)
		}
	}
	return entry, nil
}
