// Copyright openbkn.ai
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

	"github.com/openbkn-ai/bkn-foundry/comm-go/db/sqlx"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRecordFallsBackToLegacyBusinessDomainColumn(t *testing.T) {
	db, mock, err := sqlx.New()
	if err != nil {
		t.Fatalf("new sql mock: %v", err)
	}
	store := NewStore(db)
	eventTime := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	entry := Entry{
		EventID: "evt-1", EventTime: eventTime, RecordedAt: eventTime,
		ActorID: "user-a", ActorName: "Alice", ActorType: "user", AuthMethod: "oauth",
		RequestID: "req-a", SourceChannel: "api", Method: "POST", Action: "create",
		TargetType: "toolbox", TargetID: "toolbox-a", TargetName: "Toolbox A", Outcome: "success",
	}
	currentArgs := []driver.Value{
		entry.EventID, entry.EventTime, entry.RecordedAt, entry.ActorID,
		entry.ActorName, entry.ActorType, entry.AuthMethod, entry.RequestID, entry.SourceChannel,
		entry.Method, entry.Action, entry.TargetType, entry.TargetID, entry.TargetName,
		entry.Outcome, entry.FailureCode, entry.FailureMessage,
	}

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO " + tableName)).
		WithArgs(currentArgs...).
		WillReturnError(errors.New("Error 1364 (HY000): Field 'business_domain_id' doesn't have a default value"))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO " + tableName)).
		WithArgs(append(currentArgs, "")...).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := store.Record(context.Background(), entry); err != nil {
		t.Fatalf("record with legacy schema: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
