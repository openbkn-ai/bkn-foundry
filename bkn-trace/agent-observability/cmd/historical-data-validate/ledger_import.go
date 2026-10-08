// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/sessionvo"
	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/drivenadapter/dbaccess/mariadb/sessionstore"
)

type historicalLedgerTime time.Time

func (stamp *historicalLedgerTime) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			*stamp = historicalLedgerTime(parsed.UTC())
			return nil
		}
	}
	return errors.New("invalid historical ledger timestamp")
}
func (stamp historicalLedgerTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(stamp).UTC().Format(time.RFC3339Nano))
}

type historicalLedgerRow struct {
	IngestSequence      uint64               `json:"ingest_sequence"`
	EventID             string               `json:"event_id"`
	PayloadHash         string               `json:"payload_hash"`
	ImmutableRecordHash string               `json:"immutable_record_hash"`
	SchemaVersion       string               `json:"schema_version"`
	EventType           string               `json:"event_type"`
	ConversationID      string               `json:"conversation_id"`
	InteractionID       string               `json:"interaction_id"`
	OperationID         *string              `json:"operation_id"`
	Attempt             *uint32              `json:"attempt_no"`
	RequestID           *string              `json:"request_id"`
	TraceID             *string              `json:"trace_id"`
	SpanID              *string              `json:"span_id"`
	ProducerID          string               `json:"producer_id"`
	ProducerStreamID    string               `json:"producer_stream_id"`
	ProducerEpoch       uint64               `json:"producer_epoch"`
	ProducerSequence    uint64               `json:"producer_sequence"`
	CausalityStatus     string               `json:"causality_status"`
	MissingCauseIDs     *string              `json:"missing_causation_event_ids"`
	StartedAt           historicalLedgerTime `json:"started_at"`
	ObservedAt          historicalLedgerTime `json:"observed_at"`
	EmittedAt           historicalLedgerTime `json:"emitted_at"`
	IngestedAt          historicalLedgerTime `json:"ingested_at"`
	Envelope            string               `json:"envelope"`
}
type historicalLedgerSequence struct {
	EventID        string `json:"event_id"`
	SourceSequence uint64 `json:"source_sequence"`
	TargetSequence uint64 `json:"target_sequence"`
}
type historicalLedgerResult struct {
	Verified         bool                       `json:"verified"`
	Created          int                        `json:"created"`
	AlreadyVerified  int                        `json:"already_verified"`
	SequenceMappings []historicalLedgerSequence `json:"sequence_mappings,omitempty"`
}

// Historical import deliberately does not call Store.Commit: that live path
// stamps ingestion with today's time, rewrites causality/envelope, and publishes
// projection work. Original rows are restored without replaying live assembly.
func importHistoricalLedger(reader io.Reader, writer io.Writer) error {
	return historicalLedgerCommand(reader, writer, false)
}
func validateHistoricalLedger(reader io.Reader, writer io.Writer) error {
	return historicalLedgerCommand(reader, writer, true)
}
func historicalLedgerCommand(reader io.Reader, writer io.Writer, validateOnly bool) (failure error) {
	stage := "input"
	defer func() {
		if failure != nil {
			_ = json.NewEncoder(writer).Encode(map[string]any{"verified": false, "reason": "native_ledger_import_failed_" + stage})
		}
	}()
	var plan struct {
		Ledger []historicalLedgerRow `json:"ledger"`
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 128<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected historical ledger input")
	}
	stage = "validation"
	rows, err := prepareHistoricalLedger(plan.Ledger)
	if err != nil {
		return err
	}
	if validateOnly {
		return json.NewEncoder(writer).Encode(historicalLedgerResult{Verified: true})
	}
	stage = "configuration"
	dsn := os.Getenv("BKN_TRACE_CORE_MARIADB_DSN")
	if dsn == "" {
		return errors.New("core database configuration missing")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return errors.New("core database unavailable")
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	stage = "schema"
	if err = sessionstore.New(db).EnsureSchema(ctx, false); err != nil {
		return errors.New("core schema check failed")
	}
	stage = "transaction"
	result, err := importHistoricalLedgerPlan(ctx, db, rows)
	if err != nil {
		return err
	}
	return json.NewEncoder(writer).Encode(result)
}

func historicalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func historicalAttempt(value *uint32) uint32 {
	if value == nil {
		return 0
	}
	return *value
}
func prepareHistoricalLedger(rows []historicalLedgerRow) ([]historicalLedgerRow, error) {
	unique := map[string]historicalLedgerRow{}
	streams := map[string]string{}
	result := make([]historicalLedgerRow, 0, len(rows))
	for _, row := range rows {
		var event ledgervo.Event
		if err := json.Unmarshal([]byte(row.Envelope), &event); err != nil {
			return nil, errors.New("invalid historical ledger envelope")
		}
		if row.EventID == "" || row.ConversationID == "" || row.InteractionID == "" || row.ProducerID == "" || row.ProducerStreamID == "" || row.ProducerEpoch == 0 || row.ProducerSequence == 0 || time.Time(row.IngestedAt).IsZero() {
			return nil, errors.New("invalid historical ledger identity")
		}
		if event.Owner.ApplicationPrincipalID == "" || event.Owner.EffectiveSubjectType == "" || event.Owner.EffectiveSubjectID == "" {
			return nil, errors.New("invalid historical ledger owner")
		}
		if row.PayloadHash != event.PayloadHash || row.PayloadHash != ledgervo.CanonicalPayloadHash(event.Envelope) || row.ImmutableRecordHash != ledgervo.ImmutableRecordHash(event) {
			return nil, errors.New("historical ledger hash conflict")
		}
		if row.EventID != event.EventID || row.SchemaVersion != event.SchemaVersion || row.EventType != event.EventType || row.ConversationID != event.ConversationID || row.InteractionID != event.InteractionID || historicalString(row.OperationID) != event.OperationID || historicalAttempt(row.Attempt) != event.Attempt || historicalString(row.RequestID) != event.RequestID || historicalString(row.TraceID) != event.TraceID || historicalString(row.SpanID) != event.SpanID || row.ProducerID != event.ProducerID || row.ProducerStreamID != event.ProducerStreamID || row.ProducerEpoch != event.ProducerEpoch || row.ProducerSequence != event.ProducerSequence || row.CausalityStatus != event.CausalityStatus {
			return nil, errors.New("historical ledger metadata conflict")
		}
		var missing []string
		if row.MissingCauseIDs != nil {
			if err := json.Unmarshal([]byte(*row.MissingCauseIDs), &missing); err != nil {
				return nil, errors.New("invalid historical ledger causality")
			}
		}
		if strings.Join(missing, "\x00") != strings.Join(event.MissingCauseIDs, "\x00") {
			return nil, errors.New("historical ledger causality conflict")
		}
		for _, pair := range []struct {
			stored   historicalLedgerTime
			original time.Time
		}{{row.StartedAt, event.StartedAt}, {row.ObservedAt, event.ObservedAt}, {row.EmittedAt, event.EmittedAt}} {
			if pair.original.IsZero() || !time.Time(pair.stored).Equal(pair.original.UTC().Truncate(time.Microsecond)) {
				return nil, errors.New("historical ledger timestamp conflict")
			}
		}
		if prior, found := unique[row.EventID]; found {
			if !sameHistoricalLedger(prior, row) {
				return nil, errors.New("historical ledger source content conflict")
			}
			continue
		}
		key := fmt.Sprintf("%s:%d:%d", row.ProducerStreamID, row.ProducerEpoch, row.ProducerSequence)
		if prior, found := streams[key]; found && prior != row.EventID {
			return nil, errors.New("historical ledger source sequence conflict")
		}
		unique[row.EventID] = row
		streams[key] = row.EventID
		result = append(result, row)
	}
	return result, nil
}
func sameHistoricalLedger(left, right historicalLedgerRow) bool {
	left.IngestSequence = 0
	right.IngestSequence = 0
	return sameCore(left, right)
}

var historicalLedgerColumns = []string{"event_id", "payload_hash", "immutable_record_hash", "schema_version", "event_type", "conversation_id", "interaction_id", "operation_id", "attempt_no", "request_id", "trace_id", "span_id", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence", "causality_status", "missing_causation_event_ids", "started_at", "observed_at", "emitted_at", "ingested_at", "envelope"}

func historicalLedgerSelect() string {
	pairs := []string{"'ingest_sequence',ingest_sequence"}
	for _, column := range historicalLedgerColumns {
		value := column
		if strings.HasSuffix(column, "_at") {
			value = "DATE_FORMAT(" + column + ",'%Y-%m-%dT%H:%i:%s.%fZ')"
		}
		pairs = append(pairs, "'"+column+"',"+value)
	}
	return "SELECT JSON_OBJECT(" + strings.Join(pairs, ",") + ") FROM bkn_trace_evidence_event_ledger WHERE event_id=?"
}

type historicalLedgerQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readHistoricalLedger(ctx context.Context, query historicalLedgerQuery, eventID string, lock bool) (historicalLedgerRow, bool, error) {
	statement := historicalLedgerSelect()
	if lock {
		statement += " FOR UPDATE"
	}
	var raw []byte
	if err := query.QueryRowContext(ctx, statement, eventID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return historicalLedgerRow{}, false, nil
		}
		return historicalLedgerRow{}, false, err
	}
	var row historicalLedgerRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return row, false, err
	}
	return row, true, nil
}
func historicalLedgerValues(row historicalLedgerRow) []any {
	return []any{row.EventID, row.PayloadHash, row.ImmutableRecordHash, row.SchemaVersion, row.EventType, row.ConversationID, row.InteractionID, row.OperationID, row.Attempt, row.RequestID, row.TraceID, row.SpanID, row.ProducerID, row.ProducerStreamID, row.ProducerEpoch, row.ProducerSequence, row.CausalityStatus, row.MissingCauseIDs, time.Time(row.StartedAt), time.Time(row.ObservedAt), time.Time(row.EmittedAt), time.Time(row.IngestedAt), row.Envelope}
}
func verifyHistoricalLedgerOwner(ctx context.Context, tx *sql.Tx, row historicalLedgerRow) error {
	var event ledgervo.Event
	if err := json.Unmarshal([]byte(row.Envelope), &event); err != nil {
		return err
	}
	var owner sessionvo.Owner
	if err := tx.QueryRowContext(ctx, `SELECT c.application_principal_id,c.effective_subject_type,c.effective_subject_id,c.delegation_id
 FROM bkn_trace_conversations c JOIN bkn_trace_interactions i ON i.conversation_id=c.conversation_id
 WHERE c.conversation_id=? AND i.interaction_id=? FOR UPDATE`, row.ConversationID, row.InteractionID).Scan(&owner.ApplicationPrincipalID, &owner.EffectiveSubjectType, &owner.EffectiveSubjectID, &owner.DelegationID); err != nil {
		return err
	}
	if !owner.Equal(event.Owner) {
		return errors.New("historical ledger core owner conflict")
	}
	if event.OperationID != "" {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM bkn_trace_operations WHERE operation_id=? AND interaction_id=? AND conversation_id=?", event.OperationID, event.InteractionID, event.ConversationID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("historical ledger core operation missing")
		}
	}
	return nil
}
func importHistoricalLedgerPlan(ctx context.Context, db *sql.DB, source []historicalLedgerRow) (historicalLedgerResult, error) {
	result := historicalLedgerResult{}
	rows, err := prepareHistoricalLedger(source)
	if err != nil {
		return result, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, row := range rows {
		if err = verifyHistoricalLedgerOwner(ctx, tx, row); err != nil {
			return historicalLedgerResult{}, err
		}
		stored, found, err := readHistoricalLedger(ctx, tx, row.EventID, true)
		if err != nil {
			return historicalLedgerResult{}, err
		}
		if found {
			if !sameHistoricalLedger(stored, row) {
				return historicalLedgerResult{}, errors.New("historical ledger target content conflict")
			}
			result.AlreadyVerified++
		} else {
			statement := "INSERT INTO bkn_trace_evidence_event_ledger (" + strings.Join(historicalLedgerColumns, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(historicalLedgerColumns)), ",") + ")"
			if _, err = tx.ExecContext(ctx, statement, historicalLedgerValues(row)...); err != nil {
				return historicalLedgerResult{}, err
			}
			// Same derived cache invalidation as native Ledger Commit, without lifecycle
			// finish, receipt reconciliation, projection publishing or new assembly.
			if _, err = tx.ExecContext(ctx, "UPDATE bkn_trace_interactions SET record_integrity_version=record_integrity_version+1, record_integrity_json=NULL WHERE interaction_id=? AND execution_status<>?", row.InteractionID, sessionvo.InteractionActive); err != nil {
				return historicalLedgerResult{}, err
			}
			stored, found, err = readHistoricalLedger(ctx, tx, row.EventID, true)
			if err != nil {
				return historicalLedgerResult{}, err
			}
			if !found || !sameHistoricalLedger(stored, row) {
				return historicalLedgerResult{}, errors.New("historical ledger readback conflict")
			}
			result.Created++
		}
		if stored.IngestSequence == 0 {
			return historicalLedgerResult{}, errors.New("historical ledger readback sequence missing")
		}
		if stored.IngestSequence != row.IngestSequence {
			result.SequenceMappings = append(result.SequenceMappings, historicalLedgerSequence{EventID: row.EventID, SourceSequence: row.IngestSequence, TargetSequence: stored.IngestSequence})
		}
	}
	if err = tx.Commit(); err != nil {
		return historicalLedgerResult{}, err
	}
	for _, row := range rows {
		stored, found, err := readHistoricalLedger(ctx, db, row.EventID, false)
		if err != nil {
			return result, err
		}
		if !found || !sameHistoricalLedger(stored, row) {
			return result, errors.New("historical ledger persisted readback conflict")
		}
	}
	result.Verified = true
	return result, nil
}
