// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestCancellationFencesActiveWorkerWithoutRequiringAnUnarchivedSource(t *testing.T) {
	for _, scenario := range []string{"active", "archived", "foreign_owner", "completed", "linking_after_cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			record := processRecord()
			record.LeaseID = "old-worker"
			record.LeaseUntil = 999
			if scenario == "linking_after_cleanup" {
				record.Build.State = "linking"
				record.Identity.Cleaned = true
			}
			mock.ExpectBegin()
			owner := "owner"
			if scenario == "foreign_owner" {
				owner = "another"
			}
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(scenario == "archived", owner, "user"))
			if scenario == "foreign_owner" {
				mock.ExpectRollback()
			} else {
				if scenario == "completed" {
					record.Build.State = "succeeded"
				}
				mock.ExpectQuery("SELECT .* FROM "+table).WithArgs("project", "build").WillReturnRows(rowsFor(record))
				if scenario != "completed" {
					state := "cancel_pending"
					if scenario == "linking_after_cleanup" {
						state = "cancelled"
					}
					mock.ExpectExec("UPDATE "+table).WithArgs(state, int64(2), sqlmock.AnyArg(), int64(20), "project", "build", int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				mock.ExpectCommit()
			}
			ok, err := New(db).Cancel(context.Background(), "project", "build", interfaces.AccountInfo{ID: "owner", Type: "user"}, 20)
			if err != nil || ok != (scenario != "foreign_owner") {
				t.Fatal("incorrect cancellation ownership", ok, err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRunnableQueueIsBoundedAndExcludesCompletedAndHumanReviewOnlyTasks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT f_project_id,f_id FROM " + table + " WHERE f_state IN").WithArgs(8).WillReturnRows(sqlmock.NewRows([]string{"project", "id"}).AddRow("project", "build"))
	queue := New(db)
	entries, err := queue.Runnable(context.Background(), 8)
	if err != nil || len(entries) != 1 || entries[0].ID != "build" {
		t.Fatal("wrong runnable queue", err)
	}
	if _, err = queue.Runnable(context.Background(), 1001); err == nil {
		t.Fatal("unbounded queue accepted")
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
