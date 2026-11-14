// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package logics

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_ProjectCatalogs(t *testing.T) {
	Convey("Test ProjectCatalogs\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		bba := pmock.NewMockBknBackendAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		pc := &ProjectCatalogs{BBA: bba, VA: va, DA: da}
		ctx := context.Background()
		caller := interfaces.AccountInfo{ID: "u1", Type: "user"}
		resource := func(id string) *interfaces.KNDataSource {
			return &interfaces.KNDataSource{Type: interfaces.KN_DATA_SOURCE_RESOURCE, ID: id}
		}
		entry := func(stored ...string) *interfaces.ProjectEntry {
			return &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", CatalogIDs: stored}}
		}

		Convey("Resolving hands back the network's object types it read, and TableReaders names them by table\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return([]*interfaces.KNObjectTypeSource{
				{ID: "purchase_order", Name: "Purchase order", DataSource: resource("r-po")},
				{ID: "po_view", DataSource: resource("r-po")},
				{ID: interfaces.SYSTEM_OBJECT_TYPE_DOMAIN_TERMS, Name: "Terms", DataSource: resource("r-term")},
				{ID: "note", Name: "Note"},
				{ID: "rate", Name: "Rate", DataSource: &interfaces.KNDataSource{Type: "api", ID: "a1"}},
			}, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r-po"}).Return([]*interfaces.VegaTable{
				{ID: "r-po", CatalogID: "c-new"}}, nil)
			e := entry()

			read := pc.ResolveReading(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c-new"})
			// One without a name is named by its id.
			So(TableReaders(read), ShouldResemble, map[string][]string{"r-po": {"Purchase order", "po_view"}})
		})

		Convey("Nothing read names no table\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return(nil, errors.New("down"))
			So(pc.ResolveReading(ctx, "t", caller, entry()), ShouldBeNil)
			var none *ProjectCatalogs
			So(none.ResolveReading(ctx, "t", caller, entry()), ShouldBeNil)
			So(TableReaders(nil), ShouldBeEmpty)
		})

		Convey("After the network's catalog is installed again, the project reads the new one and drops the old silently\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return([]*interfaces.KNObjectTypeSource{
				{ID: "purchase_order", DataSource: resource("r-po")},
				{ID: "supplier", DataSource: resource("r-supplier")},
			}, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r-po", "r-supplier"}).Return([]*interfaces.VegaTable{
				{ID: "r-po", CatalogID: "c-new"}, {ID: "r-supplier", CatalogID: "c-new"},
			}, nil)
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c-old", "c-mine"}).Return([]string{"c-old"}, nil)
			e := entry("c-old", "c-mine")

			pc.Resolve(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c-new", "c-mine"})
			So(e.NetworkCatalogIDs, ShouldResemble, []string{"c-new"})
		})

		Convey("A catalog both stored and read by the network is listed once, as the network's\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return([]*interfaces.KNObjectTypeSource{
				{ID: "orders", DataSource: resource("r1")},
				// Leave out a system object type, one reading no table and one reading something else.
				{ID: interfaces.SYSTEM_OBJECT_TYPE_DOMAIN_TERMS, DataSource: resource("r-term")},
				{ID: "note"},
				{ID: "rate", DataSource: &interfaces.KNDataSource{Type: "api", ID: "a1"}},
			}, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r1"}).
				Return([]*interfaces.VegaTable{{ID: "r1", CatalogID: "c1"}}, nil)
			e := entry("c1")

			pc.Resolve(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c1"})
			So(e.NetworkCatalogIDs, ShouldResemble, []string{"c1"})
		})

		Convey("On a network the project created, what it reads is its own draft's: the catalogs stay as stored\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return([]*interfaces.KNObjectTypeSource{
				{ID: "orders", DataSource: resource("r1")},
			}, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{Items: interfaces.DraftItems{
				ObjectTypes: []*interfaces.DraftObjectType{{ID: "orders"}},
			}}, true, nil)
			e := entry("c-gone")

			pc.Resolve(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c-gone"})
			So(e.NetworkCatalogIDs, ShouldBeNil)
		})

		Convey("While the project drafts again, what its confirmations wrote stays its own\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return([]*interfaces.KNObjectTypeSource{
				{ID: "orders", DataSource: resource("r1")},
				{ID: "lines", DataSource: resource("r2")},
			}, nil)
			// A draft drafting or failed holds only what the last confirmations wrote.
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{Status: interfaces.DRAFT_STATUS_DRAFTING,
				Items: interfaces.DraftItems{Confirmation: &interfaces.DraftConfirmation{
					Files: []string{"object_types/orders.bkn", "relation_types/has.bkn"}, Pending: []string{"object_types/lines.bkn"},
				}}}, true, nil)
			e := entry("c1")

			pc.Resolve(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c1"})
			So(e.NetworkCatalogIDs, ShouldBeNil)
		})

		Convey("An empty network leaves the catalogs as stored\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return(nil, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			e := entry("c1")

			pc.Resolve(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c1"})
		})

		Convey("A read that fails leaves the catalogs as stored\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return(nil, errors.New("down"))
			e := entry("c1")

			pc.Resolve(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c1"})
			So(e.NetworkCatalogIDs, ShouldBeNil)

			var none *ProjectCatalogs
			none.Resolve(ctx, "t", caller, e)
			So(e.CatalogIDs, ShouldResemble, []string{"c1"})
		})

		Convey("When vega cannot tell which added catalogs are gone, all are kept\n", func() {
			bba.EXPECT().ListObjectTypeSources(gomock.Any(), "t", "kn1").Return([]*interfaces.KNObjectTypeSource{
				{ID: "orders", DataSource: resource("r1")},
			}, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r1"}).
				Return([]*interfaces.VegaTable{{ID: "r1", CatalogID: "c-net"}}, nil)
			va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c-mine"}).Return(nil, errors.New("down"))
			e := entry("c-mine")

			pc.Resolve(ctx, "t", caller, e)

			So(e.CatalogIDs, ShouldResemble, []string{"c-net", "c-mine"})
		})
	})
}
