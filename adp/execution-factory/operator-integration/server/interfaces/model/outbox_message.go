package model

import (
	"context"
	"database/sql"
)

// OutboxMessageDB outbox message table.
type OutboxMessageDB struct {
	ID          int64  `json:"id" db:"f_id"`                       // primary key.
	EventID     string `json:"event_id" db:"f_event_id"`           // Event ID.
	EventType   string `json:"type" db:"f_event_type"`             // event type.
	Topic       string `json:"topic" db:"f_topic"`                 // Message Topic.
	Payload     string `json:"payload" db:"f_payload"`             // Event payload content (message)
	Status      string `json:"status" db:"f_status"`               // Message status (pending, failed)
	CreatedAt   int64  `json:"created_at" db:"f_created_at"`       // creation time.
	UpdatedAt   int64  `json:"updated_at" db:"f_updated_at"`       // Update time.
	NextRetryAt int64  `json:"next_retry_at" db:"f_next_retry_at"` // Next retry time.
	RetryCount  int    `json:"retry_count" db:"f_retry_count"`     // Number of retries.
}

const (
	OutboxMessageStatusPending string = "pending" // Pending.
	OutboxMessageStatusFailed  string = "failed"  // failed.
)

// IOutboxMessage outbox message interface.
//
//go:generate mockgen -source=outbox_message.go -destination=../../mocks/model_outbox_message.go -package=mocks
type IOutboxMessage interface {
	Insert(ctx context.Context, tx *sql.Tx, message *OutboxMessageDB) (eventID string, err error)
	UpdateByEventID(ctx context.Context, tx *sql.Tx, message *OutboxMessageDB) error
	GetByStatus(ctx context.Context, status string, limit int) ([]*OutboxMessageDB, error)
	DeleteByEventID(ctx context.Context, tx *sql.Tx, eventID string) error
}
