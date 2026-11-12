// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/merge"
)

const existingNetworkRoot = "---\ntype: knowledge_network\nid: kn1\nname: Supply\nbkn_spec: \"3.0\"\n---\n\n# Supply\n"

// existingNetwork is the branch of a network that already has sales orders, reading t_orders, in
// regions, reading a table outside the project's catalogs, written as confirming writes them.
// Its properties are named apart from their fields, as a network modeled by hand names them.
func existingNetwork() map[string][]byte {
	items := &interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			{ID: "supply_sales_order", Name: "销售订单", Aliases: []string{"订单"}, Description: "Orders placed", Adopted: true,
				Table: interfaces.DraftTable{ResourceID: "t_orders", Name: "orders"}, PrimaryKeys: []string{"order_no"},
				DisplayKey: "order_no", Properties: []*interfaces.DraftProperty{
					{Name: "order_no", DisplayName: "订单号", Type: "string", MappedField: "order_id"},
					{Name: "buyer", DisplayName: "客户编号", Type: "string", MappedField: "customer_id"},
					{Name: "region", DisplayName: "区域", Type: "string", MappedField: "region_code"},
					{Name: "placed_at", DisplayName: "下单时间", Type: "datetime", MappedField: "placed_at"},
				}},
			{ID: "supply_region", Name: "区域", Adopted: true,
				Table: interfaces.DraftTable{ResourceID: "t_regions", Name: "regions"}, PrimaryKeys: []string{"code"},
				DisplayKey: "code", Properties: []*interfaces.DraftProperty{
					{Name: "code", DisplayName: "区域编码", Type: "string", MappedField: "code"},
				}},
		},
		RelationTypes: []*interfaces.DraftRelationType{
			{ID: "supply_order_region", Name: "订单区域", Source: "supply_sales_order", Target: "supply_region", Adopted: true,
				Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
				Mapping:     []interfaces.DraftFieldMapping{{SourceProperty: "region", TargetProperty: "code"}}},
		},
	}
	files, err := Compile(items, nil)
	So(err, ShouldBeNil)
	files["network.bkn"] = []byte(existingNetworkRoot)
	return files
}

func Test_ExistingItemsOf(t *testing.T) {
	Convey("Test reading what the network already has as draft items\n", t, func() {
		items := existingItemsOf(existingNetwork(), nil)

		Convey("its object types are adopted, existing and wait for no one, with their ids, tables and properties\n", func() {
			So(items.ObjectTypes, ShouldHaveLength, 2)
			order := findObjectType(&items, "supply_sales_order")
			So(order, ShouldNotBeNil)
			So(order.Existing, ShouldBeTrue)
			So(order.Adopted, ShouldBeTrue)
			So(order.NeedsIdentity, ShouldBeFalse)
			So(order.Name, ShouldEqual, "销售订单")
			So(order.Aliases, ShouldResemble, []string{"订单"})
			So(order.Description, ShouldEqual, "Orders placed")
			So(order.Table.ResourceID, ShouldEqual, "t_orders")
			So(order.Table.Name, ShouldEqual, "orders")
			So(order.PrimaryKeys, ShouldResemble, []string{"order_no"})
			So(order.DisplayKey, ShouldEqual, "order_no")
			So(order.Properties, ShouldHaveLength, 4)
			So(findProperty(order, "buyer").MappedField, ShouldEqual, "customer_id")
			So(findProperty(order, "buyer").DisplayName, ShouldEqual, "客户编号")
		})

		Convey("its relation types keep their ends and mapping\n", func() {
			So(items.RelationTypes, ShouldHaveLength, 1)
			rt := items.RelationTypes[0]
			So(rt.Existing, ShouldBeTrue)
			So(rt.Adopted, ShouldBeTrue)
			So(rt.Source, ShouldEqual, "supply_sales_order")
			So(rt.Target, ShouldEqual, "supply_region")
			So(rt.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "region", TargetProperty: "code"}})
		})

		Convey("a draft of only what the network has adopts nothing of its own to confirm\n", func() {
			So(anyObjectTypeAdopted(&items), ShouldBeFalse)
			So(pendingItems(&items), ShouldBeEmpty)
		})

		Convey("what the draft wrote is the draft's own, and a new network has nothing\n", func() {
			owned := existingItemsOf(existingNetwork(), []string{"object_types/supply_region.bkn"})
			So(owned.ObjectTypes, ShouldHaveLength, 1)
			empty := existingItemsOf(map[string][]byte{}, nil)
			So(empty.ObjectTypes, ShouldBeEmpty)
			So(empty.RelationTypes, ShouldBeEmpty)
		})
	})
}

func Test_GenerateDraftOnExistingNetwork(t *testing.T) {
	Convey("Test drafting a project on a network that already has object types\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		setting := testSetting
		setting.QueryTimeout = 60
		setting.Timeout = time.Minute
		svc := &draftService{setting: setting, da: da, va: va, ps: ps, gs: gs, ca: pmock.NewMockCheckAccess(ctrl),
			ira: &fakeIDRegistry{}, run: func(fn func()) { fn() }}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1",
			CatalogIDs: []string{"c1"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		va.EXPECT().MissingCatalogs(gomock.Any(), caller, []string{"c1"}).Return(nil, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		da.EXPECT().StartDraft(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(true, nil)
		network := existingNetwork()
		gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(network, nil)
		orders := table("t_orders", "c1", "orders", 100, []string{"order_id"}, col("order_id", "string", ""),
			col("customer_id", "string", ""), col("region_code", "string", ""), col("placed_at", "datetime", ""))
		orders.ForeignKeys = []interfaces.VegaForeignKey{fk("customer_id", "customers", "customer_id")}
		customers := table("t_customers", "c1", "customers", 100, []string{"customer_id"},
			col("customer_id", "string", ""), col("customer_name", "string", ""))
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{
			{ID: "t_orders", Name: "orders", CatalogID: "c1"}, {ID: "t_customers", Name: "customers", CatalogID: "c1"},
		}, nil)
		va.EXPECT().GetTables(gomock.Any(), caller, []string{"t_orders", "t_customers"}).
			Return([]*interfaces.VegaTable{orders, customers}, nil)
		va.EXPECT().Query(gomock.Any(), caller, gomock.Any(), 60).Return([]map[string]any{{"total": float64(100),
			"distinct_values": float64(100), "nulls": float64(0), "orphans": float64(0)}}, nil).AnyTimes()
		var finished *interfaces.Draft
		da.EXPECT().FinishDraft(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, d *interfaces.Draft) error { finished = d; return nil })

		_, err := svc.GenerateDraft(context.Background(), token, "p1", caller, false)

		So(err, ShouldBeNil)
		So(finished.Error, ShouldEqual, "")
		items := finished.Items

		Convey("the draft holds what the network has beside what the new tables drafted, and no table twice\n", func() {
			So(items.ObjectTypes, ShouldHaveLength, 3)
			order := findObjectType(&items, "supply_sales_order")
			So(order.Existing, ShouldBeTrue)
			So(order.Table.CatalogID, ShouldEqual, "c1")
			So(findObjectType(&items, "supply_region").Existing, ShouldBeTrue)
			customer := findObjectType(&items, "customers")
			So(customer, ShouldNotBeNil)
			So(customer.Existing, ShouldBeFalse)
			So(customer.AddedByCatalog, ShouldEqual, "")
			So(items.Inputs.ExistingTables, ShouldResemble, []string{"orders"})
			So(items.Inputs.Tables, ShouldHaveLength, 1)
			So(items.Inputs.Tables[0].ResourceID, ShouldEqual, "t_customers")
			So(findRelationType(&items, "supply_order_region").Existing, ShouldBeTrue)
		})

		Convey("a new object type relates to the network's by the existing object type's own property\n", func() {
			rt := relation(items, "supply_sales_order", "customers")
			So(rt, ShouldNotBeNil)
			So(rt.Existing, ShouldBeFalse)
			So(rt.Adopted, ShouldBeTrue)
			So(rt.Mapping, ShouldResemble, []interfaces.DraftFieldMapping{{SourceProperty: "buyer", TargetProperty: "customer_id"}})
			So(items.RelationTypes, ShouldHaveLength, 2)
		})

		Convey("confirming writes only what the draft adds, and the branch stays valid\n", func() {
			So(pendingItems(&items), ShouldBeEmpty)
			compiled, err := compileDraft(&items, nil)
			So(err, ShouldBeNil)
			So(compiled.Files, ShouldContainKey, "object_types/customers.bkn")
			So(compiled.Files, ShouldNotContainKey, "object_types/supply_sales_order.bkn")
			So(compiled.Files, ShouldNotContainKey, "object_types/supply_region.bkn")
			So(compiled.Files, ShouldNotContainKey, "relation_types/supply_order_region.bkn")
			So(compiled.Files, ShouldHaveLength, 2)
			for _, key := range compiled.IDKeys {
				So(key.SourceKey, ShouldNotStartWith, "t_orders")
			}

			merged := map[string][]byte{}
			for p, data := range network {
				merged[p] = data
			}
			for p, data := range compiled.Files {
				So(network, ShouldNotContainKey, p)
				merged[p] = data
			}
			fsys := bkn.NewMemoryFileSystem()
			fsys.AddFiles(merged)
			_, err = bkn.LoadNetworkWithFS(fsys, ".")
			So(err, ShouldBeNil)
			introduced, err := merge.IntroducedErrors(network, merged)
			So(err, ShouldBeNil)
			So(introduced, ShouldBeEmpty)
		})

		Convey("concepts show the network's items as in the model, found in the network\n", func() {
			concepts := conceptsOf(context.Background(), "p1", finished, nil, false)
			c := findConcept(concepts, itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, "supply_sales_order"))
			So(c, ShouldNotBeNil)
			So(c.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_OBJECT)
			So(c.State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			So(c.SuggestedBecause, ShouldEqual, interfaces.CONCEPT_BECAUSE_NETWORK)
			So(c.Sources[0].Kind, ShouldEqual, interfaces.CONCEPT_SOURCE_NETWORK)
			relationConcept := findConcept(concepts, itemKey(interfaces.DRAFT_KIND_RELATION_TYPES, "supply_order_region"))
			So(relationConcept.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_RELATION)
			So(relationConcept.State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			newConcept := findConcept(concepts, itemKey(interfaces.DRAFT_KIND_OBJECT_TYPES, "customers"))
			So(newConcept.Sources[0].Kind, ShouldEqual, interfaces.CONCEPT_SOURCE_TABLE)
		})

		Convey("what the network has is never edited in the draft\n", func() {
			refused := func(err error) {
				var httpErr *httperrors.HTTPError
				So(errors.As(err, &httpErr), ShouldBeTrue)
				So(httpErr.HTTPCode, ShouldEqual, http.StatusConflict)
				So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Draft_ExistingItem)
			}
			name, no := "订单", false
			_, err := applyEdit(context.Background(), &items, interfaces.DRAFT_KIND_OBJECT_TYPES, "supply_sales_order",
				interfaces.DraftItemEdit{Name: &name})
			refused(err)
			_, err = applyEdit(context.Background(), &items, interfaces.DRAFT_KIND_RELATION_TYPES, "supply_order_region",
				interfaces.DraftItemEdit{Adopted: &no})
			refused(err)
			So(findObjectType(&items, "supply_sales_order").Name, ShouldEqual, "销售订单")
			So(findRelationType(&items, "supply_order_region").Adopted, ShouldBeTrue)
			// Not adopting the new end leaves the relation to it out, never the network's.
			_, err = applyEdit(context.Background(), &items, interfaces.DRAFT_KIND_OBJECT_TYPES, "customers",
				interfaces.DraftItemEdit{Adopted: &no})
			So(err, ShouldBeNil)
			So(relation(items, "supply_sales_order", "customers").Adopted, ShouldBeFalse)
			So(findRelationType(&items, "supply_order_region").Adopted, ShouldBeTrue)
		})

		Convey("pending asks nothing of the network's items\n", func() {
			for _, item := range pendingOf(&interfaces.Project{ID: "p1"}, finished, nil).Items {
				So(item.ItemID, ShouldNotStartWith, "supply_")
			}
			So(undecidedItems(&items), ShouldBeEmpty)
		})
	})
}

func Test_ExistingItemRefusals(t *testing.T) {
	Convey("Test saving, deleting and asking about what the network already has\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
		items := existingItemsOf(existingNetwork(), nil)
		draft := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime, Items: items}
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
		refused := func(err error) {
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Draft_ExistingItem)
		}

		_, err := svc.SaveObjectType(ctx, token, "p1", "supply_sales_order", interfaces.DraftObjectTypeSave{
			UpdateTime: readTime, Name: "订单"}, caller)
		refused(err)
		_, err = svc.ChooseIdentity(ctx, token, "p1", "supply_sales_order",
			interfaces.DraftIdentity{UpdateTime: readTime, Properties: []string{"buyer"}}, caller)
		refused(err)
		_, err = svc.SaveRelationType(ctx, token, "p1", "supply_order_region", interfaces.DraftRelationTypeSave{
			UpdateTime: readTime, Name: "区域"}, caller)
		refused(err)
		_, err = svc.DeleteRelationType(ctx, token, "p1", "supply_order_region", readTime, caller)
		refused(err)
	})
}
