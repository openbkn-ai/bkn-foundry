// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package diagnosis

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

func Test_DiagnosisAccess(t *testing.T) {
	Convey("Test diagnosisAccess\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		da := &diagnosisAccess{db: db}
		ctx := context.Background()
		running := &interfaces.Diagnosis{ProjectID: "p1", State: interfaces.DIAGNOSIS_STATE_RUNNING, RunID: "r2",
			Runner: interfaces.AccountInfo{ID: "u1", Type: "user"}, DraftUpdateTime: 7, StartTime: 10}
		ensureRow := regexp.QuoteMeta("INSERT INTO t_bkn_project_diagnosis (f_project_id,f_result,f_error) VALUES (?,?,?) " +
			"ON DUPLICATE KEY UPDATE f_project_id = f_project_id")
		lockRow := regexp.QuoteMeta("SELECT f_state, f_start_time FROM t_bkn_project_diagnosis WHERE f_project_id = ? FOR UPDATE")
		replace := regexp.QuoteMeta("UPDATE t_bkn_project_diagnosis SET f_run_id = ?, f_state = ?")

		Convey("StartDiagnosis replaces a finished diagnosis\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WithArgs("p1", "", "").WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WithArgs("p1").WillReturnRows(
				sqlmock.NewRows([]string{"f_state", "f_start_time"}).AddRow("ready", int64(3)))
			smock.ExpectExec(replace).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := da.StartDiagnosis(ctx, running, 5)
			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDiagnosis leaves one running since staleBefore, and takes over an older one\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows([]string{"f_state", "f_start_time"}).AddRow("running", int64(6)))
			smock.ExpectRollback()
			started, err := da.StartDiagnosis(ctx, running, 5)
			So(err, ShouldBeNil)
			So(started, ShouldBeFalse)

			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows([]string{"f_state", "f_start_time"}).AddRow("running", int64(4)))
			smock.ExpectExec(replace).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()
			started, err = da.StartDiagnosis(ctx, running, 5)
			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SaveDiagnosis writes only while the run is the project's, and GetDiagnosis reads it back\n", func() {
			running.State = interfaces.DIAGNOSIS_STATE_READY
			running.Suggestions = []*interfaces.DiagnosisSuggestion{{ID: "s1", Kind: interfaces.DIAGNOSIS_KIND_DATA_QUALITY,
				Apply: []byte("null"), Status: interfaces.DIAGNOSIS_STATUS_OPEN}}
			running.Components = [][]string{{"a"}}
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_diagnosis SET f_state = ?, f_result = ?, f_error = ?, " +
				"f_draft_update_time = ?, f_finish_time = ? WHERE f_project_id = ? AND f_run_id = ?")).
				WillReturnResult(sqlmock.NewResult(0, 0))
			saved, err := da.SaveDiagnosis(ctx, running)
			So(err, ShouldBeNil)
			So(saved, ShouldBeFalse)

			result, _ := resultOf(running)
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_project_id, f_run_id")).WithArgs("p1").WillReturnRows(
				sqlmock.NewRows(diagnosisColumns).AddRow("p1", "r2", "ready", "u1", "user", result, "", int64(7), int64(10), int64(12)))
			got, exist, err := da.GetDiagnosis(ctx, "p1")
			So(err, ShouldBeNil)
			So(exist, ShouldBeTrue)
			So(got.Components, ShouldResemble, [][]string{{"a"}})
			So(got.Suggestions[0].ID, ShouldEqual, "s1")
			So(string(got.Suggestions[0].Apply), ShouldEqual, "null")
			So(got.DraftUpdateTime, ShouldEqual, 7)

			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_project_id, f_run_id")).WillReturnRows(sqlmock.NewRows(diagnosisColumns))
			_, exist, err = da.GetDiagnosis(ctx, "p2")
			So(err, ShouldBeNil)
			So(exist, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
