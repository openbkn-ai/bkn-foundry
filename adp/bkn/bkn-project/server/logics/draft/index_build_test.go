// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_BuildIndexes(t *testing.T) {
	Convey("Test starting the local index builds of a confirmed draft\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		va := pmock.NewMockVegaAccess(ctrl)
		iba := pmock.NewMockIndexBuildAccess(ctrl)
		svc := &draftService{setting: testSetting, va: va, iba: iba}
		ctx := context.Background()
		objectType := func(id string) *interfaces.DraftObjectType {
			return &interfaces.DraftObjectType{ID: id, Name: id, Adopted: true,
				Table: interfaces.DraftTable{ResourceID: "r_" + id, Name: id, CatalogID: "c1"}}
		}
		names := []string{"orders", "parts", "lines", "ready", "no_key", "taken", "broken"}
		items := &interfaces.DraftItems{}
		var ids []string
		for _, name := range names {
			items.ObjectTypes = append(items.ObjectTypes, objectType(name))
			ids = append(ids, "r_"+name)
		}
		configured := interfaces.VegaTableIndex{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"updated_at"},
			LocalStatus: interfaces.VEGA_LOCAL_STATUS_UNAVAILABLE}
		stale, ready := configured, configured
		stale.LocalStatus, stale.IndexName = interfaces.VEGA_LOCAL_STATUS_STALE, "idx"
		ready.LocalStatus, ready.IndexName = interfaces.VEGA_LOCAL_STATUS_AVAILABLE, "idx"
		refusal := func(code int, errorCode string) error {
			return &interfaces.VegaError{HTTPCode: code, BaseError: httperrors.BaseError{ErrorCode: errorCode}}
		}

		Convey("Tables that need a build get one as the runner, and every start is recorded\n", func() {
			va.EXPECT().GetTables(gomock.Any(), caller, ids).Return([]*interfaces.VegaTable{
				{ID: "r_orders", Index: configured}, {ID: "r_parts", Index: stale}, {ID: "r_lines", Index: configured},
				{ID: "r_ready", Index: ready}, {ID: "r_no_key", Index: interfaces.VegaTableIndex{IncrementalFields: []string{"u"}}},
				{ID: "r_taken", Index: configured}, {ID: "r_broken", Index: configured},
			}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c1").
				Return([]*interfaces.VegaBuildTask{{ID: "t1", ResourceID: "r_lines", Status: "running"}}, true, nil)
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, "r_orders").Return("t9", nil)
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, "r_parts").Return("", refusal(http.StatusForbidden, httperrors.PublicError_Forbidden))
			// A task started between reading and creating is taken as this confirmation's own.
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, "r_taken").
				Return("", refusal(http.StatusBadRequest, "VegaBackend.BuildTask.Exist"))
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, "r_broken").Return("", errors.New("vega is down"))
			var recorded []*interfaces.IndexBuild
			iba.EXPECT().RecordBuilds(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, builds []*interfaces.IndexBuild) error {
					recorded = builds
					return nil
				})

			err := svc.buildIndexes(ctx, "p1", "c0ffee", items, caller)

			So(err, ShouldBeNil)
			outcomes := map[string][3]string{}
			for _, b := range recorded {
				So(b.ProjectID, ShouldEqual, "p1")
				So(b.Commit, ShouldEqual, "c0ffee")
				So(b.Runner, ShouldResemble, caller)
				So(b.CreateTime, ShouldBeGreaterThan, 0)
				So(b.ResourceID, ShouldEqual, "r_"+b.ObjectTypeID)
				outcomes[b.ObjectTypeID] = [3]string{b.Outcome, b.TaskID, b.Reason}
			}
			So(outcomes, ShouldResemble, map[string][3]string{
				"orders": {interfaces.INDEX_BUILD_CREATED, "t9", ""},
				"parts":  {interfaces.INDEX_BUILD_NO_PERMISSION, "", ""},
				"lines":  {interfaces.INDEX_BUILD_EXISTING, "t1", ""},
				"taken":  {interfaces.INDEX_BUILD_EXISTING, "", ""},
				"broken": {interfaces.INDEX_BUILD_FAILED, "", "vega is down"},
			})
		})

		Convey("Only vega-backend's refusal of a second task reads as a build under way\n", func() {
			outcome, reason := buildOutcome(refusal(http.StatusBadRequest, "VegaBackend.BuildTask.InvalidMode"))

			So(outcome, ShouldEqual, interfaces.INDEX_BUILD_FAILED)
			So(reason, ShouldContainSubstring, "VegaBackend.BuildTask.InvalidMode")
		})

		Convey("Only a refused permission reads as no permission; another 403 keeps its reason\n", func() {
			outcome, reason := buildOutcome(refusal(http.StatusForbidden, "VegaBackend.Connector.EntitlementDenied"))

			So(outcome, ShouldEqual, interfaces.INDEX_BUILD_FAILED)
			So(reason, ShouldContainSubstring, "VegaBackend.Connector.EntitlementDenied")
		})

		Convey("Nothing is recorded when no table needs a build\n", func() {
			va.EXPECT().GetTables(gomock.Any(), caller, ids).Return([]*interfaces.VegaTable{{ID: "r_ready", Index: ready}}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c1").Return(nil, true, nil)

			So(svc.buildIndexes(ctx, "p1", "c0ffee", items, caller), ShouldBeNil)
		})

		Convey("A failure reading the tables starts nothing\n", func() {
			va.EXPECT().GetTables(gomock.Any(), caller, ids).Return(nil, errors.New("vega is down"))

			So(svc.buildIndexes(ctx, "p1", "c0ffee", items, caller), ShouldNotBeNil)
		})

		Convey("A failure recording the builds is returned\n", func() {
			va.EXPECT().GetTables(gomock.Any(), caller, ids).Return([]*interfaces.VegaTable{{ID: "r_orders", Index: configured}}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, "c1").Return(nil, true, nil)
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, "r_orders").Return("t9", nil)
			iba.EXPECT().RecordBuilds(gomock.Any(), gomock.Any()).Return(errors.New("db is down"))

			So(svc.buildIndexes(ctx, "p1", "c0ffee", items, caller), ShouldNotBeNil)
		})
	})
}

func Test_BuildIndex(t *testing.T) {
	Convey("Test the owner's retry of the confirmation's local index builds\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		iba := pmock.NewMockIndexBuildAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, va: va, iba: iba}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		confirmed := func() *interfaces.Draft {
			d := confirmableDraft()
			d.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: readTime, Commit: "c1"}
			return d
		}
		var adopted *interfaces.DraftObjectType
		for _, ot := range confirmed().Items.ObjectTypes {
			if ot.Adopted && adopted == nil {
				adopted = ot
			}
		}
		configured := interfaces.VegaTableIndex{PrimaryKeyFields: []string{"id"}, IncrementalFields: []string{"u"}}

		Convey("The owner builds the confirmed tables again and gets the readiness with the builds\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmed(), true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).
				Return([]*interfaces.VegaTable{{ID: adopted.Table.ResourceID, Index: configured}}, nil).Times(2)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, gomock.Any()).Return(nil, true, nil).AnyTimes()
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, adopted.Table.ResourceID).Return("t9", nil)
			var recorded []*interfaces.IndexBuild
			iba.EXPECT().RecordBuilds(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, builds []*interfaces.IndexBuild) error {
					recorded = builds
					return nil
				})
			iba.EXPECT().ListBuilds(gomock.Any(), "p1", "c1").DoAndReturn(
				func(context.Context, string, string) ([]*interfaces.IndexBuild, error) { return recorded, nil })

			index, err := svc.BuildIndex(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(recorded, ShouldHaveLength, 1)
			So(recorded[0].Commit, ShouldEqual, "c1")
			var built *interfaces.ProjectIndexItem
			for i := range index.ObjectTypes {
				if index.ObjectTypes[i].ID == adopted.ID {
					built = &index.ObjectTypes[i]
				}
			}
			So(built.Build.Outcome, ShouldEqual, interfaces.INDEX_BUILD_CREATED)
			So(built.Build.TaskID, ShouldEqual, "t9")
		})

		Convey("Only the owner may retry\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)

			_, err := svc.BuildIndex(ctx, token, "p1", interfaces.AccountInfo{ID: "u2", Type: "user"})

			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Index_NotOwner)
		})

		Convey("An archived project is refused\n", func() {
			archived := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, Archived: true}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(archived, nil)

			_, err := svc.BuildIndex(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_ProjectArchived)
		})

		Convey("Nothing confirmed has nothing to build\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).Times(2)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			_, err := svc.BuildIndex(ctx, token, "p1", caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Index_NothingConfirmed)

			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmableDraft(), true, nil)
			_, err = svc.BuildIndex(ctx, token, "p1", caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Index_NothingConfirmed)

			// A confirmation claimed and refused before its commit confirmed nothing.
			claimed := confirmableDraft()
			claimed.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: readTime, Pending: []string{"a.bkn"}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(claimed, true, nil)
			_, err = svc.BuildIndex(ctx, token, "p1", caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Index_NothingConfirmed)
		})

		Convey("A draft changed since its confirmation is confirmed again first\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).Times(2)
			edited := confirmed()
			edited.UpdateTime = readTime + 1
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(edited, true, nil)
			_, err := svc.BuildIndex(ctx, token, "p1", caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Index_ConfirmAgain)

			drafting := confirmed()
			drafting.Status = interfaces.DRAFT_STATUS_DRAFTING
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(drafting, true, nil)
			_, err = svc.BuildIndex(ctx, token, "p1", caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Index_ConfirmAgain)
		})

		Convey("A build that cannot be recorded is an internal error\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmed(), true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).
				Return([]*interfaces.VegaTable{{ID: adopted.Table.ResourceID, Index: configured}}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, gomock.Any()).Return(nil, true, nil).AnyTimes()
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, adopted.Table.ResourceID).Return("t9", nil)
			iba.EXPECT().RecordBuilds(gomock.Any(), gomock.Any()).Return(errors.New("db is down"))

			_, err := svc.BuildIndex(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
		})

		Convey("A failure reading the readiness after the build is an internal error\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmed(), true, nil)
			first := va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).
				Return([]*interfaces.VegaTable{{ID: adopted.Table.ResourceID, Index: configured}}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), caller, gomock.Any()).Return(nil, true, nil).AnyTimes()
			va.EXPECT().CreateBuildTask(gomock.Any(), caller, adopted.Table.ResourceID).Return("t9", nil)
			iba.EXPECT().RecordBuilds(gomock.Any(), gomock.Any()).Return(nil)
			va.EXPECT().GetTables(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("vega is down")).After(first)

			_, err := svc.BuildIndex(ctx, token, "p1", caller)

			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Draft_InternalError)
		})
	})
}
