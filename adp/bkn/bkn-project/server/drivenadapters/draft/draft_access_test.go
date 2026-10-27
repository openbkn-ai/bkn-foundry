// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

const emptyItems = `{"object_types":null,"relation_types":null,"value_types":null,"excluded":null,"unreadable":null,"decisions":null}`

const decidedItems = `{"decisions":[{"kind":"object_types","item_id":"orders","action":"unadopt"}]}`

func Test_DraftAccess(t *testing.T) {
	Convey("Test draftAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		da := &draftAccess{db: db}
		ctx := context.Background()
		drafting := &interfaces.Draft{
			ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING,
			Drafter: interfaces.AccountInfo{ID: "u1", Type: "user"}, CreateTime: 5, UpdateTime: 5,
		}
		lockRow := regexp.QuoteMeta("SELECT f_status, f_update_time, f_items FROM t_bkn_project_draft WHERE f_project_id = ? FOR UPDATE")
		lockCols := []string{"f_status", "f_update_time", "f_items"}

		Convey("StartDraft inserts the first draft of a project\n", func() {
			smock.ExpectBegin()
			smock.ExpectQuery(lockRow).WithArgs("p1").WillReturnRows(sqlmock.NewRows(lockCols))
			smock.ExpectExec(regexp.QuoteMeta(
				"INSERT INTO t_bkn_project_draft (f_project_id,f_status,f_items,f_error,f_drafter,f_drafter_type,f_create_time,f_update_time) VALUES (?,?,?,?,?,?,?,?)")).
				WithArgs("p1", "drafting", emptyItems, "", "u1", "user", int64(5), int64(5)).
				WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectCommit()

			started, err := da.StartDraft(ctx, drafting, 3, false)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDraft replaces a finished draft\n", func() {
			smock.ExpectBegin()
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("ready", int64(1), emptyItems))
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_draft SET f_status = ?, f_items = ?, f_error = ?")).
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := da.StartDraft(ctx, drafting, 3, false)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDraft leaves a project that is drafting alone\n", func() {
			smock.ExpectBegin()
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("drafting", int64(4), emptyItems))
			smock.ExpectRollback()

			started, err := da.StartDraft(ctx, drafting, 3, false)

			So(err, ShouldBeNil)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDraft takes over a draft left drafting since before the stale time\n", func() {
			smock.ExpectBegin()
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("drafting", int64(2), emptyItems))
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_draft SET f_status = ?")).
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := da.StartDraft(ctx, drafting, 3, false)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDraft rolls back when the write fails\n", func() {
			smock.ExpectBegin()
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols))
			smock.ExpectExec("INSERT INTO t_bkn_project_draft").WillReturnError(errors.New("lock wait timeout"))
			smock.ExpectRollback()

			_, err := da.StartDraft(ctx, drafting, 3, false)

			So(err, ShouldNotBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDraft keeps a draft people made decisions on unless told to discard them\n", func() {
			smock.ExpectBegin()
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("ready", int64(1), decidedItems))
			smock.ExpectRollback()

			started, err := da.StartDraft(ctx, drafting, 3, false)

			So(errors.Is(err, interfaces.ErrDraftHasDecisions), ShouldBeTrue)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDraft discards the decisions when told to\n", func() {
			smock.ExpectBegin()
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("ready", int64(1), decidedItems))
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_draft SET f_status = ?")).
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := da.StartDraft(ctx, drafting, 3, true)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("UpdateDraft writes a ready draft only if no one changed it since it was read\n", func() {
			edited := &interfaces.Draft{ProjectID: "p1", UpdateTime: 9}
			smock.ExpectExec(regexp.QuoteMeta(
				"UPDATE t_bkn_project_draft SET f_items = ?, f_update_time = ? WHERE f_project_id = ? AND f_status = ? AND f_update_time = ?")).
				WithArgs(emptyItems, int64(9), "p1", "ready", int64(7)).
				WillReturnResult(sqlmock.NewResult(0, 1))

			written, err := da.UpdateDraft(ctx, edited, 7)

			So(err, ShouldBeNil)
			So(written, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("UpdateDraft reports a draft someone changed meanwhile\n", func() {
			smock.ExpectExec("UPDATE t_bkn_project_draft").WillReturnResult(sqlmock.NewResult(0, 0))

			written, err := da.UpdateDraft(ctx, &interfaces.Draft{ProjectID: "p1", UpdateTime: 9}, 7)

			So(err, ShouldBeNil)
			So(written, ShouldBeFalse)
		})

		Convey("FinishDraft only ends a draft that is still drafting\n", func() {
			smock.ExpectExec(regexp.QuoteMeta(
				"UPDATE t_bkn_project_draft SET f_status = ?, f_items = ?, f_error = ?, f_update_time = ? WHERE f_project_id = ? AND f_status = ?")).
				WithArgs("ready", emptyItems, "", int64(9), "p1", "drafting").
				WillReturnResult(sqlmock.NewResult(0, 1))

			err := da.FinishDraft(ctx, &interfaces.Draft{ProjectID: "p1", Status: "ready", UpdateTime: 9})

			So(err, ShouldBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FailDrafting fails every draft left drafting\n", func() {
			smock.ExpectExec(regexp.QuoteMeta(
				"UPDATE t_bkn_project_draft SET f_status = ?, f_error = ?, f_update_time = ? WHERE f_status = ?")).
				WithArgs("failed", "restarted", int64(7), "drafting").
				WillReturnResult(sqlmock.NewResult(0, 2))

			n, err := da.FailDrafting(ctx, "restarted", 7)

			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)
		})

		Convey("GetDraft reads the items back\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_project_id, f_status, f_items")).
				WithArgs("p1").
				WillReturnRows(sqlmock.NewRows(draftColumns).AddRow(
					"p1", "ready", `{"object_types":[{"id":"orders","name":"Orders"}]}`, "", "u1", "user", int64(1), int64(2)))

			draft, exist, err := da.GetDraft(ctx, "p1")

			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(draft.Items.ObjectTypes[0].ID, ShouldEqual, "orders")
			So(draft.Drafter, ShouldResemble, interfaces.AccountInfo{ID: "u1", Type: "user"})
		})

		Convey("GetDraft reports a project with no draft\n", func() {
			smock.ExpectQuery("FROM t_bkn_project_draft").WillReturnRows(sqlmock.NewRows(draftColumns))

			_, exist, err := da.GetDraft(ctx, "p1")

			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
		})
	})
}
