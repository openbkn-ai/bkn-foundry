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

func publicationFixture() *interfaces.KBPublication {
	return &interfaces.KBPublication{NetworkID: "lineage_1.0.0", ProjectID: "project", LineageID: "lineage", DefinitionCommit: "commit", ModelFingerprint: strings.Repeat("b", 64), GenerationID: "generation", FullBuildID: "build", Publisher: interfaces.AccountInfo{ID: "owner", Type: "user"}, RuntimeDigest: strings.Repeat("c", 64), RuntimeRowID: strings.Repeat("c", 64), DefinitionDigest: strings.Repeat("d", 64), Ranges: []interfaces.KBSourceRange{{KnowledgeBaseID: "1", CatalogID: "2"}}, Snapshot: interfaces.DraftDatasetSpec{Owner: interfaces.AccountInfo{ID: "storage", Type: "app"}, ProjectID: "project", GenerationID: "publication", ObjectTypeID: "published_model", ResourceID: "resource", CatalogID: "catalog"}, State: "preparing", Revision: 1, DraftUpdateTime: 10, CreateTime: 20, UpdateTime: 20}
}
func publicationRows(p *interfaces.KBPublication) *sqlmock.Rows {
	raw, _ := json.Marshal(p)
	return sqlmock.NewRows(strings.Split(publicationColumns, ",")).AddRow(p.NetworkID, p.ProjectID, p.GenerationID, p.State, p.Revision, string(raw), p.CreateTime, p.UpdateTime)
}
func TestPublicationSealRefusesActiveUncertainStaleAndForeignWriters(t *testing.T) {
	for _, scenario := range []string{"seal", "active", "source_changed", "draft_changed", "model_changed", "foreign_owner", "archived", "wrong_digest"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			p := publicationFixture()
			expectedDigest := p.RuntimeDigest
			if scenario == "wrong_digest" {
				expectedDigest = strings.Repeat("e", 64)
			}
			mock.ExpectQuery("SELECT .* FROM " + publicationTable).WithArgs(p.NetworkID).WillReturnRows(publicationRows(p))
			if scenario != "wrong_digest" {
				mock.ExpectBegin()
				owner := "owner"
				if scenario == "foreign_owner" {
					owner = "another"
				}
				mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(scenario == "archived", owner, "user"))
				if scenario != "archived" && scenario != "foreign_owner" {
					mock.ExpectQuery("SELECT .* FROM " + publicationTable + ".*FOR UPDATE").WithArgs(p.NetworkID).WillReturnRows(publicationRows(p))
					full := processRecord()
					full.Build.Mode = "full"
					full.Build.State = "succeeded"
					full.Identity.Cleaned = true
					full.Build.Files[0].State = "succeeded"
					if scenario == "model_changed" {
						full.Build.ModelFingerprint = strings.Repeat("f", 64)
					}
					mock.ExpectQuery("SELECT .* FROM "+table+".*FOR UPDATE").WithArgs("project", "build").WillReturnRows(rowsFor(full))
					if scenario != "model_changed" {
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
							mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", at))
							if scenario != "draft_changed" {
								active := 0
								if scenario == "active" {
									active = 1
								}
								mock.ExpectQuery(regexp.QuoteMeta(activeBuilds)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(active))
							}
						}
					}
				}
				if scenario == "seal" {
					mock.ExpectExec("UPDATE "+publicationTable).WithArgs("sealed", int64(2), sqlmock.AnyArg(), int64(30), p.NetworkID, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit()
				} else {
					mock.ExpectRollback()
				}
			}
			ok, err := NewPublications(db).Seal(context.Background(), p.NetworkID, expectedDigest, 30)
			if err != nil || ok != (scenario == "seal") {
				t.Fatal("unsafe publication seal", ok, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSealedGenerationCannotBeReopenedByAnInteractiveLinkingClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	full := processRecord()
	full.Build.State = "succeeded"
	full.Identity.Cleaned = true
	full.Build.Files[0].State = "succeeded"
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(projectLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"archived", "owner", "type"}).AddRow(false, "owner", "user"))
	mock.ExpectQuery("SELECT .* FROM "+table+".*FOR UPDATE").WithArgs("project", "build").WillReturnRows(rowsFor(full))
	mock.ExpectQuery(regexp.QuoteMeta(selectionLock)).WithArgs("project", "selection").WillReturnRows(sqlmock.NewRows([]string{"state", "revision"}).AddRow("active", 7))
	mock.ExpectQuery(regexp.QuoteMeta(draftLock)).WithArgs("project").WillReturnRows(sqlmock.NewRows([]string{"status", "time"}).AddRow("ready", 10))
	mock.ExpectQuery(regexp.QuoteMeta(sealedGenerations)).WithArgs("project", "generation").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectRollback()
	result, err := New(db).(interfaces.KBLinkingAccess).ClaimLinking(context.Background(), "project", "build", full.Build.Owner, "operation", strings.Repeat("c", 64), 30, 90)
	if err != nil || result != nil {
		t.Fatal("published Dataset reopened for writes", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationActivationUsesRecordedReleaseWithoutReadingMutableProject(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	p := publicationFixture()
	p.State = "mounted"
	p.Revision = 3
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM " + publicationTable + ".*FOR UPDATE").WithArgs(p.NetworkID).WillReturnRows(publicationRows(p))
	mock.ExpectQuery(regexp.QuoteMeta(publicationRelease)).WithArgs(p.NetworkID, p.ProjectID, p.LineageID, p.Publisher.ID, p.Publisher.Type).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec("UPDATE "+publicationTable).WithArgs("active", int64(4), sqlmock.AnyArg(), int64(40), p.NetworkID, int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	ok, err := NewPublications(db).Activate(context.Background(), p.NetworkID, p.RuntimeDigest, 40)
	if err != nil || !ok {
		t.Fatal("recorded release depended on mutable project", ok, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
