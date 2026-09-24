// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root.

// Package auditstore is the transactional center-ledger adapter for Audit v1.
// It owns only the deduplication decision and monthly append; Kafka offset
// management remains in the Writer service.
package auditstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

type Event struct {
	EventID          string
	ContentHash      string
	SourceID         string
	Payload          []byte
	OccurredAt       time.Time
	BrokerReceivedAt time.Time
}

type Decision string

const (
	DecisionInserted   Decision = "inserted"
	DecisionIdempotent Decision = "idempotent"
	DecisionConflict   Decision = "event_id_conflict"
)

var tableNamePattern = regexp.MustCompile(`^audit_event_[0-9]{6}$`)

var (
	ErrInvalidEvent = errors.New("invalid audit event")
	ErrInvalidMonth = errors.New("invalid audit event month")
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("audit store database is required")
	}
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Store) Append(ctx context.Context, event Event) (Decision, error) {
	if event.EventID == "" || event.ContentHash == "" || event.SourceID == "" || len(event.Payload) == 0 || event.OccurredAt.IsZero() || event.BrokerReceivedAt.IsZero() {
		return "", ErrInvalidEvent
	}
	table := "audit_event_" + event.OccurredAt.UTC().Format("200601")
	if !tableNamePattern.MatchString(table) {
		return "", ErrInvalidMonth
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", fmt.Errorf("begin audit ledger transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existingHash, existingTable string
	err = tx.QueryRowContext(ctx, `
		SELECT content_hash, target_table
		FROM bkn_audit.audit_event_dedup
		WHERE event_id=? FOR UPDATE`, event.EventID).Scan(&existingHash, &existingTable)
	switch {
	case err == nil:
		if existingHash == event.ContentHash {
			if err := tx.Commit(); err != nil {
				return "", fmt.Errorf("commit idempotent audit event: %w", err)
			}
			return DecisionIdempotent, nil
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit audit event conflict: %w", err)
		}
		return DecisionConflict, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("lookup audit event dedup: %w", err)
	}

	recordedAt := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bkn_audit.audit_event_dedup (event_id, content_hash, first_recorded_at, target_table)
		VALUES (?, ?, ?, ?)`, event.EventID, event.ContentHash, recordedAt, table); err != nil {
		return "", fmt.Errorf("insert audit event dedup: %w", err)
	}
	query := `INSERT INTO bkn_audit.` + table + `
		(event_id, source_id, content_hash, payload, occurred_at, broker_received_at, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := tx.ExecContext(ctx, query, event.EventID, event.SourceID, event.ContentHash,
		event.Payload, event.OccurredAt.UTC(), event.BrokerReceivedAt.UTC(), recordedAt); err != nil {
		return "", fmt.Errorf("insert audit event ledger: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit audit event ledger: %w", err)
	}
	return DecisionInserted, nil
}
