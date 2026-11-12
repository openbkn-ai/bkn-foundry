// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_GetExistingIndex(t *testing.T) {
	Convey("Test the index readiness of the network's existing object types\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, gs: gs, va: va}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)

		Convey("a new network has none, and nothing is read from vega-backend\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{}, nil)

			got, err := svc.GetExistingIndex(ctx, token, "p1", caller)
			So(err, ShouldBeNil)
			So(got.ObjectTypes, ShouldBeEmpty)
			So(got.Counts, ShouldBeEmpty)
			So(got.TasksVisible, ShouldBeTrue)
		})

		Convey("each existing table is listed with its state; the draft's own types are not\n", func() {
			draft := &interfaces.Draft{ProjectID: "p1", Items: interfaces.DraftItems{
				Confirmation: &interfaces.DraftConfirmation{Files: []string{"object_types/customers.bkn"}}}}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
				"object_types/regions.bkn":   []byte(publishedRegions),
				"object_types/customers.bkn": []byte(publishedCustomers),
			}, nil)
			va.EXPECT().GetTables(gomock.Any(), gomock.Any(), []string{"t_regions"}).Return([]*interfaces.VegaTable{{
				ID: "t_regions", CatalogID: "c1", Name: "regions",
				Index: interfaces.VegaTableIndex{LocalStatus: interfaces.VEGA_LOCAL_STATUS_AVAILABLE, IndexName: "idx"},
			}}, nil)
			va.EXPECT().ListActiveBuildTasks(gomock.Any(), gomock.Any(), "c1").Return(nil, true, nil)
			va.EXPECT().ListLastBuilds(gomock.Any(), gomock.Any(), "c1").Return(map[string]int64{"t_regions": 42}, true, nil)

			got, err := svc.GetExistingIndex(ctx, token, "p1", caller)
			So(err, ShouldBeNil)
			So(got.ObjectTypes, ShouldHaveLength, 1)
			So(got.ObjectTypes[0].ID, ShouldEqual, "regions")
			So(got.ObjectTypes[0].Table, ShouldResemble, interfaces.DraftTable{ResourceID: "t_regions", Name: "regions", CatalogID: "c1"})
			So(got.ObjectTypes[0].State, ShouldEqual, interfaces.INDEX_STATE_READY)
			So(got.ObjectTypes[0].BuiltTime, ShouldEqual, 42)
			So(got.Counts, ShouldResemble, map[string]int{interfaces.INDEX_STATE_READY: 1})
		})

		Convey("a refused read of the tables leaves them unreadable rather than failing\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
				"object_types/regions.bkn": []byte(publishedRegions),
			}, nil)
			va.EXPECT().GetTables(gomock.Any(), gomock.Any(), []string{"t_regions"}).Return(nil, errors.New("403"))

			got, err := svc.GetExistingIndex(ctx, token, "p1", caller)
			So(err, ShouldBeNil)
			So(got.ObjectTypes, ShouldHaveLength, 1)
			So(got.ObjectTypes[0].State, ShouldEqual, interfaces.INDEX_STATE_UNREADABLE)
		})

		Convey("a branch that cannot be read fails\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(nil, errors.New("boom"))

			_, err := svc.GetExistingIndex(ctx, token, "p1", caller)
			So(err, ShouldNotBeNil)
		})
	})
}
