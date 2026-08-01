package iprojectionoutbox

import (
	"context"
	"errors"
	"time"
)

var ErrLeaseLost = errors.New("projection outbox lease was lost")

type permanentError struct {
	err error
}

func (e permanentError) Error() string {
	return e.err.Error()
}

func (e permanentError) Unwrap() error {
	return e.err
}

func (e permanentError) Permanent() bool {
	return true
}

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

func IsPermanent(err error) bool {
	var classified interface{ Permanent() bool }
	return errors.As(err, &classified) && classified.Permanent()
}

type Item struct {
	ID               uint64    `json:"outbox_id"`
	AggregateType    string    `json:"aggregate_type"`
	AggregateID      string    `json:"aggregate_id"`
	AggregateVersion uint64    `json:"aggregate_version"`
	EventType        string    `json:"event_type"`
	EventID          string    `json:"event_id"`
	Payload          []byte    `json:"payload"`
	Attempts         uint32    `json:"attempts"`
	LeaseToken       string    `json:"-"`
	CreatedAt        time.Time `json:"created_at"`
}

func DocumentID(item Item) string {
	return item.AggregateType + ":" + item.AggregateID
}

type Store interface {
	Lease(ctx context.Context, limit int, lockDuration time.Duration) ([]Item, error)
	MarkDelivered(ctx context.Context, item Item) error
	MarkRetry(ctx context.Context, item Item, errorCode string, availableAt time.Time) error
	MoveToDLQ(ctx context.Context, item Item, errorCode string) error
}

type ReplayRequest struct {
	DLQID         uint64
	Operator      string
	Reason        string
	RepairVersion string
}

type DLQAdmin interface {
	ReplayProjectionDLQ(ctx context.Context, request ReplayRequest) error
}

type Sink interface {
	Project(ctx context.Context, item Item) error
}
