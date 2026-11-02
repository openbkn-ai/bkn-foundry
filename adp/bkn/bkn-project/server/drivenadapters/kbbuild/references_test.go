// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbbuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func referenceFixture() *interfaces.KBReference {
	b := processRecord().Build
	file := b.Files[0].File
	file.SampleRole = "acceptance"
	return &interfaces.KBReference{ID: "reference", ProjectID: b.ProjectID, Owner: b.Owner, ModelFingerprint: b.ModelFingerprint, SelectionID: b.SelectionID, SelectionRevision: b.SelectionRevision, DraftUpdateTime: b.DraftUpdateTime, GenerationID: b.GenerationID, Revision: 1, UpdateTime: 11, Files: []interfaces.KBReferenceFile{{File: file}}}
}
func TestReferenceDecisionIsFencedByOwnerScopeAndReviewRevision(t *testing.T) {
	for _, scenario := range []string{"create", "stale_revision", "archived", "source_changed", "draft_changed"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			next := referenceFixture()
			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(scenario == "archived", "owner", "user"))
			if scenario != "archived" {
				revision := 7
				if scenario == "source_changed" {
					revision = 8
				}
				mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", revision))
				if scenario != "source_changed" {
					at := 10
					if scenario == "draft_changed" {
						at = 12
					}
					mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"state", "time"}).AddRow("ready", at))
					if scenario != "draft_changed" {
						query := mock.ExpectQuery("SELECT f_id,f_project_id,f_revision,f_data FROM "+referenceTable).WithArgs("project", "reference")
						if scenario == "stale_revision" {
							previous := *next
							previous.Revision = 2
							raw, _ := json.Marshal(previous)
							query.WillReturnRows(sqlmock.NewRows([]string{"id", "project", "revision", "data"}).AddRow("reference", "project", 2, string(raw)))
						} else {
							query.WillReturnError(sql.ErrNoRows)
						}
					}
				}
			}
			if scenario == "create" {
				mock.ExpectExec("INSERT INTO "+referenceTable).WithArgs("reference", "project", int64(1), sqlmock.AnyArg(), int64(11)).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			saved, err := NewReferences(db).Put(context.Background(), next, 0)
			if err != nil || saved != (scenario == "create") {
				t.Fatal("stale review decision accepted", saved, err)
			}
			if err = mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestReferenceRejectsUnconfirmedAndDebugFilesBeforeAnyDatabaseWrite(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, scenario := range []string{"debug", "unconfirmed"} {
		next := referenceFixture()
		if scenario == "debug" {
			next.Files[0].File.SampleRole = "debug"
		} else {
			next.Files[0].RowID = strings.Repeat("a", 64)
		}
		if saved, err := NewReferences(db).Put(context.Background(), next, 0); err != nil || saved {
			t.Fatal("invalid reference decision persisted", scenario, err)
		}
	}
	if err = mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
