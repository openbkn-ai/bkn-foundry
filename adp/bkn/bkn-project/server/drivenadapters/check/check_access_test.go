// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package check

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_CheckAccess(t *testing.T) {
	Convey("Test checkAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ca := &checkAccess{db: db}
		ctx := context.Background()
		running := &interfaces.Check{
			ProjectID: "p1", Commit: "c2", Status: interfaces.CHECK_STATUS_RUNNING, RunID: "r2", Attempt: 1,
			Runner: interfaces.AccountInfo{ID: "u1", Type: "user"}, Input: json.RawMessage(`{"object_types":[]}`),
			StartTime: 10,
		}
		lockRow := regexp.QuoteMeta("SELECT f_commit, f_status, f_start_time FROM t_bkn_project_check WHERE f_project_id = ? FOR UPDATE")
		lockCols := []string{"f_commit", "f_status", "f_start_time"}
		replace := regexp.QuoteMeta("UPDATE t_bkn_project_check SET f_commit = ?, f_status = ?, f_run_id = ?")
		ensureRow := regexp.QuoteMeta(
			"INSERT INTO t_bkn_project_check (f_project_id,f_input,f_results,f_error) VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE f_project_id = f_project_id")

		Convey("StartCheck writes the first check of a project into the row it makes sure of\n", func() {
			smock.ExpectBegin()
			// Two first starts both find the row: the second waits on the first's, then sees it.
			smock.ExpectExec(ensureRow).WithArgs("p1", "", "", "").WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectQuery(lockRow).WithArgs("p1").WillReturnRows(sqlmock.NewRows(lockCols).AddRow("", "", int64(0)))
			smock.ExpectExec(replace).WithArgs("c2", "running", "r2", 1, "u1", "user", `{"object_types":[]}`, "", "",
				int64(10), int64(0), "p1").
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ca.StartCheck(ctx, running, 5)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartCheck replaces a finished check\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("c2", "failed", int64(8)))
			smock.ExpectExec(replace).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ca.StartCheck(ctx, running, 5)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartCheck leaves a running check of the same commit alone\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("c2", "running", int64(8)))
			smock.ExpectRollback()

			started, err := ca.StartCheck(ctx, running, 5)

			So(err, ShouldBeNil)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartCheck replaces a running check of an earlier commit\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("c1", "running", int64(8)))
			smock.ExpectExec(replace).WithArgs("c2", "running", "r2", 1, "u1", "user", `{"object_types":[]}`, "", "",
				int64(10), int64(0), "p1").
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ca.StartCheck(ctx, running, 5)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartCheck takes over a check running since before the stale time\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("c2", "running", int64(4)))
			smock.ExpectExec(replace).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ca.StartCheck(ctx, running, 5)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartCheck rolls back when the write fails\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("c1", "passed", int64(8)))
			smock.ExpectExec(replace).WillReturnError(errors.New("db down"))
			smock.ExpectRollback()

			started, err := ca.StartCheck(ctx, running, 5)

			So(err, ShouldNotBeNil)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartCheck rolls back when the row cannot be made sure of\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnError(errors.New("db down"))
			smock.ExpectRollback()

			started, err := ca.StartCheck(ctx, running, 5)

			So(err, ShouldNotBeNil)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FinishCheck records only the run that is still the project's running check\n", func() {
			finished := *running
			finished.Status, finished.Attempt, finished.FinishTime = interfaces.CHECK_STATUS_ISSUES, 2, 20
			finished.Results = json.RawMessage(`{"keys":[]}`)
			smock.ExpectExec(regexp.QuoteMeta(
				"UPDATE t_bkn_project_check SET f_status = ?, f_attempt = ?, f_results = ?, f_error = ?, f_finish_time = ? WHERE f_project_id = ? AND f_run_id = ? AND f_status = ?")).
				WithArgs("issues", 2, `{"keys":[]}`, "", int64(20), "p1", "r2", "running").
				WillReturnResult(sqlmock.NewResult(0, 1))

			written, err := ca.FinishCheck(ctx, &finished)

			So(err, ShouldBeNil)
			So(written, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FinishCheck reports a run a later one replaced\n", func() {
			smock.ExpectExec("UPDATE t_bkn_project_check SET f_status").WillReturnResult(sqlmock.NewResult(0, 0))

			written, err := ca.FinishCheck(ctx, running)

			So(err, ShouldBeNil)
			So(written, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FailRunning fails every check left running\n", func() {
			smock.ExpectExec(regexp.QuoteMeta(
				"UPDATE t_bkn_project_check SET f_status = ?, f_error = ?, f_finish_time = ? WHERE f_status = ?")).
				WithArgs("failed", "interrupted", int64(30), "running").
				WillReturnResult(sqlmock.NewResult(0, 2))

			n, err := ca.FailRunning(ctx, "interrupted", 30)

			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("GetCheck reads the input and results back\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_project_id, f_commit")).WithArgs("p1").
				WillReturnRows(sqlmock.NewRows(checkColumns).AddRow(
					"p1", "c2", "passed", "r2", 1, "u1", "user", `{"object_types":[]}`, `{"keys":[]}`, "", int64(10), int64(20)))

			check, exist, err := ca.GetCheck(ctx, "p1")

			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(check.Commit, ShouldEqual, "c2")
			So(check.Runner, ShouldResemble, interfaces.AccountInfo{ID: "u1", Type: "user"})
			So(string(check.Input), ShouldEqual, `{"object_types":[]}`)
			So(string(check.Results), ShouldEqual, `{"keys":[]}`)
			So(check.FinishTime, ShouldEqual, 20)
		})

		Convey("GetCheck reads a running check's empty results as none\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_project_id, f_commit")).
				WillReturnRows(sqlmock.NewRows(checkColumns).AddRow(
					"p1", "c2", "running", "r2", 1, "u1", "user", `{}`, "", "", int64(10), int64(0)))

			check, _, err := ca.GetCheck(ctx, "p1")

			So(err, ShouldBeNil)
			So(check.Results, ShouldBeNil)
			out, err := json.Marshal(check)
			So(err, ShouldBeNil)
			So(string(out), ShouldContainSubstring, `"results":null`)
			So(string(out), ShouldNotContainSubstring, "r2")
		})

		Convey("GetCheck reports a project with no check\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_project_id, f_commit")).
				WillReturnRows(sqlmock.NewRows(checkColumns))

			check, exist, err := ca.GetCheck(ctx, "p1")

			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(check, ShouldBeNil)
		})
	})
}
