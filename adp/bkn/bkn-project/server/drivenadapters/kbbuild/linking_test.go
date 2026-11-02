// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func TestClaimLinkingKeepsCompletedExtractionAndLocksTheCurrentProjectScope(t *testing.T) {
	for _, scenario := range []string{"claim", "foreign_lease", "archived", "source_changed", "another_build", "not_cleaned", "incomplete_file", "unchanged_result"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			r := processRecord()
			r.Build.State, r.Identity.Cleaned = "succeeded", true
			r.Build.Files[0].State = "succeeded"
			if scenario == "unchanged_result" {
				r.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: "previous-operation", Signature: strings.Repeat("c", 64), State: "succeeded"}
			}
			if scenario == "incomplete_file" {
				r.Build.Files[0].State = "pending"
			}
			if scenario == "foreign_lease" {
				r.Build.State, r.LeaseID, r.LeaseUntil = "linking", "other", 300
			}
			if scenario == "not_cleaned" {
				r.Identity.Cleaned = false
			}
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(scenario == "archived", "owner", "user"))
			if scenario != "archived" {
				mock.ExpectQuery("SELECT "+columns+" FROM "+table).WithArgs("project", "build").WillReturnRows(rowsFor(r))
			}
			if scenario == "claim" || scenario == "source_changed" || scenario == "another_build" || scenario == "unchanged_result" {
				revision := 7
				if scenario == "source_changed" {
					revision = 8
				}
				mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", revision))
				if scenario != "source_changed" {
					mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", 10))
					count := 0
					if scenario == "another_build" {
						count = 1
					}
					mock.ExpectQuery(regexp.QuoteMeta(otherLinkingBuilds)).WithArgs("project", "build").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
				}
			}
			switch scenario {
			case "claim":
				mock.ExpectExec("UPDATE " + table).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			case "unchanged_result":
				mock.ExpectCommit()
			default:
				mock.ExpectRollback()
			}
			claimed, err := New(db).(interfaces.KBLinkingAccess).ClaimLinking(context.Background(), "project", "build", r.Build.Owner, "operation", strings.Repeat("c", 64), 100, 200)
			if err != nil || (claimed != nil) != (scenario == "claim" || scenario == "unchanged_result") {
				t.Fatal("incorrect linking claim boundary", scenario, err)
			}
			if claimed != nil && scenario == "claim" && (claimed.Build.State != "linking" || claimed.Build.Linking.Signature != strings.Repeat("c", 64) || !claimed.Identity.Cleaned || claimed.Build.Revision != 2) {
				t.Fatal("extraction identity or linking intent changed", claimed)
			}
			if scenario == "unchanged_result" && (claimed.Build.Revision != 1 || claimed.Build.Linking.OperationID != "previous-operation") {
				t.Fatal("unchanged results created another mutation")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSaveLinkingCannotAlterExtractionOrOutliveItsLease(t *testing.T) {
	for _, scenario := range []string{"complete", "changed_file", "changed_signature", "expired", "revoked_owner", "source_changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			old := processRecord()
			old.Build.State, old.Build.Revision, old.Build.UpdateTime = "linking", 2, 100
			old.Identity.Cleaned = true
			old.LeaseID, old.LeaseUntil = "operation", 300
			old.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: "operation", Signature: strings.Repeat("c", 64), State: "running"}
			if scenario == "expired" {
				old.LeaseUntil = 199
			}
			next := *old
			copyBuild := *old.Build
			next.Build = &copyBuild
			next.Build.Files = slices.Clone(old.Build.Files)
			next.Build.State, next.Build.Revision, next.Build.UpdateTime = "succeeded", 3, 200
			next.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: "operation", Signature: strings.Repeat("c", 64), State: "succeeded", RowsWritten: 1, Eligible: 1, Linked: 1}
			if scenario == "changed_file" {
				next.Build.Files[0].AgentTaskID = "different-task"
			}
			if scenario == "changed_signature" {
				next.Build.Linking.Signature = strings.Repeat("d", 64)
			}
			mock.ExpectBegin()
			owner := "owner"
			if scenario == "revoked_owner" {
				owner = "another"
			}
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(false, owner, "user"))
			mock.ExpectQuery("SELECT "+columns+" FROM "+table).WithArgs("project", "build").WillReturnRows(rowsFor(old))
			if scenario != "expired" && scenario != "revoked_owner" {
				revision := 7
				if scenario == "source_changed" {
					revision = 8
				}
				mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", revision))
				if scenario != "source_changed" {
					mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", 10))
				}
			}
			if scenario == "complete" {
				mock.ExpectExec("UPDATE " + table).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			ok, err := New(db).(interfaces.KBLinkingAccess).SaveLinking(context.Background(), &next, 2, 200)
			if err != nil || ok != (scenario == "complete") {
				t.Fatal("invalid linking completion escaped", scenario, ok, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheckLinkingRenewsOnlyLiveFencedOperations(t *testing.T) {
	for _, scenario := range []string{"live", "same_millisecond", "clock_rollback", "expired", "cancelled", "replaced", "source_changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			r := processRecord()
			r.Build.State, r.Build.Revision = "linking", 2
			r.Identity.Cleaned = true
			r.LeaseID, r.LeaseUntil = "operation", 300
			r.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: "operation", Signature: strings.Repeat("c", 64), State: "running"}
			switch scenario {
			case "same_millisecond":
				r.LeaseUntil = 900200
			case "clock_rollback":
				r.LeaseUntil = 900300
			case "expired":
				r.LeaseUntil = 200
			case "cancelled":
				r.Build.State, r.Build.Linking.State = "succeeded", "cancelled"
			case "replaced":
				r.LeaseID = "replacement"
			}
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(false, "owner", "user"))
			mock.ExpectQuery("SELECT "+columns+" FROM "+table).WithArgs("project", "build").WillReturnRows(rowsFor(r))
			success := scenario == "live" || scenario == "same_millisecond" || scenario == "clock_rollback"
			if success || scenario == "source_changed" {
				revision := 7
				if scenario == "source_changed" {
					revision = 8
				}
				mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", revision))
				if success {
					mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", 10))
				}
			}
			if scenario == "live" {
				mock.ExpectExec("UPDATE "+table+" SET f_lease_until").WithArgs(int64(900200), "project", "build", int64(2), "operation").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			if success {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			ok, err := New(db).(interfaces.KBLinkingAccess).CheckLinking(context.Background(), "project", "build", "operation", 2, 200)
			if err != nil || ok != success {
				t.Fatal("incorrect lease renewal", scenario, ok, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
