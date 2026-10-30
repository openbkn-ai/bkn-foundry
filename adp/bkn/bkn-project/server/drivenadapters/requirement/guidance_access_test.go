// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

var guidanceRowColumns = []string{"f_project_id", "f_question_id", "f_spec_version", "f_question", "f_status", "f_tests",
	"f_guidance", "f_ontology_signature", "f_out_of_scope", "f_no_tests_reason", "f_left_out_tests", "f_deleted_tests", "f_error", "f_run_id",
	"f_agent_task_id", "f_runner", "f_runner_type", "f_create_time", "f_update_time"}

func Test_QuestionGuidance(t *testing.T) {
	Convey("Test question guidance\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ra := &requirementAccess{db: db}
		ctx := context.Background()
		selectGuidance := regexp.QuoteMeta("SELECT f_project_id, f_question_id, f_spec_version, f_question, f_status, f_tests, f_guidance, f_ontology_signature, f_out_of_scope, f_no_tests_reason, f_left_out_tests, f_deleted_tests, f_error, f_run_id, f_agent_task_id, f_runner, f_runner_type, f_create_time, f_update_time FROM t_bkn_project_question_guidance")
		row := func(rows *sqlmock.Rows) *sqlmock.Rows {
			return rows.AddRow("p1", "q1", 2, "Which orders?", "ready",
				`[{"id":"t1","text":"Orders of 10001?","kind":"paraphrase","slots":[{"concept_id":"c2","property":"customer_id","value":"10001"}],"expect":"exists","edited":false,"check":{"outcome":"passed"}}]`,
				"Use the customer relation.", "sig", `["Orders of their parent company?"]`, "", 2, 1, "", "run1", "task1", "u1", "user", int64(5), int64(7))
		}

		Convey("GetGuidance reads a question's guidance, and false when it has none\n", func() {
			smock.ExpectQuery(selectGuidance+regexp.QuoteMeta(" WHERE f_project_id = ? AND f_question_id = ?")).
				WithArgs("p1", "q1").WillReturnRows(row(sqlmock.NewRows(guidanceRowColumns)))
			smock.ExpectQuery(selectGuidance).WithArgs("p1", "q2").WillReturnRows(sqlmock.NewRows(guidanceRowColumns))

			g, ok, err := ra.GetGuidance(ctx, "p1", "q1")
			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(g.SpecVersion, ShouldEqual, 2)
			So(g.Tests[0].Slots[0], ShouldResemble, interfaces.QuestionTestSlot{ConceptID: "c2", Property: "customer_id", Value: "10001"})
			So(g.Tests[0].Check.Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(g.OutOfScope, ShouldResemble, []string{"Orders of their parent company?"})
			So(g.LeftOutTests, ShouldEqual, 2)
			So(g.RunID, ShouldEqual, "run1")
			So(g.Runner, ShouldResemble, interfaces.AccountInfo{ID: "u1", Type: "user"})
			_, ok, err = ra.GetGuidance(ctx, "p1", "q2")
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("ListGuidance reads every question's guidance of the project\n", func() {
			smock.ExpectQuery(selectGuidance + regexp.QuoteMeta(" WHERE f_project_id = ? ORDER BY f_question_id")).
				WithArgs("p1").WillReturnRows(row(sqlmock.NewRows(guidanceRowColumns)))

			list, err := ra.ListGuidance(ctx, "p1")
			So(err, ShouldBeNil)
			So(list, ShouldHaveLength, 1)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		ensureRow := regexp.QuoteMeta("INSERT INTO t_bkn_project_question_guidance (f_project_id,f_question_id,f_question,f_tests,f_guidance,f_out_of_scope) VALUES (?,?,?,?,?,?) ON DUPLICATE KEY UPDATE f_project_id = f_project_id")
		lockRow := regexp.QuoteMeta("SELECT f_status, f_update_time FROM t_bkn_project_question_guidance WHERE f_project_id = ? AND f_question_id = ? FOR UPDATE")
		start := regexp.QuoteMeta("UPDATE t_bkn_project_question_guidance SET f_spec_version = ?, f_question = ?, f_status = ?, f_tests = ?, f_guidance = ?, f_ontology_signature = ?, f_out_of_scope = ?, f_no_tests_reason = ?, f_left_out_tests = ?, f_deleted_tests = ?, f_error = ?, f_run_id = ?, f_agent_task_id = ?, f_runner = ?, f_runner_type = ?, f_create_time = ?, f_update_time = ? WHERE f_project_id = ? AND f_question_id = ?")
		generating := &interfaces.QuestionGuidance{ProjectID: "p1", QuestionID: "q1", SpecVersion: 2, Question: "Which orders?",
			Status: interfaces.GUIDANCE_STATUS_GENERATING, RunID: "run1", Runner: interfaces.AccountInfo{ID: "u1", Type: "user"},
			CreateTime: 9, UpdateTime: 9}

		Convey("StartGenerating writes the guidance generating, unless it is generating and not stale\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WithArgs("p1", "q1", "", "", "", "").WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectQuery(lockRow).WithArgs("p1", "q1").WillReturnRows(sqlmock.NewRows([]string{"f_status", "f_update_time"}).AddRow("", int64(0)))
			smock.ExpectExec(start).WithArgs(2, "Which orders?", "generating", "[]", "", "", "[]", "", 0, 0, "", "run1", "", "u1", "user",
				int64(9), int64(9), "p1", "q1").WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows([]string{"f_status", "f_update_time"}).AddRow("generating", int64(8)))
			smock.ExpectRollback()
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows([]string{"f_status", "f_update_time"}).AddRow("generating", int64(3)))
			smock.ExpectExec(start).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ra.StartGenerating(ctx, generating, 5)
			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			started, err = ra.StartGenerating(ctx, generating, 5)
			So(err, ShouldBeNil)
			So(started, ShouldBeFalse)
			started, err = ra.StartGenerating(ctx, generating, 5)
			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartGenerating rolls back when the write fails\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows([]string{"f_status", "f_update_time"}).AddRow("ready", int64(3)))
			smock.ExpectExec(start).WillReturnError(errors.New("boom"))
			smock.ExpectRollback()

			started, err := ra.StartGenerating(ctx, generating, 5)
			So(err, ShouldNotBeNil)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("UpdateGuidance writes ready guidance nobody changed meanwhile\n", func() {
			ready := &interfaces.QuestionGuidance{ProjectID: "p1", QuestionID: "q1", DeletedTests: 1, UpdateTime: 11}
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_question_guidance SET f_tests = ?, f_deleted_tests = ?, f_update_time = ? WHERE f_project_id = ? AND f_question_id = ? AND f_status = ? AND f_update_time = ?")).
				WithArgs("[]", 1, int64(11), "p1", "q1", "ready", int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))

			ok, err := ra.UpdateGuidance(ctx, ready, 7)
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FinishGenerating records the outcome only for the run that is generating\n", func() {
			done := &interfaces.QuestionGuidance{ProjectID: "p1", QuestionID: "q1", Status: "ready", RunID: "run1",
				AgentTaskID: "task1", Guidance: "g", OntologySignature: "sig", OutOfScope: []string{"x"}, UpdateTime: 12,
				Tests: []*interfaces.QuestionTest{}, NoTestsReason: "Nothing drafted holds the amount.", LeftOutTests: 3}
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_question_guidance SET f_status = ?, f_tests = ?, f_guidance = ?, f_ontology_signature = ?, f_out_of_scope = ?, f_no_tests_reason = ?, f_left_out_tests = ?, f_error = ?, f_agent_task_id = ?, f_update_time = ? WHERE f_project_id = ? AND f_question_id = ? AND f_status = ? AND f_run_id = ?")).
				WithArgs("ready", "[]", "g", "sig", `["x"]`, "Nothing drafted holds the amount.", 3, "", "task1", int64(12), "p1", "q1", "generating", "run1").
				WillReturnResult(sqlmock.NewResult(0, 1))

			ok, err := ra.FinishGenerating(ctx, done)
			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FailGenerating fails all guidance still generating\n", func() {
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_question_guidance SET f_status = ?, f_error = ?, f_update_time = ? WHERE f_status = ?")).
				WithArgs("failed", "interrupted", int64(9), "generating").WillReturnResult(sqlmock.NewResult(0, 3))

			n, err := ra.FailGenerating(ctx, "interrupted", 9)
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 3)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
