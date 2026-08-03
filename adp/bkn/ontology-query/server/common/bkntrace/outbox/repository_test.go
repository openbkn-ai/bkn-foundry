// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package outbox

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEnqueueUsesCurrentEpochFromStreamState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()

	repository := &Repository{
		db: db,
		config: Config{
			ProducerID:       "bkn-ontology",
			ProducerStreamID: "ontology-query",
		},
		dialect: dialectMariaDB,
	}
	now := time.Now().UTC()
	owner := Owner{
		TenantID: "t1", BusinessDomainID: "d1", ApplicationPrincipalID: "ontology-query",
		EffectiveSubjectType: "service", EffectiveSubjectID: "svc-1",
	}
	event := Event{
		EventID: "evt-1", EventType: "data.query.observed", ConversationID: "c1", InteractionID: "i1",
		StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: []byte(`{"payload":{}}`),
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT current_epoch, next_sequence FROM "+tableStream+" WHERE producer_id = ? AND producer_stream_id = ? FOR UPDATE")).
		WithArgs("bkn-ontology", "ontology-query").
		WillReturnRows(sqlmock.NewRows([]string{"current_epoch", "next_sequence"}).AddRow(uint64(3), uint64(7)))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE "+tableStream+" SET next_sequence = ?, updated_at = ? WHERE producer_id = ? AND producer_stream_id = ?")).
		WithArgs(uint64(8), sqlmock.AnyArg(), "bkn-ontology", "ontology-query").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO "+tableOutbox)).
		WithArgs(
			"evt-1", sqlmock.AnyArg(), "data.query.observed", "3.0.0", "t1", "bkn-ontology", "ontology-query",
			uint64(3), uint64(7), sqlmock.AnyArg(), StatusPending, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	got, err := repository.Enqueue(context.Background(), event, owner)
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if got.ProducerEpoch != 3 || got.ProducerSequence != 7 {
		t.Fatalf("Enqueue() = epoch %d sequence %d, want 3/7", got.ProducerEpoch, got.ProducerSequence)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueRejectsZeroEpoch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	repository := &Repository{
		db:      db,
		config:  Config{ProducerID: "bkn-ontology", ProducerStreamID: "ontology-query"},
		dialect: dialectMariaDB,
	}
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT current_epoch, next_sequence FROM "+tableStream+" WHERE producer_id = ? AND producer_stream_id = ? FOR UPDATE")).
		WithArgs("bkn-ontology", "ontology-query").
		WillReturnRows(sqlmock.NewRows([]string{"current_epoch", "next_sequence"}).AddRow(uint64(0), uint64(1)))
	mock.ExpectRollback()

	_, err = repository.Enqueue(context.Background(), Event{
		EventID: "evt-zero", EventType: "data.query.observed", ConversationID: "c1", InteractionID: "i1",
		StartedAt: now, ObservedAt: now, EmittedAt: now, Envelope: []byte(`{"payload":{}}`),
	}, Owner{TenantID: "t1", BusinessDomainID: "d1", ApplicationPrincipalID: "ontology-query", EffectiveSubjectType: "service", EffectiveSubjectID: "svc-1"})
	if err == nil {
		t.Fatal("Enqueue() accepted epoch 0")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureStreamStateStartsAtEpochOne(t *testing.T) {
	if query := (&Repository{dialect: dialectMariaDB}).ensureStreamStateSQL(); !regexp.MustCompile(`SELECT \?, \?, 1, 1`).MatchString(query) {
		t.Fatalf("MariaDB stream-state initialization must use epoch 1: %s", query)
	}
	if query := (&Repository{dialect: dialectDM8}).ensureStreamStateSQL(); !regexp.MustCompile(`VALUES \(source\.producer_id, source\.producer_stream_id, 1, 1`).MatchString(query) {
		t.Fatalf("DM8 stream-state initialization must use epoch 1: %s", query)
	}
}

func TestClaimHeadOfLineBlocksLaterSequenceDuringBackoff(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	repository := &Repository{db: db, config: Config{ProducerStreamID: "stream-0"}, dialect: dialectMariaDB}
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(repository.claimHeadOfLineSQL())).
		WithArgs("stream-0", StatusDelivered, StatusAbandoned).
		WillReturnRows(sqlmock.NewRows([]string{"outbox_id", "envelope", "status", "attempts", "state_version"}).
			AddRow(int64(1), `{}`, StatusRetry, 1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT available_at FROM " + tableOutbox + " WHERE outbox_id = ?")).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"available_at"}).AddRow(now.Add(time.Minute)))
	mock.ExpectRollback()

	record, err := repository.ClaimHeadOfLine(context.Background(), now)
	if err != nil {
		t.Fatalf("ClaimHeadOfLine() error = %v", err)
	}
	if record != nil {
		t.Fatalf("ClaimHeadOfLine() = %+v, want nil while oldest sequence is backing off", record)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRejectsStaleLeaseToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	repository := &Repository{db: db}
	record := &Record{OutboxID: 9, LeaseToken: "stale-lease"}
	query := regexp.QuoteMeta("UPDATE " + tableOutbox + " SET status = ?, delivered_at = ?, lease_token = NULL, locked_until = NULL, updated_at = ?, state_version = state_version + 1 WHERE outbox_id = ? AND status = ? AND lease_token = ?")
	mock.ExpectExec(query).
		WithArgs(StatusDelivered, sqlmock.AnyArg(), sqlmock.AnyArg(), int64(9), StatusProcessing, "stale-lease").
		WillReturnResult(sqlmock.NewResult(0, 0))

	updated, err := repository.Complete(context.Background(), record, StatusDelivered, "", time.Time{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if updated {
		t.Fatal("Complete() accepted a stale lease token")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupOnlyDeletesCompletedStates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	repository := &Repository{db: db, dialect: dialectMariaDB}
	now := time.Now().UTC()

	mock.ExpectExec(regexp.QuoteMeta(repository.deleteOutboxSQL("delivered_at"))).
		WithArgs(StatusDelivered, now, 1000).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(repository.deleteOutboxSQL("abandoned_at"))).
		WithArgs(StatusAbandoned, now, 1000).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(repository.deleteExpiredAuditsSQL())).
		WithArgs(now, StatusDelivered, StatusAbandoned, 1000).WillReturnResult(sqlmock.NewResult(0, 0))

	result, err := repository.Cleanup(context.Background(), now, now, now, 1000)
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if result != (CleanupResult{}) {
		t.Fatalf("Cleanup() = %+v, want no deleted records", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxListFilters(t *testing.T) {
	where, args := outboxListFilters(ListOptions{
		EventID:          "event-1",
		ProducerStreamID: "stream-0",
		Statuses:         []string{"retry", " dlq ", ""},
	})
	if want := " AND event_id = ? AND producer_stream_id = ? AND status IN (?,?)"; where != want {
		t.Fatalf("where = %q, want %q", where, want)
	}
	if want := []any{"event-1", "stream-0", "retry", "dlq"}; !sameArgs(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

func TestCountUsesListFilters(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	defer db.Close()
	repository := &Repository{db: db}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM " + tableOutbox + " WHERE 1=1 AND status IN (?)")).
		WithArgs(StatusRetry).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(3))

	total, err := repository.Count(context.Background(), ListOptions{Statuses: []string{StatusRetry}})
	if err != nil || total != 3 {
		t.Fatalf("Count() = %d, %v; want 3, nil", total, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func sameArgs(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
