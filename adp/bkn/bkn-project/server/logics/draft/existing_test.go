// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

const existingSupplies = "---\ntype: relation_type\nid: customers_regions\nname: Customer region\n---\n\n" +
	"## RelationType: Customer region\n\n### Endpoint\n\n| Source | Target | Type |\n|--------|--------|------|\n" +
	"| customers | regions | direct |\n\n### Mapping Rules\n\n| Source Property | Target Property |\n" +
	"|-----------------|-----------------|\n| region | code |\n"

func Test_ExistingOf(t *testing.T) {
	Convey("Test reading what the network already has from the branch\n", t, func() {
		files := map[string][]byte{
			"object_types/regions.bkn":             []byte(publishedRegions),
			"object_types/customers.bkn":           []byte(publishedCustomers),
			"relation_types/customers_regions.bkn": []byte(existingSupplies),
			"object_types/broken.bkn":              []byte("not a definition"),
			"network.bkn":                          []byte("---\ntype: knowledge_network\n---\n"),
		}

		Convey("object and relation types are read in path order, with the table each reads\n", func() {
			got := existingOf(files, nil)
			So(got.ObjectTypes, ShouldHaveLength, 2)
			So(got.ObjectTypes[0].ID, ShouldEqual, "customers")
			So(got.ObjectTypes[0].TableID, ShouldEqual, "t_customers")
			So(got.ObjectTypes[1].ID, ShouldEqual, "regions")
			So(got.ObjectTypes[1].PropertyCount, ShouldEqual, 2)
			So(got.RelationTypes, ShouldHaveLength, 1)
			So(got.RelationTypes[0].Source, ShouldEqual, "customers")
			So(got.RelationTypes[0].Target, ShouldEqual, "regions")
		})

		Convey("what the draft wrote is the draft's, not the network's\n", func() {
			got := existingOf(files, []string{"object_types/customers.bkn"})
			So(got.ObjectTypes, ShouldHaveLength, 1)
			So(got.ObjectTypes[0].ID, ShouldEqual, "regions")
		})

		Convey("a new network has nothing\n", func() {
			got := existingOf(map[string][]byte{}, nil)
			So(got.ObjectTypes, ShouldBeEmpty)
			So(got.RelationTypes, ShouldBeEmpty)
		})
	})
}

func Test_WithoutExistingTables(t *testing.T) {
	Convey("Test leaving out the tables existing object types read\n", t, func() {
		tables := []*interfaces.VegaTable{{ID: "t_regions", Name: "regions"}, {ID: "t_orders", Name: "orders"}}
		unreadable := []*interfaces.DraftTable{{ResourceID: "t_customers", Name: "customers"}, {ResourceID: "t_logs", Name: "logs"}}

		Convey("a used table is neither drafted nor reported unreadable, and is named\n", func() {
			kept, keptUnreadable, left := withoutExistingTables(tables, unreadable,
				map[string]bool{"t_regions": true, "t_customers": true})
			So(kept, ShouldHaveLength, 1)
			So(kept[0].ID, ShouldEqual, "t_orders")
			So(keptUnreadable, ShouldHaveLength, 1)
			So(keptUnreadable[0].ResourceID, ShouldEqual, "t_logs")
			So(left, ShouldResemble, []string{"regions"})
		})

		Convey("without existing object types every table is drafted\n", func() {
			kept, keptUnreadable, left := withoutExistingTables(tables, unreadable, nil)
			So(kept, ShouldHaveLength, 2)
			So(keptUnreadable, ShouldHaveLength, 2)
			So(left, ShouldBeNil)
		})
	})
}

func Test_GetExisting(t *testing.T) {
	Convey("Test reading the existing definitions of a project\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, gs: gs}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
			"object_types/regions.bkn":   []byte(publishedRegions),
			"object_types/customers.bkn": []byte(publishedCustomers),
		}, nil)

		Convey("the files the draft's confirmation wrote are left out\n", func() {
			draft := &interfaces.Draft{ProjectID: "p1", Items: interfaces.DraftItems{
				Confirmation: &interfaces.DraftConfirmation{Files: []string{"object_types/customers.bkn"}}}}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			got, err := svc.GetExisting(context.Background(), token, "p1")
			So(err, ShouldBeNil)
			So(got.ObjectTypes, ShouldHaveLength, 1)
			So(got.ObjectTypes[0].ID, ShouldEqual, "regions")
		})

		Convey("before any draft everything on the branch is the network's\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)

			got, err := svc.GetExisting(context.Background(), token, "p1")
			So(err, ShouldBeNil)
			So(got.ObjectTypes, ShouldHaveLength, 2)
		})
	})
}

func Test_GenerateDraftLeavesOutExistingTables(t *testing.T) {
	Convey("Test drafting a project on a network that already models some tables\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		setting := testSetting
		setting.QueryTimeout = 60
		svc := &draftService{setting: setting, da: da, va: va, ps: ps, gs: gs, ca: pmock.NewMockCheckAccess(ctrl),
			ira: &fakeIDRegistry{}, run: func(fn func()) { fn() }}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1",
			CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
		va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c1"}).Return(nil, nil)
		da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(true, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{
			"object_types/regions.bkn": []byte(publishedRegions),
		}, nil)
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{
			{ID: "t_regions", Name: "regions", CatalogID: "c1"},
			{ID: "t_orders", Name: "orders", CatalogID: "c1"},
		}, nil)
		va.EXPECT().GetTables(gomock.Any(), caller, []string{"t_regions", "t_orders"}).Return([]*interfaces.VegaTable{
			table("t_regions", "c1", "regions", 2, []string{"code"}, col("code", "string", "")),
			table("t_orders", "c1", "orders", 2, []string{"id"}, col("id", "string", "")),
		}, nil)
		va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 60).
			Return([]map[string]any{{"total": float64(2), "distinct_values": float64(2), "nulls": float64(0)}}, nil).AnyTimes()
		var finished *interfaces.Draft
		da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, d *interfaces.Draft) error { finished = d; return nil })

		_, err := svc.GenerateDraft(context.Background(), token, "p1", caller, false)

		So(err, ShouldBeNil)
		So(finished.Status, ShouldEqual, interfaces.DRAFT_STATUS_READY)
		So(finished.Items.ObjectTypes, ShouldHaveLength, 1)
		So(finished.Items.ObjectTypes[0].Table.ResourceID, ShouldEqual, "t_orders")
		So(finished.Items.Inputs.ExistingTables, ShouldResemble, []string{"regions"})
	})
}
