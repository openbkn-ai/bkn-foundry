// Package operationaudit persists the bounded management facts emitted by
// Execution Factory. Runtime calls are intentionally not represented here.
package operationaudit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/comm-go/db/sqlx"
)

const tableName = "t_execution_factory_operation_audit"

type Entry struct {
	EventID          string    `json:"event_id"`
	EventTime        time.Time `json:"event_time"`
	RecordedAt       time.Time `json:"recorded_at"`
	TenantID         string    `json:"tenant_id"`
	BusinessDomainID string    `json:"business_domain_id"`
	ActorID          string    `json:"actor_id"`
	ActorName        string    `json:"actor_name"`
	ActorType        string    `json:"actor_type"`
	AuthMethod       string    `json:"auth_method"`
	RequestID        string    `json:"request_id"`
	SourceChannel    string    `json:"source_channel"`
	Method           string    `json:"method"`
	Action           string    `json:"action"`
	TargetType       string    `json:"target_type"`
	TargetID         string    `json:"target_id"`
	TargetName       string    `json:"target_name"`
	Outcome          string    `json:"outcome"`
	FailureCode      string    `json:"failure_code"`
	FailureMessage   string    `json:"failure_message"`
}

type Store struct{ db *sqlx.DB }

func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

func EventID(tenantID, requestID, method, path string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tenantID, requestID, strings.ToUpper(method), path}, "\n")))
	return "evt_" + hex.EncodeToString(sum[:])
}

func (s *Store) Record(ctx context.Context, entry Entry) error {
	if s == nil || s.db == nil {
		return errors.New("operation audit store is not configured")
	}
	if err := validate(entry); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO "+tableName+" (event_id,event_time,recorded_at,tenant_id,business_domain_id,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE event_id=VALUES(event_id)",
		entry.EventID, entry.EventTime, entry.RecordedAt, entry.TenantID, entry.BusinessDomainID, entry.ActorID, entry.ActorName, entry.ActorType, entry.AuthMethod, entry.RequestID, entry.SourceChannel, entry.Method, entry.Action, entry.TargetType, entry.TargetID, entry.TargetName, entry.Outcome, entry.FailureCode, entry.FailureMessage)
	if err != nil {
		return fmt.Errorf("record execution factory operation audit: %w", err)
	}
	return nil
}

type Filter struct {
	TenantID, BusinessDomain string
	From, To, BeforeTime     time.Time
	BeforeEventID            string
	Limit                    int
	ActorID, Action          string
	TargetType, TargetID     string
	Outcome                  string
}
type Page struct {
	Entries []Entry
	HasMore bool
}

func (s *Store) List(ctx context.Context, filter Filter) (Page, error) {
	if s == nil || s.db == nil {
		return Page{}, errors.New("operation audit store is not configured")
	}
	if filter.TenantID == "" || filter.BusinessDomain == "" || filter.From.IsZero() || filter.To.IsZero() || !filter.From.Before(filter.To) {
		return Page{}, errors.New("scope and valid time range are required")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	query := "SELECT event_id,event_time,recorded_at,tenant_id,business_domain_id,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM " + tableName + " WHERE tenant_id=? AND business_domain_id=? AND event_time>=? AND event_time<?"
	args := []any{filter.TenantID, filter.BusinessDomain, filter.From, filter.To}
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
		return Page{}, fmt.Errorf("list execution factory operation audit: %w", err)
	}
	defer rows.Close()
	page := Page{Entries: make([]Entry, 0, limit+1)}
	for rows.Next() {
		var item Entry
		if err := rows.Scan(&item.EventID, &item.EventTime, &item.RecordedAt, &item.TenantID, &item.BusinessDomainID, &item.ActorID, &item.ActorName, &item.ActorType, &item.AuthMethod, &item.RequestID, &item.SourceChannel, &item.Method, &item.Action, &item.TargetType, &item.TargetID, &item.TargetName, &item.Outcome, &item.FailureCode, &item.FailureMessage); err != nil {
			return Page{}, err
		}
		page.Entries = append(page.Entries, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Entries) > limit {
		page.HasMore = true
		page.Entries = page.Entries[:limit]
	}
	return page, nil
}

func (s *Store) Get(ctx context.Context, eventID, tenantID, businessDomain string) (Entry, bool, error) {
	if s == nil || s.db == nil {
		return Entry{}, false, errors.New("operation audit store is not configured")
	}
	var item Entry
	err := s.db.QueryRowContext(ctx, "SELECT event_id,event_time,recorded_at,tenant_id,business_domain_id,actor_id,actor_name,actor_type,auth_method,request_id,source_channel,method,action,target_type,target_id,target_name,outcome,failure_code,failure_message FROM "+tableName+" WHERE event_id=? AND tenant_id=? AND business_domain_id=?", eventID, tenantID, businessDomain).Scan(&item.EventID, &item.EventTime, &item.RecordedAt, &item.TenantID, &item.BusinessDomainID, &item.ActorID, &item.ActorName, &item.ActorType, &item.AuthMethod, &item.RequestID, &item.SourceChannel, &item.Method, &item.Action, &item.TargetType, &item.TargetID, &item.TargetName, &item.Outcome, &item.FailureCode, &item.FailureMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return item, true, nil
}

func validate(entry Entry) error {
	for _, field := range []struct{ name, value string }{{"event id", entry.EventID}, {"tenant", entry.TenantID}, {"business domain", entry.BusinessDomainID}, {"actor", entry.ActorID}, {"actor name", entry.ActorName}, {"request id", entry.RequestID}, {"action", entry.Action}, {"target type", entry.TargetType}, {"target id", entry.TargetID}, {"outcome", entry.Outcome}} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if entry.EventTime.IsZero() || entry.RecordedAt.IsZero() {
		return errors.New("event and recorded time are required")
	}
	if entry.Outcome != "success" && entry.Outcome != "failure" && entry.Outcome != "denied" {
		return errors.New("outcome is invalid")
	}
	if len(entry.TargetID) > 1024 || len(entry.TargetName) > 1024 || len(entry.FailureMessage) > 512 {
		return errors.New("operation audit fact exceeds bounded field size")
	}
	return nil
}
