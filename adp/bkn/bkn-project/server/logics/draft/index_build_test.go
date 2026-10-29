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
