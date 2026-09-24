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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	monthlyaudit "github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/migrations/mariadb/audit"
)

type Event struct {
	EventID          string
	ContentHash      string
	SourceID         string
	Payload          []byte
	OccurredAt       time.Time
	BrokerReceivedAt time.Time
	Kafka            KafkaCoordinate
}

type KafkaCoordinate struct {
	Topic     string
	Partition int
	Offset    int64
}

type Decision string

const (
	DecisionInserted   Decision = "inserted"
	DecisionIdempotent Decision = "idempotent"
	DecisionConflict   Decision = "event_id_conflict"
)

var tableNamePattern = regexp.MustCompile(`^audit_event_[0-9]{6}$`)

var (
	ErrInvalidEvent             = errors.New("invalid audit event")
	ErrInvalidMonth             = errors.New("invalid audit event month")
	ErrMonthlySchemaUnavailable = errors.New("audit monthly table does not satisfy v032 Kafka coordinate schema")
)

const MonthlyTemplateSHA256 = "52acd735cb9c9bd3442e9280c50de0f26676faadc829453ef99d0e53546979d7"

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
	if event.EventID == "" || event.ContentHash == "" || event.SourceID == "" || len(event.Payload) == 0 || event.OccurredAt.IsZero() || event.BrokerReceivedAt.IsZero() || event.Kafka.Topic != "openbkn.audit.v1" || event.Kafka.Partition < 0 || event.Kafka.Offset < 0 {
		return "", ErrInvalidEvent
	}
	table := "audit_event_" + event.OccurredAt.UTC().Format("200601")
	if !tableNamePattern.MatchString(table) {
		return "", ErrInvalidMonth
	}
	if err := verifyMonthlyTemplate(); err != nil {
		return "", err
	}
	if err := verifyMonthlyTableSchema(ctx, s.db, table); err != nil {
		return "", err
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
		(event_id, source_id, content_hash, payload, occurred_at, broker_received_at, recorded_at,
		 topic, partition_id, offset_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := tx.ExecContext(ctx, query, event.EventID, event.SourceID, event.ContentHash,
		event.Payload, event.OccurredAt.UTC(), event.BrokerReceivedAt.UTC(), recordedAt,
		event.Kafka.Topic, event.Kafka.Partition, event.Kafka.Offset); err != nil {
		return "", fmt.Errorf("insert audit event ledger: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit audit event ledger: %w", err)
	}
	return DecisionInserted, nil
}

func (s *Store) ValidateMonthlyWindow(ctx context.Context, now time.Time) error {
	if err := verifyMonthlyTemplate(); err != nil {
		return err
	}
	month := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		table := "audit_event_" + month.AddDate(0, i, 0).Format("200601")
		if err := verifyMonthlyTableSchema(ctx, s.db, table); err != nil {
			return fmt.Errorf("validate Audit monthly table %s: %w", table, err)
		}
	}
	return nil
}

// MigrateMonthlyWindow is the explicit, repeatable operator migration. It is
// intentionally not called by application startup; operators may alter only
// the current UTC month and the next two months through this entry point.
func (s *Store) MigrateMonthlyWindow(ctx context.Context, now time.Time) error {
	if err := verifyMonthlyTemplate(); err != nil {
		return err
	}
	month := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		table := "audit_event_" + month.AddDate(0, i, 0).Format("200601")
		if err := s.migrateMonthlyTable(ctx, table); err != nil {
			return fmt.Errorf("migrate Audit monthly table %s: %w", table, err)
		}
	}
	return nil
}

func (s *Store) migrateMonthlyTable(ctx context.Context, table string) error {
	if !tableNamePattern.MatchString(table) {
		return ErrInvalidMonth
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema=? AND table_name=?`, "bkn_audit", table).Scan(&exists); err != nil {
		return fmt.Errorf("check monthly table: %w", err)
	}
	switch exists {
	case 0:
		create := strings.ReplaceAll(monthlyaudit.TemplateSQL(), "YYYYMM", strings.TrimPrefix(table, "audit_event_"))
		if _, err := s.db.ExecContext(ctx, create); err != nil {
			return fmt.Errorf("create monthly v032 table: %w", err)
		}
	case 1:
		baseColumns := []string{"event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at"}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(baseColumns)), ",")
		args := []any{"bkn_audit", table}
		for _, column := range baseColumns {
			args = append(args, column)
		}
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT column_name) FROM information_schema.columns
			WHERE table_schema=? AND table_name=? AND column_name IN (`+placeholders+")", args...).Scan(&count); err != nil {
			return fmt.Errorf("verify v031 monthly table before upgrade: %w", err)
		}
		if count != len(baseColumns) {
			return errors.New("existing Audit monthly table is not a recognized v031/v032 table")
		}
		alter := "ALTER TABLE bkn_audit." + table +
			"\nADD COLUMN IF NOT EXISTS topic VARCHAR(249) NULL," +
			"\nADD COLUMN IF NOT EXISTS partition_id INT NULL," +
			"\nADD COLUMN IF NOT EXISTS offset_id BIGINT NULL"
		if _, err := s.db.ExecContext(ctx, alter); err != nil {
			return fmt.Errorf("add nullable Kafka coordinate columns: %w", err)
		}
		index := `CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_kafka_coordinate ON bkn_audit.` + table + ` (topic, partition_id, offset_id)`
		if _, err := s.db.ExecContext(ctx, index); err != nil {
			return fmt.Errorf("add Kafka coordinate unique index: %w", err)
		}
	default:
		return fmt.Errorf("unexpected monthly table count %d", exists)
	}
	return verifyMonthlyTableSchema(ctx, s.db, table)
}

func verifyMonthlyTemplate() error {
	if digest([]byte(monthlyaudit.TemplateSQL())) != MonthlyTemplateSHA256 {
		return errors.New("embedded Audit monthly v032 template digest mismatch")
	}
	return nil
}

func verifyMonthlyTableSchema(ctx context.Context, db *sql.DB, table string) error {
	if !tableNamePattern.MatchString(table) {
		return ErrInvalidMonth
	}
	columns := []string{"event_id", "source_id", "content_hash", "payload", "occurred_at", "broker_received_at", "recorded_at", "topic", "partition_id", "offset_id"}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
	args := []any{"bkn_audit", table}
	for _, column := range columns {
		args = append(args, column)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT column_name) FROM information_schema.columns
		WHERE table_schema=? AND table_name=? AND column_name IN (`+placeholders+")", args...).Scan(&count); err != nil {
		return fmt.Errorf("read Audit monthly table columns: %w", err)
	}
	if count != len(columns) {
		return ErrMonthlySchemaUnavailable
	}
	var indexColumns sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',')
		FROM information_schema.statistics WHERE table_schema=? AND table_name=?
		AND index_name='uq_audit_kafka_coordinate' AND non_unique=0`, "bkn_audit", table).Scan(&indexColumns); err != nil {
		return fmt.Errorf("read Audit monthly Kafka coordinate index: %w", err)
	}
	if !indexColumns.Valid || indexColumns.String != "topic,partition_id,offset_id" {
		return ErrMonthlySchemaUnavailable
	}
	columnRows, err := db.QueryContext(ctx, `SELECT column_name, LOWER(data_type), LOWER(column_type), is_nullable
		FROM information_schema.columns WHERE table_schema=? AND table_name=?`, "bkn_audit", table)
	if err != nil {
		return fmt.Errorf("read Audit monthly column definitions: %w", err)
	}
	actualColumns := make(map[string]struct{ dataType, columnType, nullable string })
	for columnRows.Next() {
		var name, dataType, columnType, nullable string
		if err := columnRows.Scan(&name, &dataType, &columnType, &nullable); err != nil {
			_ = columnRows.Close()
			return fmt.Errorf("scan Audit monthly column definition: %w", err)
		}
		actualColumns[name] = struct{ dataType, columnType, nullable string }{dataType, columnType, nullable}
	}
	if err := columnRows.Err(); err != nil {
		_ = columnRows.Close()
		return fmt.Errorf("read Audit monthly column definitions: %w", err)
	}
	if err := columnRows.Close(); err != nil {
		return fmt.Errorf("close Audit monthly column definitions: %w", err)
	}
	expectedColumns := map[string]struct{ dataType, columnType, nullable string }{
		"event_id": {"varchar", "varchar(128)", "NO"}, "source_id": {"varchar", "varchar(128)", "NO"},
		"content_hash": {"char", "char(71)", "NO"}, "payload": {"json", "json", "NO"},
		"occurred_at": {"datetime", "datetime(6)", "NO"}, "broker_received_at": {"datetime", "datetime(6)", "NO"},
		"recorded_at": {"datetime", "datetime(6)", "NO"}, "topic": {"varchar", "varchar(249)", ""},
		"partition_id": {"int", "", ""}, "offset_id": {"bigint", "", ""},
	}
	for name, expected := range expectedColumns {
		actual, ok := actualColumns[name]
		validType := actual.dataType == expected.dataType && (expected.columnType == "" || actual.columnType == expected.columnType)
		if name == "payload" && actual.dataType == "longtext" && actual.columnType == "longtext" {
			validType = true // MariaDB represents its JSON alias as LONGTEXT.
		}
		if name == "partition_id" || name == "offset_id" {
			validType = actual.dataType == expected.dataType && !strings.Contains(actual.columnType, "unsigned")
		}
		if !ok || !validType || (expected.nullable != "" && actual.nullable != expected.nullable) {
			return ErrMonthlySchemaUnavailable
		}
		if expected.nullable == "" && actual.nullable != "YES" && actual.nullable != "NO" {
			return ErrMonthlySchemaUnavailable
		}
	}
	indexRows, err := db.QueryContext(ctx, `SELECT index_name, GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ','), non_unique
		FROM information_schema.statistics WHERE table_schema=? AND table_name=? GROUP BY index_name, non_unique`, "bkn_audit", table)
	if err != nil {
		return fmt.Errorf("read Audit monthly indexes: %w", err)
	}
	actualIndexes := make(map[string]struct {
		columns   string
		nonUnique int
	})
	for indexRows.Next() {
		var name, columns string
		var nonUnique int
		if err := indexRows.Scan(&name, &columns, &nonUnique); err != nil {
			_ = indexRows.Close()
			return fmt.Errorf("scan Audit monthly index: %w", err)
		}
		actualIndexes[name] = struct {
			columns   string
			nonUnique int
		}{columns, nonUnique}
	}
	if err := indexRows.Err(); err != nil {
		_ = indexRows.Close()
		return fmt.Errorf("read Audit monthly indexes: %w", err)
	}
	if err := indexRows.Close(); err != nil {
		return fmt.Errorf("close Audit monthly indexes: %w", err)
	}
	for name, expected := range map[string]struct {
		columns   string
		nonUnique int
	}{
		"PRIMARY": {"event_id", 0}, "uq_audit_kafka_coordinate": {"topic,partition_id,offset_id", 0},
		"idx_audit_event_occurred": {"occurred_at,event_id", 1}, "idx_audit_event_source": {"source_id,occurred_at,event_id", 1},
	} {
		if actualIndexes[name] != expected {
			return ErrMonthlySchemaUnavailable
		}
	}
	if len(actualColumns) != len(expectedColumns) {
		return ErrMonthlySchemaUnavailable
	}
	return nil
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
