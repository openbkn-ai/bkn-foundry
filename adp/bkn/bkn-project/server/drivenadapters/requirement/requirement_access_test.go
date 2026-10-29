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

const emptySpec = `{"scenarios":null,"concepts":null,"questions":null,"rules":null,"conflicts":null,"choices":null,"rejected":null}`

const resolvedSpec = `{"conflicts":[{"id":"k1","resolution":{"document_id":"d1","reason":"r","by":{"id":"u1","type":"user"},"time":1}}]}`

const chosenSpec = `{"choices":[{"id":"f1","decision":{"option_id":"o1","basis":"b","by":{"id":"u1","type":"user"},"time":1}}]}`

const deferredSpec = `{"conflicts":[{"id":"k1","deferred":true}]}`

var docColumns = []string{"f_id", "f_project_id", "f_name", "f_doc_type", "f_format", "f_sha256", "f_size",
	"f_uploader", "f_uploader_type", "f_create_time", "f_update_time"}

var reqColumns = []string{"f_project_id", "f_version", "f_status", "f_spec", "f_inputs", "f_error", "f_run_id",
	"f_agent_task_id", "f_drafter", "f_drafter_type", "f_create_time", "f_update_time"}

func Test_RequirementDocuments(t *testing.T) {
	Convey("Test requirement documents\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ra := &requirementAccess{db: db}
		ctx := context.Background()
		doc := &interfaces.RequirementDocument{
			ID: "d1", ProjectID: "p1", Name: "prd.md", DocType: "other", Format: "markdown",
			Content: "# A\nbody", Blocks: []interfaces.RequirementBlock{{ID: "b1", Heading: "A", Start: 0, End: 8}},
			SHA256: "h1", Size: 8, Uploader: interfaces.AccountInfo{ID: "u1", Type: "user"}, CreateTime: 5, UpdateTime: 5,
		}
		insert := regexp.QuoteMeta("INSERT INTO t_bkn_project_requirement_document (f_id,f_project_id,f_name,f_doc_type,f_format,f_content,f_blocks,f_sha256,f_size,f_uploader,f_uploader_type,f_create_time,f_update_time) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE f_id = f_id")
		byHash := regexp.QuoteMeta("SELECT f_id FROM t_bkn_project_requirement_document WHERE f_project_id = ? AND f_sha256 = ?")

		Convey("CreateDocument stores a new document with its blocks\n", func() {
			smock.ExpectExec(insert).
				WithArgs("d1", "p1", "prd.md", "other", "markdown", "# A\nbody",
					`[{"id":"b1","heading":"A","start":0,"end":8}]`, "h1", int64(8), "u1", "user", int64(5), int64(5)).
				WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectQuery(byHash).WithArgs("p1", "h1").WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("d1"))

			created, existing, err := ra.CreateDocument(ctx, doc)

			So(err, ShouldBeNil)
			So(created, ShouldBeTrue)
			So(existing, ShouldEqual, "d1")
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("CreateDocument reports the document that already has the same content\n", func() {
			smock.ExpectExec(insert).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(byHash).WithArgs("p1", "h1").WillReturnRows(sqlmock.NewRows([]string{"f_id"}).AddRow("d0"))

			created, existing, err := ra.CreateDocument(ctx, doc)

			So(err, ShouldBeNil)
			So(created, ShouldBeFalse)
			So(existing, ShouldEqual, "d0")
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("CreateDocument returns the write error\n", func() {
			smock.ExpectExec(insert).WillReturnError(errors.New("boom"))

			_, _, err := ra.CreateDocument(ctx, doc)

			So(err, ShouldNotBeNil)
		})

		Convey("ListDocuments reads the project's documents oldest first, without content\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_project_id, f_name, f_doc_type, f_format, f_sha256, f_size, f_uploader, f_uploader_type, f_create_time, f_update_time FROM t_bkn_project_requirement_document WHERE f_project_id = ? ORDER BY f_create_time, f_id")).
				WithArgs("p1").
				WillReturnRows(sqlmock.NewRows(docColumns).AddRow("d1", "p1", "prd.md", "prd", "markdown", "h1", int64(8), "u1", "user", int64(5), int64(6)))

			docs, err := ra.ListDocuments(ctx, "p1")

			So(err, ShouldBeNil)
			So(docs, ShouldHaveLength, 1)
			So(docs[0].DocType, ShouldEqual, "prd")
			So(docs[0].Content, ShouldEqual, "")
			So(docs[0].Uploader, ShouldResemble, interfaces.AccountInfo{ID: "u1", Type: "user"})
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("GetDocuments reads content and blocks too\n", func() {
			smock.ExpectQuery(regexp.QuoteMeta("SELECT f_id, f_project_id, f_name, f_doc_type, f_format, f_sha256, f_size, f_uploader, f_uploader_type, f_create_time, f_update_time, f_content, f_blocks FROM t_bkn_project_requirement_document WHERE f_project_id = ? ORDER BY f_create_time, f_id")).
				WithArgs("p1").
				WillReturnRows(sqlmock.NewRows(append(docColumns, "f_content", "f_blocks")).
					AddRow("d1", "p1", "prd.md", "prd", "markdown", "h1", int64(8), "u1", "user", int64(5), int64(6),
						"# A\nbody", `[{"id":"b1","heading":"A","start":0,"end":8}]`))

			docs, err := ra.GetDocuments(ctx, "p1")

			So(err, ShouldBeNil)
			So(docs[0].Content, ShouldEqual, "# A\nbody")
			So(docs[0].Blocks, ShouldResemble, []interfaces.RequirementBlock{{ID: "b1", Heading: "A", Start: 0, End: 8}})
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("GetDocument reads one document of the project, and false when it has none\n", func() {
			get := regexp.QuoteMeta("FROM t_bkn_project_requirement_document WHERE f_id = ? AND f_project_id = ?")
			smock.ExpectQuery(get).WithArgs("d1", "p1").WillReturnRows(sqlmock.NewRows(append(docColumns, "f_content", "f_blocks")).
				AddRow("d1", "p1", "prd.md", "prd", "markdown", "h1", int64(8), "u1", "user", int64(5), int64(6), "x", `[]`))
			smock.ExpectQuery(get).WithArgs("d2", "p1").WillReturnRows(sqlmock.NewRows(append(docColumns, "f_content", "f_blocks")))

			got, ok, err := ra.GetDocument(ctx, "p1", "d1")
			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(got.Content, ShouldEqual, "x")
			_, ok, err = ra.GetDocument(ctx, "p1", "d2")
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("SetDocumentType and DeleteDocument touch only the project's document\n", func() {
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_requirement_document SET f_doc_type = ?, f_update_time = ? WHERE f_id = ? AND f_project_id = ?")).
				WithArgs("policy", int64(9), "d1", "p1").WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectExec(regexp.QuoteMeta("DELETE FROM t_bkn_project_requirement_document WHERE f_id = ? AND f_project_id = ?")).
				WithArgs("d1", "p1").WillReturnResult(sqlmock.NewResult(0, 0))

			ok, err := ra.SetDocumentType(ctx, "p1", "d1", "policy", 9)
			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			ok, err = ra.DeleteDocument(ctx, "p1", "d1")
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}

func Test_RequirementSpecification(t *testing.T) {
	Convey("Test requirement specifications\n", t, func() {
		db, smock, err := sqlmock.New()
		So(err, ShouldBeNil)
		defer func() { _ = db.Close() }()
		ra := &requirementAccess{db: db}
		ctx := context.Background()
		drafting := &interfaces.Requirement{
			ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_DRAFTING, RunID: "run1",
			Drafter: interfaces.AccountInfo{ID: "u1", Type: "user"}, CreateTime: 5, UpdateTime: 5,
		}
		ensureRow := regexp.QuoteMeta("INSERT INTO t_bkn_project_requirement (f_project_id,f_spec,f_inputs) VALUES (?,?,?) ON DUPLICATE KEY UPDATE f_project_id = f_project_id")
		lockRow := regexp.QuoteMeta("SELECT f_status, f_update_time, f_spec FROM t_bkn_project_requirement WHERE f_project_id = ? FOR UPDATE")
		lockCols := []string{"f_status", "f_update_time", "f_spec"}
		start := regexp.QuoteMeta("UPDATE t_bkn_project_requirement SET f_version = ?, f_status = ?, f_spec = ?, f_inputs = ?, f_error = ?, f_run_id = ?, f_agent_task_id = ?, f_drafter = ?, f_drafter_type = ?, f_create_time = ?, f_update_time = ? WHERE f_project_id = ?")

		Convey("StartDrafting writes the first specification of a project into the row it makes sure of\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WithArgs("p1", "", "").WillReturnResult(sqlmock.NewResult(1, 1))
			smock.ExpectQuery(lockRow).WithArgs("p1").WillReturnRows(sqlmock.NewRows(lockCols).AddRow("", int64(0), ""))
			smock.ExpectExec(start).
				WithArgs(1, "drafting", emptySpec, "null", "", "run1", "", "u1", "user", int64(5), int64(5), "p1").
				WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ra.StartDrafting(ctx, drafting, 3, false)

			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDrafting leaves a project that is drafting alone, unless its drafting went stale\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("drafting", int64(4), emptySpec))
			smock.ExpectRollback()
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("drafting", int64(2), emptySpec))
			smock.ExpectExec(start).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ra.StartDrafting(ctx, drafting, 3, false)
			So(err, ShouldBeNil)
			So(started, ShouldBeFalse)
			started, err = ra.StartDrafting(ctx, drafting, 3, false)
			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDrafting keeps a specification with decisions unless they are discarded\n", func() {
			for _, spec := range []string{resolvedSpec, chosenSpec, deferredSpec} {
				smock.ExpectBegin()
				smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
				smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("ready", int64(2), spec))
				smock.ExpectRollback()

				started, err := ra.StartDrafting(ctx, drafting, 3, false)
				So(errors.Is(err, interfaces.ErrRequirementHasDecisions), ShouldBeTrue)
				So(started, ShouldBeFalse)
			}
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("ready", int64(2), resolvedSpec))
			smock.ExpectExec(start).WillReturnResult(sqlmock.NewResult(0, 1))
			smock.ExpectCommit()

			started, err := ra.StartDrafting(ctx, drafting, 3, true)
			So(err, ShouldBeNil)
			So(started, ShouldBeTrue)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("StartDrafting rolls back when the write fails\n", func() {
			smock.ExpectBegin()
			smock.ExpectExec(ensureRow).WillReturnResult(sqlmock.NewResult(0, 0))
			smock.ExpectQuery(lockRow).WillReturnRows(sqlmock.NewRows(lockCols).AddRow("ready", int64(2), emptySpec))
			smock.ExpectExec(start).WillReturnError(errors.New("boom"))
			smock.ExpectRollback()

			started, err := ra.StartDrafting(ctx, drafting, 3, false)
			So(err, ShouldNotBeNil)
			So(started, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("GetRequirement reads the specification, and false when there is none\n", func() {
			get := regexp.QuoteMeta("SELECT f_project_id, f_version, f_status, f_spec, f_inputs, f_error, f_run_id, f_agent_task_id, f_drafter, f_drafter_type, f_create_time, f_update_time FROM t_bkn_project_requirement WHERE f_project_id = ?")
			smock.ExpectQuery(get).WithArgs("p1").WillReturnRows(sqlmock.NewRows(reqColumns).
				AddRow("p1", 2, "ready", `{"questions":[{"id":"q1","text":"t","needs":[{"kind":"object_type","id":"ot1"}]}]}`,
					`{"rules_version":1,"documents":[{"id":"d1","sha256":"h1","doc_type":"prd"}],"catalog_ids":["c1"],"naming_language":"zh-CN"}`,
					"", "run1", "task1", "u1", "user", int64(5), int64(7)))
			smock.ExpectQuery(get).WithArgs("p2").WillReturnRows(sqlmock.NewRows(reqColumns))

			req, ok, err := ra.GetRequirement(ctx, "p1")
			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(req.Version, ShouldEqual, 2)
			So(req.RunID, ShouldEqual, "run1")
			So(req.AgentTaskID, ShouldEqual, "task1")
			So(req.Spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{{Kind: "object_type", ID: "ot1"}})
			So(req.Inputs.Documents[0].DocType, ShouldEqual, "prd")
			So(req.UpdateTime, ShouldEqual, 7)
			_, ok, err = ra.GetRequirement(ctx, "p2")
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("UpdateRequirement writes only a ready specification nobody changed meanwhile\n", func() {
			ready := &interfaces.Requirement{ProjectID: "p1", UpdateTime: 9}
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_requirement SET f_spec = ?, f_update_time = ? WHERE f_project_id = ? AND f_status = ? AND f_update_time = ?")).
				WithArgs(emptySpec, int64(9), "p1", "ready", int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))

			ok, err := ra.UpdateRequirement(ctx, ready, 7)
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FinishDrafting records the outcome only for the run that is drafting\n", func() {
			done := &interfaces.Requirement{ProjectID: "p1", Status: "ready", RunID: "run1", AgentTaskID: "task1",
				Inputs: &interfaces.RequirementInputs{RulesVersion: 1}, UpdateTime: 9}
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_requirement SET f_status = ?, f_spec = ?, f_inputs = ?, f_error = ?, f_agent_task_id = ?, f_update_time = ? WHERE f_project_id = ? AND f_status = ? AND f_run_id = ?")).
				WithArgs("ready", emptySpec, `{"rules_version":1,"documents":null,"catalog_ids":null,"naming_language":""}`, "", "task1", int64(9), "p1", "drafting", "run1").
				WillReturnResult(sqlmock.NewResult(0, 0))

			ok, err := ra.FinishDrafting(ctx, done)
			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})

		Convey("FailDrafting fails every specification still drafting\n", func() {
			smock.ExpectExec(regexp.QuoteMeta("UPDATE t_bkn_project_requirement SET f_status = ?, f_error = ?, f_update_time = ? WHERE f_status = ?")).
				WithArgs("failed", "interrupted", int64(9), "drafting").WillReturnResult(sqlmock.NewResult(0, 2))

			n, err := ra.FailDrafting(ctx, "interrupted", 9)
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 2)
			So(smock.ExpectationsWereMet(), ShouldBeNil)
		})
	})
}
