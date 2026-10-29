// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package regression

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_RegressionAccess(t *testing.T) {
	Convey("Test regressionAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ra := &regressionAccess{db: db}
		ctx := context.Background()
		questions := `[{"id":"q1","text":"Which supplier?","needs":[{"kind":"object_type","id":"supplier"}]}]`
		running := &interfaces.Regression{
			NetworkID: "supply_v1_1_0", ProjectID: "p1", Version: "1.1.0", Status: interfaces.REGRESSION_STATUS_RUNNING,
			RunID: "r2", Attempt: 1, Runner: interfaces.AccountInfo{ID: "u1", Type: "user"},
			Questions: []interfaces.RegressionQuestion{{ID: "q1", Text: "Which supplier?",
				Needs: []interfaces.RequirementItemRef{{Kind: "object_type", ID: "supplier"}}}},
			StartTime: 10,
		}
		columns := []string{"f_network_id", "f_project_id", "f_version", "f_status", "f_run_id", "f_attempt",
			"f_runner", "f_runner_type", "f_questions", "f_results", "f_error", "f_start_time", "f_finish_time"}
		ensureRow := regexp.QuoteMeta("INSERT INTO t_bkn_project_regression (f_network_id,f_questions,f_results) " +
			"VALUES (?,?,?) ON DUPLICATE KEY UPDATE f_network_id = f_network_id")
		lockRow := regexp.QuoteMeta("SELECT f_status, f_start_time FROM t_bkn_project_regression WHERE f_network_id = ? FOR UPDATE")
		replace := regexp.QuoteMeta("UPDATE t_bkn_project_regression SET f_project_id = ?, f_version = ?, f_status = ?")

		Convey("GetRegression reads a release's regression with its questions and results\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_network_id, f_project_id, f_version")).WithArgs("supply_v1_1_0").
				WillReturnRows(sqlmock.NewRows(columns).AddRow("supply_v1_1_0", "p1", "1.1.0", "not_passed", "r2", 2, "u1",
					"user", questions,
					`[{"question_id":"q1","outcome":"missing","missing":[{"kind":"object_type","id":"supplier","name":"supplier"}]}]`,
					"", int64(10), int64(20)))

			r, exist, err := ra.GetRegression(ctx, "supply_v1_1_0")

			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(r, ShouldResemble, &interfaces.Regression{
				NetworkID: "supply_v1_1_0", ProjectID: "p1", Version: "1.1.0", Status: "not_passed", RunID: "r2",
				Attempt: 2, Runner: interfaces.AccountInfo{ID: "u1", Type: "user"}, Questions: running.Questions,
				Results: []interfaces.RegressionResult{{QuestionID: "q1", Outcome: "missing",
					Missing: []interfaces.ReleaseItem{{Kind: "object_type", ID: "supplier", Name: "supplier"}}}},
				StartTime: 10, FinishTime: 20,
			})
		})

		Convey("GetRegression reads a release without one as none, and empty columns as empty lists\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_network_id")).WillReturnRows(sqlmock.NewRows(columns))

			_, exist, err := ra.GetRegression(ctx, "supply_v1_1_0")

			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)

			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_network_id")).WillReturnRows(sqlmock.NewRows(columns).AddRow(
				"supply_v1_1_0", "p1", "1.1.0", "running", "r2", 1, "u1", "user", "", "", "", int64(10), int64(0)))

			r, _, err := ra.GetRegression(ctx, "supply_v1_1_0")

			So(err, ShouldBeNil)
			So(r.Questions, ShouldResemble, []interfaces.RegressionQuestion{})
			So(r.Results, ShouldResemble, []interfaces.RegressionResult{})
		})

		Convey("GetRegression passes a failure to read on\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_network_id")).WillReturnError(errors.New("db down"))

			_, _, err := ra.GetRegression(ctx, "supply_v1_1_0")

			So(err, ShouldNotBeNil)
		})

		Convey("StartRegression writes the first regression of a release into the row it makes sure of\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WithArgs("supply_v1_1_0", "", "").WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectQuery(lockRow).WithArgs("supply_v1_1_0").
				WillReturnRows(sqlmock.NewRows([]string{"f_status", "f_start_time"}).AddRow("", int64(0)))
			smock.ExpectExec(replace).WithArgs("p1", "1.1.0", "running", "r2", 1, "u1", "user", questions, "[]", "",
				int64(10), int64(0), "supply_v1_1_0").WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ra.StartRegression(ctx, running, 5)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartRegression replaces a finished regression and takes over a stale one\n", func() {
			for _, row := range [][]driver.Value{{"failed", int64(8)}, {"running", int64(4)}} {
				smock.ExpectBegin()
				smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
				smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows([]string{"f_status", "f_start_time"}).AddRow(row...))
				smock.ExpectExec(replace).WillReturnResult(sqlmock.NewResult(0, 1))
				smock.ExpectCommit()

				started, err := ra.StartRegression(ctx, running, 5)

				So(err, ShouldBeNil)
				So(started, ShouldBeTrue)
				So(smock.ExpectationsWereMet(), ShouldBeNil)
			}
		})

		Convey("StartRegression leaves a regression running since staleBefore alone\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows([]string{"f_status", "f_start_time"}).AddRow("running", int64(5)))
			smock.ExpectRollback()

			started, err := ra.StartRegression(ctx, running, 5)

			So(err, ShouldBeNil)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartRegression rolls back when a write fails\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnError(errors.New("db down"))
			smock.ExpectRollback()

			_, err := ra.StartRegression(ctx, running, 5)

			So(err, ShouldNotBeNil)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FinishRegression records the outcome only for the run still running\n", func() {
			finished := *running
			finished.Status, finished.Attempt, finished.FinishTime = interfaces.REGRESSION_STATUS_PASSED, 2, 30
			finished.Results = []interfaces.RegressionResult{{QuestionID: "q1", Outcome: "passed", Answer: "S1."}}
			finish := regexp.QuoteMeta("UPDATE t_bkn_project_regression SET f_status = ?, f_attempt = ?, f_results = ?, " +
				"f_error = ?, f_finish_time = ? WHERE f_network_id = ? AND f_run_id = ? AND f_status = ?")
			smock.ExpectExec(finish).WithArgs("passed", 2, `[{"question_id":"q1","outcome":"passed","answer":"S1."}]`, "",
				int64(30), "supply_v1_1_0", "r2", "running").WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectExec(finish).WillReturnResult(sqlmock.NewResult(0, 0))

			ok, err := ra.FinishRegression(ctx, &finished)
			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)

			ok, err = ra.FinishRegression(ctx, &finished)
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
		})

		Convey("FailRunning fails every running regression\n", func() {
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_regression SET f_status = ?, f_error = ?, f_finish_time = ? WHERE f_status = ?")).
				WithArgs("failed", "interrupted", int64(40), "running").WillReturnResult(sqlmock.NewResult(0, 3))

			n, err := ra.FailRunning(ctx, "interrupted", 40)

			So(err, ShouldBeNil)
			So(n, ShouldEqual, 3)
		})
	})
}
