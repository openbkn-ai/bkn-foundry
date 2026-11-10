// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_schedule

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func TestBuildSelectQueryAlwaysReadsExecutionSubject(t *testing.T) {
	access := &actionScheduleAccess{}
	query, _, err := access.buildSelectQuery().ToSql()
	if err != nil {
		t.Fatalf("build select query: %v", err)
	}
	if !strings.Contains(query, "f_execution_subject") || !strings.Contains(query, "f_execution_subject_type") {
		t.Fatalf("query omits execution subject columns: %s", query)
	}
}

func TestCreateScheduleWritesExecutionSubject(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectExec("INSERT INTO t_action_schedule .*f_execution_subject.*f_execution_subject_type.*").
		WillReturnResult(sqlmock.NewResult(1, 1))
	access := &actionScheduleAccess{db: db}
	err = access.CreateSchedule(context.Background(), nil, &interfaces.ActionSchedule{
		ID: "schedule-1", Name: "schedule", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
		ActionTypeID: "at-1", CronExpression: "* * * * *",
		ExecutionSubject: interfaces.AccountInfo{ID: "user-current", Type: "user"},
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("execution subject was not persisted: %v", err)
	}
}

func TestDeleteSchedulesByKnIDDeletesEveryBranchInTx(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM t_action_schedule WHERE f_kn_id = ?").WithArgs("kn-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	access := &actionScheduleAccess{db: db}
	if err := access.DeleteSchedulesByKnID(context.Background(), tx, "kn-1"); err != nil {
		t.Fatalf("DeleteSchedulesByKnID() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("schedules were not deleted by network: %v", err)
	}
}
