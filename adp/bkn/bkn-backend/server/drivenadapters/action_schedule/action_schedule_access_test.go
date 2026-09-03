// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_schedule

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"bkn-backend/interfaces"
)

func TestBuildSelectQueryReadsExecutionSubjectOnlyWhilePEPIsEnabled(t *testing.T) {
	access := &actionScheduleAccess{}

	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "false")
	disabledSQL, _, err := access.buildSelectQuery().ToSql()
	if err != nil {
		t.Fatalf("disabled query: %v", err)
	}
	if strings.Contains(disabledSQL, "f_execution_subject") {
		t.Fatalf("disabled query references the migrated column: %s", disabledSQL)
	}

	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "true")
	enabledSQL, _, err := access.buildSelectQuery().ToSql()
	if err != nil {
		t.Fatalf("enabled query: %v", err)
	}
	if !strings.Contains(enabledSQL, "f_execution_subject") || !strings.Contains(enabledSQL, "f_execution_subject_type") {
		t.Fatalf("enabled query omits execution subject columns: %s", enabledSQL)
	}
}

func TestCreateScheduleWritesExecutionSubjectWhilePEPIsDisabled(t *testing.T) {
	t.Setenv("ACTION_EXECUTION_PEP_ENABLED", "false")
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
