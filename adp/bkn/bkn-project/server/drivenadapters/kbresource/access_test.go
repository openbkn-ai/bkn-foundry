// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kbresource

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	. "github.com/smartystreets/goconvey/convey"
)

func TestSelectionPersistence(t *testing.T) {
	Convey("Source intents serialize with project decisions before any remote hold", t, func() {
		db, mock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer db.Close()
		access := New(db)
		ctx := context.Background()
		selection := &interfaces.KBSelection{ID: "op", ProjectID: "p", ExpectedID: "old", InputHash: "hash", Ranges: []interfaces.KBSourceRange{}, Files: []interfaces.KBResourceFile{}, Holds: []interfaces.KBHoldGroup{{ID: "private-hold-uuid", VersionIDs: []string{"11"}}}}
		Convey("Archiving prevents a new intent", func() {
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT f_archived FROM t_bkn_project_project").WithArgs("p").WillReturnRows(sqlmock.NewRows([]string{"f_archived"}).AddRow(true))
			mock.ExpectRollback()
			So(access.Prepare(ctx, selection), ShouldEqual, interfaces.ErrKBSelectionConflict)
		})
		Convey("A stale selection cannot replace the current one", func() {
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT f_archived FROM t_bkn_project_project").WithArgs("p").WillReturnRows(sqlmock.NewRows([]string{"f_archived"}).AddRow(false))
			mock.ExpectQuery("SELECT f_id FROM t_bkn_project_kb_selection.*f_current").WithArgs("p").WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("newer"))
			mock.ExpectRollback()
			So(access.Prepare(ctx, selection), ShouldEqual, interfaces.ErrKBSelectionConflict)
		})
		Convey("A pending operation prevents a second set of anonymous holds", func() {
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT f_archived FROM t_bkn_project_project").WillReturnRows(sqlmock.NewRows([]string{"f_archived"}).AddRow(false))
			mock.ExpectQuery("SELECT f_id FROM t_bkn_project_kb_selection.*f_current").WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("old"))
			mock.ExpectQuery("SELECT f_id FROM t_bkn_project_kb_selection.*f_state").WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("in-progress"))
			mock.ExpectRollback()
			So(access.Prepare(ctx, selection), ShouldEqual, interfaces.ErrKBSelectionConflict)
		})
		Convey("A database failure leaves no half-written intent", func() {
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT f_archived FROM t_bkn_project_project").WillReturnRows(sqlmock.NewRows([]string{"f_archived"}).AddRow(false))
			mock.ExpectQuery("SELECT f_id FROM t_bkn_project_kb_selection.*f_current").WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("old"))
			mock.ExpectQuery("SELECT f_id FROM t_bkn_project_kb_selection.*f_state").WillReturnError(sql.ErrNoRows)
			mock.ExpectExec("INSERT INTO t_bkn_project_kb_selection").WillReturnError(sql.ErrConnDone)
			mock.ExpectRollback()
			So(access.Prepare(ctx, selection), ShouldEqual, sql.ErrConnDone)
		})
		Convey("Public snapshots never expose hold capabilities", func() {
			raw, err := json.Marshal(interfaces.KBResourceState{Pending: selection})
			So(err, ShouldBeNil)
			So(string(raw), ShouldNotContainSubstring, "private-hold-uuid")
		})
		Convey("Clearing an unarchived history does not requeue already released holds", func() {
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT f_archived FROM t_bkn_project_project").WithArgs("p").WillReturnRows(sqlmock.NewRows([]string{"f_archived"}).AddRow(false))
			mock.ExpectExec("UPDATE t_bkn_project_kb_selection.*WHEN f_state='released' THEN 'released'").WithArgs(sqlmock.AnyArg(), "p").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			So(access.Clear(ctx, "p"), ShouldBeNil)
		})
		So(mock.ExpectationsWereMet(), ShouldBeNil)
	})
}
