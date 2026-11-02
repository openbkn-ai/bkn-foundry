// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func processRecord() *interfaces.KBBuildRecord {
	return &interfaces.KBBuildRecord{InputHash: strings.Repeat("a", 64), Identity: interfaces.KBBuildIdentity{GrantID: "grant"}, Build: &interfaces.KBBuild{ID: "build", ProjectID: "project", NetworkID: "lineage", Owner: interfaces.AccountInfo{ID: "owner", Type: "user"}, Mode: "debug", State: "preparing", Revision: 1, SelectionID: "selection", SelectionRevision: 7, DraftUpdateTime: 10, DefinitionCommit: "commit", ModelFingerprint: strings.Repeat("b", 64), GenerationID: "generation", AgentVersion: 3, Files: []interfaces.KBFileBuild{{File: interfaces.KBResourceFile{KnowledgeBaseID: "1", FileID: "2", ContentVersionID: "3", SampleRole: "debug"}, State: "pending"}}, CreateTime: 11, UpdateTime: 11}}
}
func TestCreateBuildLocksAndRechecksOwnerSourceAndConfirmedDraft(t *testing.T) {
	for _, scenario := range []string{"create", "busy", "archived", "owner_changed", "source_changed", "draft_changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			owner := "owner"
			if scenario == "owner_changed" {
				owner = "another"
			}
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(scenario == "archived", owner, "user"))
			if scenario != "archived" && scenario != "owner_changed" {
				revision := 7
				if scenario == "source_changed" || scenario == "attention_source_changed" {
					revision = 8
				}
				mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", revision))
				if scenario != "source_changed" {
					at := 10
					if scenario == "draft_changed" {
						at = 12
					}
					mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", at))
				}
			}
			if scenario == "create" || scenario == "busy" {
				count := 0
				if scenario == "busy" {
					count = 1
				}
				mock.ExpectQuery(regexp.QuoteMeta(activeBuilds)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
			}
			if scenario == "create" {
				mock.ExpectExec("INSERT INTO "+table).WithArgs("build", "project", "preparing", int64(1), strings.Repeat("a", 64), sqlmock.AnyArg(), sqlmock.AnyArg(), "", int64(0), int64(11), int64(11)).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			written, err := New(db).Create(context.Background(), processRecord())
			if err != nil || written != (scenario == "create") {
				t.Fatal("stale build persisted", written, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func rowsFor(record *interfaces.KBBuildRecord) *sqlmock.Rows {
	payload, _ := json.Marshal(record.Build)
	identity, _ := json.Marshal(record.Identity)
	b := record.Build
	return sqlmock.NewRows(strings.Split(columns, ",")).AddRow(b.ID, b.ProjectID, b.State, b.Revision, record.InputHash, string(payload), string(identity), record.LeaseID, record.LeaseUntil, b.CreateTime, b.UpdateTime)
}
func TestSaveCannotChangeScopeOrUseExpiredOrForeignWorkerLease(t *testing.T) {
	for _, scenario := range []string{"save", "expired", "foreign_lease", "file_changed", "model_changed", "source_changed", "attention_source_changed", "attention_archived", "attention_owner_changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			old := processRecord()
			old.Build.State = "running"
			old.Build.Revision = 2
			old.Build.UpdateTime = 20
			old.LeaseID = "worker"
			old.LeaseUntil = 100
			next := *old
			raw, _ := json.Marshal(old.Build)
			next.Build = &interfaces.KBBuild{}
			if json.Unmarshal(raw, next.Build) != nil {
				t.Fatal("fixture")
			}
			next.Build.Revision = 3
			next.Build.UpdateTime = 21
			next.Build.Files[0].State = "submitting"
			switch scenario {
			case "expired":
				old.LeaseUntil = 20
			case "foreign_lease":
				next.LeaseID = "another"
			case "file_changed":
				next.Build.Files[0].File.ContentVersionID = "different"
			case "model_changed":
				next.Build.ModelFingerprint = strings.Repeat("c", 64)
			}
			if strings.HasPrefix(scenario, "attention_") {
				next.Build.State = "attention"
			}
			owner := "owner"
			if scenario == "attention_owner_changed" {
				owner = "different"
			}
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(scenario == "attention_archived", owner, "user"))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE")).WithArgs("project", "build").WillReturnRows(rowsFor(old))
			if scenario == "save" || scenario == "source_changed" || scenario == "attention_source_changed" {
				revision := 7
				if scenario == "source_changed" || scenario == "attention_source_changed" {
					revision = 8
				}
				mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", revision))
				if scenario == "save" {
					mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", 10))
					mock.ExpectExec("UPDATE "+table).WithArgs("running", int64(3), sqlmock.AnyArg(), sqlmock.AnyArg(), "worker", int64(100), int64(21), "project", "build", int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				} else {
					mock.ExpectRollback()
				}
			} else {
				mock.ExpectRollback()
			}
			saved, err := New(db).Save(context.Background(), &next, 2, 21)
			if err != nil || saved != (scenario == "save") {
				t.Fatal("invalid process update accepted", saved, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestPrivateProcessRecordDoesNotSerializeDelegationOrLeaseIntoResponses(t *testing.T) {
	record := processRecord()
	record.LeaseID = "private-worker"
	record.Identity.PrincipalID = "private-principal"
	raw, err := json.Marshal(record)
	if err != nil || string(raw) != "{}" {
		t.Fatal("private process storage leaked into a business DTO")
	}
}

func TestLeaseClaimCannotStealActiveWorkerAndTurnsStaleInputsIntoCleanupOnly(t *testing.T) {
	for _, scenario := range []string{"claim", "leased", "archived", "source_changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			record := processRecord()
			record.Build.State = "running"
			record.LeaseID = "old-worker"
			record.LeaseUntil = 20
			if scenario == "leased" {
				record.LeaseUntil = 100
			}
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(scenario == "archived", "owner", "user"))
			mock.ExpectQuery(regexp.QuoteMeta("SELECT "+columns+" FROM "+table+" WHERE f_project_id=? AND f_id=? FOR UPDATE")).WithArgs("project", "build").WillReturnRows(rowsFor(record))
			if scenario == "leased" {
				mock.ExpectRollback()
			} else {
				if scenario != "archived" {
					revision := 7
					if scenario == "source_changed" {
						revision = 8
					}
					mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", revision))
					if scenario == "claim" {
						mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", 10))
					}
				}
				state := "running"
				revision := int64(1)
				updated := int64(11)
				if scenario != "claim" {
					state = "cancel_pending"
					revision = 2
					updated = 21
				}
				mock.ExpectExec("UPDATE "+table).WithArgs(state, revision, sqlmock.AnyArg(), sqlmock.AnyArg(), "new-worker", int64(60), updated, "project", "build").WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			got, err := New(db).Claim(context.Background(), "project", "build", "new-worker", 21, 60)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "leased" {
				if got != nil {
					t.Fatal("lease stolen from another worker")
				}
			} else {
				if got == nil || got.LeaseID != "new-worker" {
					t.Fatal("lease missing")
				}
				if scenario != "claim" && got.Build.State != "cancel_pending" {
					t.Fatal("stale scope remained runnable")
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
