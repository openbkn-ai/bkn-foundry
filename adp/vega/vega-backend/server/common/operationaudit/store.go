// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

// Package operationaudit persists bounded data-resource management facts.
package operationaudit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	libdb "github.com/openbkn-ai/bkn-foundry/comm-go/db"

	"vega-backend/common"
)

const tableName = "t_vega_operation_audit"

var (
	storeOnce sync.Once
	store     *Store
)

type Entry struct {
	EventID        string
	EventTime      time.Time
	RecordedAt     time.Time
	ActorID        string
	ActorName      string
	ActorType      string
	AuthMethod     string
	RequestID      string
	SourceChannel  string
	Method         string
	Action         string
	TargetType     string
	TargetID       string
	TargetName     string
	Outcome        string
	FailureCode    string
	FailureMessage string
}

type Filter struct {
	From, To      time.Time
	BeforeTime    time.Time
	BeforeEventID string
	Limit         int
	ActorID       string
	Action        string
	TargetType    string
	TargetID      string
	Outcome       string
}

type Page struct {
	Entries []Entry
	HasMore bool
}

type Store struct{ db *sql.DB }

func NewStore(appSetting *common.AppSetting) *Store {
	storeOnce.Do(func() { store = &Store{db: libdb.NewDB(&appSetting.DBSetting)} })
	return store
}

func EventID(requestID, method, path string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{requestID, strings.ToUpper(method), path}, "\n")))
	return "evt_" + hex.EncodeToString(sum[:])
}

func (s *Store) Record(ctx context.Context, entry Entry) error {
	if s == nil || s.db == nil {
		return errors.New("operation audit store is not configured")
	}
	if err := validate(entry); err != nil {
		return err
	}
	arguments := []any{
		entry.EventID, entry.EventTime, entry.RecordedAt,
		entry.ActorID, entry.ActorName, entry.ActorType, entry.AuthMethod, entry.RequestID,
		entry.SourceChannel, entry.Method, entry.Action, entry.TargetType, entry.TargetID,
		entry.TargetName, entry.Outcome, entry.FailureCode, entry.FailureMessage,
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO "+tableName+" (event_id,event_time,recorded_at,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE event_id=VALUES(event_id)",
		arguments...,
	)
	if err != nil {
		return fmt.Errorf("record operation audit: %w", err)
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
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	query := "SELECT event_id,event_time,recorded_at,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM " + tableName + " WHERE event_time>=? AND event_time<?"
	args := []any{filter.From, filter.To}
	for _, condition := range []struct{ column, value string }{
		{"actor_id", filter.ActorID}, {"action", filter.Action}, {"target_type", filter.TargetType}, {"target_id", filter.TargetID}, {"outcome", filter.Outcome},
	} {
		if strings.TrimSpace(condition.value) != "" {
			query += " AND " + condition.column + "=?"
			args = append(args, strings.TrimSpace(condition.value))
		}
	}
	if !filter.BeforeTime.IsZero() {
		if filter.BeforeEventID == "" {
			return Page{}, errors.New("cursor event id is required")
		}
		query += " AND (event_time < ? OR (event_time = ? AND event_id > ?))"
		args = append(args, filter.BeforeTime, filter.BeforeTime, filter.BeforeEventID)
	}
	query += " ORDER BY event_time DESC,event_id ASC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("list operation audit: %w", err)
	}
	defer rows.Close()
	entries := make([]Entry, 0, limit+1)
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.EventID, &entry.EventTime, &entry.RecordedAt, &entry.ActorID, &entry.ActorName, &entry.ActorType, &entry.AuthMethod, &entry.RequestID, &entry.SourceChannel, &entry.Method, &entry.Action, &entry.TargetType, &entry.TargetID, &entry.TargetName, &entry.Outcome, &entry.FailureCode, &entry.FailureMessage); err != nil {
			return Page{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	page := Page{Entries: entries}
	if len(page.Entries) > limit {
		page.HasMore = true
		page.Entries = page.Entries[:limit]
	}
	return page, nil
}

func (s *Store) Get(ctx context.Context, eventID string) (Entry, bool, error) {
	if s == nil || s.db == nil {
		return Entry{}, false, errors.New("operation audit store is not configured")
	}
	if strings.TrimSpace(eventID) == "" {
		return Entry{}, false, errors.New("event id is required")
	}
	row := s.db.QueryRowContext(ctx, "SELECT event_id,event_time,recorded_at,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM "+tableName+" WHERE event_id=?", eventID)
	var entry Entry
	err := row.Scan(&entry.EventID, &entry.EventTime, &entry.RecordedAt, &entry.ActorID, &entry.ActorName, &entry.ActorType, &entry.AuthMethod, &entry.RequestID, &entry.SourceChannel, &entry.Method, &entry.Action, &entry.TargetType, &entry.TargetID, &entry.TargetName, &entry.Outcome, &entry.FailureCode, &entry.FailureMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("get operation audit: %w", err)
	}
	return entry, true, nil
}

func validate(entry Entry) error {
	for _, value := range []struct{ name, value string }{{"event id", entry.EventID}, {"actor id", entry.ActorID}, {"actor name", entry.ActorName}, {"request id", entry.RequestID}, {"action", entry.Action}, {"target type", entry.TargetType}, {"target id", entry.TargetID}, {"outcome", entry.Outcome}} {
		if strings.TrimSpace(value.value) == "" {
			return fmt.Errorf("%s is required", value.name)
		}
	}
	if entry.EventTime.IsZero() || entry.RecordedAt.IsZero() {
		return errors.New("event and recorded time are required")
	}
	if entry.Outcome != "success" && entry.Outcome != "failure" && entry.Outcome != "denied" {
		return errors.New("outcome is invalid")
	}
	if len(entry.FailureMessage) > 512 || len(entry.TargetID) > 1024 || len(entry.TargetName) > 1024 {
		return errors.New("operation audit fact exceeds bounded field size")
	}
	return nil
}
