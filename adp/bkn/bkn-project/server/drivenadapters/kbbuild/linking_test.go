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
					mock.ExpectQuery(regexp.QuoteMeta(sealedGenerations)).WithArgs("project", "generation").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
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
					mock.ExpectQuery(regexp.QuoteMeta(sealedGenerations)).WithArgs("project", "generation").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
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

func TestSaveLinkingPersistsOnlyIndependentAcceptanceLinkMetrics(t *testing.T) {
	for _, scenario := range []string{"measured", "low_score", "changed_extraction", "changed_task", "changed_issues", "bad_ratio", "aggregate_mismatch", "missing_reference", "attention", "debug", "full"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			precision, recall, linking := 10.0/11, 1.0, 1.0
			old := processRecord()
			old.Build.Mode, old.Build.State, old.Build.Revision, old.Build.UpdateTime = "acceptance", "linking", 2, 100
			old.Build.ReferenceID, old.Build.ReferenceRevision = "reviewed-reference", 6
			old.Identity.Cleaned = true
			old.LeaseID, old.LeaseUntil = "operation", 300
			old.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: "operation", Signature: strings.Repeat("c", 64), State: "running"}
			old.Build.Files[0].State, old.Build.Files[0].AgentTaskID = "succeeded", "original-task"
			old.Build.Files[0].Assessment = &interfaces.KBAcceptanceCounts{Expected: 10, Predicted: 11, Correct: 10, Precision: &precision, Recall: &recall, Issues: map[string]int{"unexpected_or_incorrect": 1}}
			old.Build.Assessment = old.Build.Files[0].Assessment
			if scenario == "debug" || scenario == "full" {
				old.Build.Mode = scenario
			}
			if scenario == "missing_reference" {
				old.Build.ReferenceID, old.Build.ReferenceRevision = "", 0
			}
			next := *old
			copyBuild := *old.Build
			next.Build = &copyBuild
			next.Build.Files = slices.Clone(old.Build.Files)
			next.Build.State, next.Build.Revision, next.Build.UpdateTime = "succeeded", 3, 200
			next.Build.Linking = &interfaces.KBLinkingReceipt{OperationID: "operation", Signature: strings.Repeat("c", 64), State: "succeeded", RowsWritten: 1, Eligible: 2, Linked: 2}
			score := &interfaces.KBAcceptanceCounts{Expected: 10, Predicted: 11, Correct: 10, ExpectedLinks: 2, PredictedLinks: 3, CorrectLinks: 2, Precision: &precision, Recall: &recall, Linking: &linking, Passed: true, Issues: map[string]int{"unexpected_or_incorrect": 2}}
			if scenario == "low_score" {
				linking, score.CorrectLinks, score.Passed = 0.5, 1, false
				score.Issues = map[string]int{"missing": 1, "unexpected_or_incorrect": 3}
			}
			if scenario == "changed_extraction" {
				score.Correct = 9
			}
			if scenario == "changed_task" {
				next.Build.Files[0].AgentTaskID = "unrelated-task"
			}
			if scenario == "changed_issues" {
				score.Issues["unexpected_or_incorrect"] = 99
			}
			if scenario == "bad_ratio" {
				linking = 0.5
			}
			next.Build.Files[0].Assessment, next.Build.Assessment = score, score
			if scenario == "aggregate_mismatch" {
				total := *score
				total.ExpectedLinks = 3
				ratio := 2.0 / 3
				total.Linking = &ratio
				total.Issues = map[string]int{"missing": 1, "unexpected_or_incorrect": 2}
				next.Build.Assessment = &total
			}
			if scenario == "attention" {
				next.Build.State, next.Build.Linking.State = "linking_attention", "attention"
			}
			payload, identity, err := encode(&next)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(false, "owner", "user"))
			mock.ExpectQuery("SELECT "+columns+" FROM "+table).WithArgs("project", "build").WillReturnRows(rowsFor(old))
			mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", 7))
			mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", 10))
			mock.ExpectQuery(regexp.QuoteMeta(sealedGenerations)).WithArgs("project", "generation").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			success := scenario == "measured" || scenario == "low_score"
			if success {
				mock.ExpectExec("UPDATE "+table).WithArgs("succeeded", int64(3), payload, identity, int64(200), "project", "build", int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			ok, err := New(db).(interfaces.KBLinkingAccess).SaveLinking(context.Background(), &next, 2, 200)
			if err != nil || ok != success {
				t.Fatal("incorrect acceptance persistence boundary", scenario, ok, err)
			}
			after, _, _ := encode(&next)
			if after != payload || old.Build.Files[0].Assessment.ExpectedLinks != 0 {
				t.Fatal("saving mutated caller or baseline metrics")
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
					mock.ExpectQuery(regexp.QuoteMeta(sealedGenerations)).WithArgs("project", "generation").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
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
