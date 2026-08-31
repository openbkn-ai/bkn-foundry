// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package operationaudit

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRecordUsesStableEventIDForIdempotency(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := &Store{db: db}
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	entry := Entry{EventID: EventID("req-a", "POST", "/catalogs"), EventTime: now, RecordedAt: now, ActorID: "user-a", ActorName: "管理员", ActorType: "user", AuthMethod: "oauth", RequestID: "req-a", SourceChannel: "api", Method: "POST", Action: "create", TargetType: "catalog", TargetID: "catalog:req-a", TargetName: "供应链数据源", Outcome: "success"}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO "+tableName)).WithArgs(
		entry.EventID, entry.EventTime, entry.RecordedAt,
		entry.ActorID, entry.ActorName, entry.ActorType, entry.AuthMethod, entry.RequestID,
		entry.SourceChannel, entry.Method, entry.Action, entry.TargetType, entry.TargetID, entry.TargetName,
		entry.Outcome, entry.FailureCode, entry.FailureMessage,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, store.Record(context.Background(), entry))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreRecordRejectsUnboundedFailureMessage(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := &Store{db: db}
	entry := Entry{EventID: "evt-a", EventTime: time.Now(), RecordedAt: time.Now(), ActorID: "user-a", ActorName: "管理员", RequestID: "req-a", Action: "create", TargetType: "catalog", TargetID: "catalog-a", Outcome: "failure", FailureMessage: string(make([]byte, 513))}
	err = store.Record(context.Background(), entry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bounded field size")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStoreRecordFallsBackToLegacyBusinessDomainColumn(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	store := &Store{db: db}
	now := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	entry := Entry{EventID: EventID("req-a", "POST", "/catalogs"), EventTime: now, RecordedAt: now, ActorID: "user-a", ActorName: "管理员", ActorType: "user", AuthMethod: "oauth", RequestID: "req-a", SourceChannel: "api", Method: "POST", Action: "create", TargetType: "catalog", TargetID: "catalog:req-a", TargetName: "供应链数据源", Outcome: "success"}
	currentArgs := []driver.Value{
		entry.EventID, entry.EventTime, entry.RecordedAt,
		entry.ActorID, entry.ActorName, entry.ActorType, entry.AuthMethod, entry.RequestID,
		entry.SourceChannel, entry.Method, entry.Action, entry.TargetType, entry.TargetID, entry.TargetName,
		entry.Outcome, entry.FailureCode, entry.FailureMessage,
	}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO " + tableName)).WithArgs(currentArgs...).
		WillReturnError(errors.New("Error 1364 (HY000): Field 'business_domain_id' doesn't have a default value"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO " + tableName)).WithArgs(append(currentArgs, "")...).
		WillReturnResult(sqlmock.NewResult(1, 1))
	require.NoError(t, store.Record(context.Background(), entry))
	assert.NoError(t, mock.ExpectationsWereMet())
}
