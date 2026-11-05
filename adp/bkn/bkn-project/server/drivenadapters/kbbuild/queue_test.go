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
	for _, scenario := range []string{"active", "archived", "foreign_owner", "completed", "linking_after_cleanup", "linking_attention_after_cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			record := processRecord()
			record.LeaseID = "old-worker"
			record.LeaseUntil = 999
			if scenario == "linking_after_cleanup" || scenario == "linking_attention_after_cleanup" {
				record.Build.State = "linking"
				record.Identity.Cleaned = true
				record.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: "operation", State: "running"}
				if scenario == "linking_attention_after_cleanup" {
					record.Build.State = "linking_attention"
				}
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
					if scenario == "linking_after_cleanup" || scenario == "linking_attention_after_cleanup" {
						state = "succeeded"
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

func TestWorkItemsReportStallFromProgressNotFromLastSave(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT f_state,f_data,f_update_time FROM t_bkn_project_kb_build").WillReturnRows(sqlmock.NewRows([]string{"state", "data", "updated"}).
		AddRow("running", `{"progress_time":500}`, int64(900)).
		AddRow("queued", `{}`, int64(700)).
		AddRow("attention", `not json`, int64(800)))
	items, err := New(db).(interfaces.KBWorkItems).WorkItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].ProgressTime != 500 || items[1].ProgressTime != 700 || items[2].ProgressTime != 800 || items[0].State != "running" {
		t.Fatalf("%+v", items)
	}
}

func TestRefreshWorkItemsSkipIdleLineages(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("f_state<>'idle'").WillReturnRows(sqlmock.NewRows([]string{"state", "data", "updated"}).AddRow("applying", `{"progress_time":40}`, int64(90)))
	items, err := NewRefreshes(db).(interfaces.KBWorkItems).WorkItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ProgressTime != 40 || items[0].State != "applying" {
		t.Fatalf("%+v", items)
	}
}
