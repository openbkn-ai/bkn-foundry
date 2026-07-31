package sessionstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/port/driven/iprojectionoutbox"
)

func (s *Store) Lease(ctx context.Context, limit int, lockDuration time.Duration) ([]iprojectionoutbox.Item, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT outbox_id, aggregate_type, aggregate_id, aggregate_version, event_type, event_id,
			payload, attempts, created_at
		FROM bkn_trace_projection_outbox
		WHERE status IN ('pending', 'processing') AND available_at<=UTC_TIMESTAMP(6)
		  AND (locked_until IS NULL OR locked_until<=UTC_TIMESTAMP(6))
		ORDER BY outbox_id LIMIT ? FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	var result []iprojectionoutbox.Item
	for rows.Next() {
		var item iprojectionoutbox.Item
		if err := rows.Scan(
			&item.ID, &item.AggregateType, &item.AggregateID, &item.AggregateVersion,
			&item.EventType,
			&item.EventID, &item.Payload, &item.Attempts, &item.CreatedAt,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range result {
		result[index].LeaseToken = newOutboxLeaseToken()
		update, err := tx.ExecContext(ctx, `
			UPDATE bkn_trace_projection_outbox
			SET status='processing',
				lease_token=?,
				locked_until=DATE_ADD(UTC_TIMESTAMP(6), INTERVAL ? MICROSECOND)
			WHERE outbox_id=? AND (locked_until IS NULL OR locked_until<=UTC_TIMESTAMP(6))`,
			result[index].LeaseToken, lockDuration.Microseconds(), result[index].ID)
		if err != nil {
			return nil, err
		}
		if err := requireOutboxLease(update); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) MarkDelivered(ctx context.Context, item iprojectionoutbox.Item) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE bkn_trace_projection_outbox
		SET status='delivered', delivered_at=UTC_TIMESTAMP(6), locked_until=NULL,
			lease_token=NULL, last_error_code=NULL
		WHERE outbox_id=? AND status='processing' AND lease_token=?`,
		item.ID, item.LeaseToken)
	if err != nil {
		return err
	}
	return requireOutboxLease(result)
}

func (s *Store) MarkRetry(ctx context.Context, item iprojectionoutbox.Item, errorCode string, availableAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE bkn_trace_projection_outbox
		SET status='pending', attempts=attempts+1, available_at=?, locked_until=NULL,
			lease_token=NULL, last_error_code=?
		WHERE outbox_id=? AND status='processing' AND lease_token=?`,
		availableAt, errorCode, item.ID, item.LeaseToken,
	)
	if err != nil {
		return err
	}
	return requireOutboxLease(result)
}

func (s *Store) MoveToDLQ(ctx context.Context, item iprojectionoutbox.Item, errorCode string) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_projection_outbox
		SET status='dead', attempts=attempts+1, locked_until=NULL,
			lease_token=NULL, last_error_code=?
		WHERE outbox_id=? AND status='processing' AND lease_token=?`,
		errorCode, item.ID, item.LeaseToken,
	)
	if err != nil {
		return err
	}
	if err := requireOutboxLease(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_dlq (
			source_kind, source_id, failure_class, attempts,
			last_error_code, payload, failed_at
		) VALUES ('projection', ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE
			failure_class=VALUES(failure_class),
			attempts=VALUES(attempts),
			last_error_code=VALUES(last_error_code),
			payload=VALUES(payload),
			failed_at=VALUES(failed_at),
			replayed_at=NULL,
			replayed_by=NULL`,
		item.EventID, errorCode, item.Attempts+1, errorCode, item.Payload,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReplayProjectionDLQ(
	ctx context.Context,
	request iprojectionoutbox.ReplayRequest,
) error {
	if request.DLQID == 0 ||
		strings.TrimSpace(request.Operator) == "" ||
		strings.TrimSpace(request.Reason) == "" ||
		strings.TrimSpace(request.RepairVersion) == "" {
		return errors.New("DLQ ID, operator, reason and repair version are required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var sourceKind, sourceID string
	var replayedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT source_kind, source_id, replayed_at
		FROM bkn_trace_dlq WHERE dlq_id=? FOR UPDATE`,
		request.DLQID,
	).Scan(&sourceKind, &sourceID, &replayedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("projection DLQ entry %d not found", request.DLQID)
	}
	if err != nil {
		return err
	}
	if sourceKind != "projection" {
		return fmt.Errorf("DLQ entry %d is not a projection failure", request.DLQID)
	}
	if replayedAt.Valid {
		return fmt.Errorf("projection DLQ entry %d was already replayed", request.DLQID)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_projection_outbox
		SET status='pending', attempts=0, available_at=UTC_TIMESTAMP(6),
			locked_until=NULL, lease_token=NULL, last_error_code=NULL,
			delivered_at=NULL
		WHERE event_id=? AND status='dead'`,
		sourceID,
	)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("projection outbox for DLQ entry %d is not dead", request.DLQID)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_trace_dlq_replay_audit (
			dlq_id, source_kind, source_id, replayed_by,
			reason, repair_version, replayed_at
		) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(6))`,
		request.DLQID, sourceKind, sourceID, request.Operator,
		request.Reason, request.RepairVersion,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE bkn_trace_dlq
		SET replayed_at=UTC_TIMESTAMP(6), replayed_by=?
		WHERE dlq_id=?`,
		request.Operator, request.DLQID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func requireOutboxLease(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return iprojectionoutbox.ErrLeaseLost
	}
	return nil
}

func newOutboxLeaseToken() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}
