// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_Understand(t *testing.T) {
	Convey("Test understanding the tables of a project's data catalogs\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		var ran []func()
		svc := &draftService{setting: testSetting, va: va, ps: ps, run: func(fn func()) { ran = append(ran, fn) }}
		ctx := context.Background()
		project := func(language string, catalogs ...string) *interfaces.ProjectEntry {
			return &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller,
				CatalogIDs: catalogs, NamingLanguage: language}}
		}
		tables := []*interfaces.VegaTable{{ID: "r_new", Name: "orders"}, {ID: "r_done", Name: "parts"},
			{ID: "r_running", Name: "lines"}, {ID: "r_failed", Name: "bills"}, {ID: "r_queued", Name: "plans"},
			{ID: "r_previewed", Name: "notes"}, {ID: "r_unsure", Name: "codes"}}
		// Newest first, as vega-backend lists them: r_done was understood after an earlier failure.
		tasks := []*interfaces.VegaSemanticTask{
			// A preview, run by hand, wrote nothing; a result below the confidence threshold neither.
			{ID: "s7", ResourceID: "r_previewed", Status: "completed", ApplyMode: interfaces.VEGA_SEMANTIC_APPLY_DRY_RUN, CreateTime: 7},
			{ID: "s6", ResourceID: "r_unsure", Status: "completed", ApplyMode: interfaces.VEGA_SEMANTIC_APPLY_FILL_EMPTY, CreateTime: 6},
			{ID: "s5", ResourceID: "r_queued", Status: "pending", CreateTime: 5},
			{ID: "s4", ResourceID: "r_failed", Status: "failed", CreateTime: 4},
			{ID: "s3", ResourceID: "r_running", Status: "running", CreateTime: 3},
			{ID: "s2", ResourceID: "r_done", Status: "completed", Applied: true, CreateTime: 2, FinishTime: 9},
			{ID: "s1", ResourceID: "r_done", Status: "failed", CreateTime: 1},
		}

		Convey("Submitting understands, as the owner and in the naming language, the tables not understood yet\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project("en-US", "c1", "c2"), nil)
			va.EXPECT().ListSemanticTasks(gomock.Any(), caller, "c1").Return(tasks, true, nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(tables, nil)
			// Without task_manage on c2 nothing is submitted there.
			va.EXPECT().ListSemanticTasks(gomock.Any(), caller, "c2").Return(nil, false, nil)

			got, err := svc.Understand(ctx, token, "p1", interfaces.ProjectUnderstand{}, caller)

			So(err, ShouldBeNil)
			So(got.Catalogs, ShouldResemble, []interfaces.UnderstandSubmittedCatalog{
				{CatalogID: "c1", Permitted: true, Tables: 3}, {CatalogID: "c2"}})
			var created []string
			va.EXPECT().CreateSemanticTask(gomock.Any(), caller, gomock.Any(), "en-US", interfaces.VEGA_SEMANTIC_APPLY_FILL_EMPTY).
				DoAndReturn(func(_ context.Context, _ interfaces.AccountInfo, id string, _ string, _ string) (*interfaces.VegaSemanticTask, error) {
					created = append(created, id)
					if id == "r_new" {
						return nil, errors.New("vega is down")
					}
					return &interfaces.VegaSemanticTask{ID: "s9", ResourceID: id}, nil
				}).Times(3)
			So(ran, ShouldHaveLength, 1)
			ran[0]()
			// A table that failed, or was only previewed, is submitted again; one queued, running or
			// understood is not, nor one whose result was too unsure to write: asking again gives the same.
			So(created, ShouldResemble, []string{"r_new", "r_failed", "r_previewed"})
		})

		Convey("Submitting all understands every table again, overwriting when asked\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project("", "c1"), nil)
			va.EXPECT().ListSemanticTasks(gomock.Any(), caller, "c1").Return(tasks, true, nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(tables, nil)

			got, err := svc.Understand(ctx, token, "p1",
				interfaces.ProjectUnderstand{ApplyMode: interfaces.VEGA_SEMANTIC_APPLY_FORCE, All: true}, caller)

			So(err, ShouldBeNil)
			So(got.Catalogs[0].Tables, ShouldEqual, 7)
			// A project without a naming language names in Chinese.
			va.EXPECT().CreateSemanticTask(gomock.Any(), caller, gomock.Any(), "zh-CN", interfaces.VEGA_SEMANTIC_APPLY_FORCE).
				Return(&interfaces.VegaSemanticTask{}, nil).Times(7)
			ran[0]()
		})

		Convey("Submitting is refused before reading vega\n", func() {
			Convey("a write mode other than filling or overwriting\n", func() {
				for _, mode := range []string{interfaces.VEGA_SEMANTIC_APPLY_DRY_RUN, "replace"} {
					_, err := svc.Understand(ctx, token, "p1", interfaces.ProjectUnderstand{ApplyMode: mode}, caller)
					shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Understand_InvalidApplyMode)
				}
			})
			Convey("someone else than the owner\n", func() {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project("", "c1"), nil)
				_, err := svc.Understand(ctx, token, "p1", interfaces.ProjectUnderstand{},
					interfaces.AccountInfo{ID: "u2", Type: "user"})
				shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Understand_NotOwner)
			})
			Convey("an archived project\n", func() {
				archived := project("", "c1")
				archived.Archived = true
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil)
				_, err := svc.Understand(ctx, token, "p1", interfaces.ProjectUnderstand{}, caller)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
			})
			Convey("a project without catalogs\n", func() {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project(""), nil)
				_, err := svc.Understand(ctx, token, "p1", interfaces.ProjectUnderstand{}, caller)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Understand_NoCatalogs)
			})
			So(ran, ShouldBeEmpty)
		})

		Convey("A failure reading vega submits nothing\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project("", "c1"), nil)
			va.EXPECT().ListSemanticTasks(gomock.Any(), caller, "c1").Return(tasks, true, nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(nil, errors.New("vega is down"))

			_, err := svc.Understand(ctx, token, "p1", interfaces.ProjectUnderstand{}, caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
			So(ran, ShouldBeEmpty)
		})

		Convey("The progress is each table's newest task, read as whoever sees the project\n", func() {
			other := interfaces.AccountInfo{ID: "u2", Type: "user"}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project("", "c1", "c2"), nil)
			va.EXPECT().ListSemanticTasks(gomock.Any(), other, "c1").Return(tasks, true, nil)
			va.EXPECT().ListTables(gomock.Any(), other, "c1").Return(tables, nil)
			va.EXPECT().ListSemanticTasks(gomock.Any(), other, "c2").Return(nil, false, nil)

			got, err := svc.GetUnderstanding(ctx, token, "p1", other)

			So(err, ShouldBeNil)
			So(got.Catalogs, ShouldHaveLength, 2)
			c1 := got.Catalogs[0]
			So(c1.Visible, ShouldBeTrue)
			So(c1.Tables, ShouldResemble, []interfaces.UnderstandingTable{
				{ResourceID: "r_new", Name: "orders", Status: interfaces.UNDERSTAND_NOT_STARTED},
				{ResourceID: "r_done", Name: "parts", Status: "completed", Applied: true, FinishTime: 9},
				{ResourceID: "r_running", Name: "lines", Status: "running"},
				{ResourceID: "r_failed", Name: "bills", Status: "failed"},
				{ResourceID: "r_queued", Name: "plans", Status: "pending"},
				{ResourceID: "r_previewed", Name: "notes", Status: "completed"},
				{ResourceID: "r_unsure", Name: "codes", Status: "completed"},
			})
			So(c1.Counts, ShouldResemble, map[string]int{
				interfaces.UNDERSTAND_NOT_STARTED: 1, "completed": 3, "running": 1, "failed": 1, "pending": 1})
			// Without task_manage the progress is unknown, not "nothing started".
			So(got.Catalogs[1], ShouldResemble, interfaces.UnderstandingCatalog{CatalogID: "c2", Counts: map[string]int{}})
		})

		Convey("A failure reading the progress is an internal error\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project("", "c1"), nil)
			va.EXPECT().ListSemanticTasks(gomock.Any(), caller, "c1").Return(nil, false, errors.New("vega is down"))

			_, err := svc.GetUnderstanding(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
		})
	})
}
