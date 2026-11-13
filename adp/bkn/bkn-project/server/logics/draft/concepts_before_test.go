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

// catalogTables are the tables of catalog c1 as vega lists them: orders, which semantic
// understanding named, customers, which it did not, and t_orders, which the network's sales
// orders read.
func catalogTables() []*interfaces.VegaTable {
	return []*interfaces.VegaTable{
		{ID: "r_orders", CatalogID: "c1", Name: "订单", Description: "客户下的订单。每行一笔订单",
			SourceSchema: "public", SourceName: "orders"},
		{ID: "r_customers", CatalogID: "c1", Name: "public.customers", SourceSchema: "public", SourceName: "customers"},
		{ID: "t_orders", CatalogID: "c1", Name: "public.sales", SourceSchema: "public", SourceName: "sales"},
	}
}

func Test_ConceptsBeforeDrafting(t *testing.T) {
	Convey("Test the concepts of a project before drafting\n", t, func() {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		gs := pmock.NewMockGitStore(ctrl)
		caller := interfaces.AccountInfo{ID: "u1", Type: "user"}
		svc := &draftService{setting: testSetting, da: da, va: va, ps: ps, rqa: rqa, gs: gs}
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Branch: "project/p1",
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).AnyTimes()
		va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(catalogTables(), nil).AnyTimes()
		// The catalog's semantic understanding, as the modeling page reads it: none ran unless a test says so.
		var tasks []*interfaces.VegaSemanticTask
		va.EXPECT().ListSemanticTasks(gomock.Any(), caller, "c1").DoAndReturn(
			func(context.Context, interfaces.VegaAccount, string) ([]*interfaces.VegaSemanticTask, bool, error) {
				return tasks, true, nil
			}).AnyTimes()
		req := readyRequirement()
		req.Spec.Concepts = []*interfaces.RequirementConcept{
			{ID: "k1", Term: "订单", Category: "object", Citations: []interfaces.RequirementCitation{{DocumentID: "d1"}}},
		}
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(req, true, nil).AnyTimes()

		Convey("On a new network, each catalog table is a candidate object before any draft\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{}, nil)

			got, err := svc.GetConcepts(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.DraftUpdateTime, ShouldEqual, 0)
			So(got.Concepts, ShouldHaveLength, 3)
			orders := conceptByKey(got, "table_concepts/r_orders")
			So(orders, ShouldNotBeNil)
			// Named as semantic understanding named the table, the table's own name an alias.
			So(orders.Name, ShouldEqual, "订单")
			So(orders.Description, ShouldEqual, "客户下的订单。每行一笔订单")
			So(orders.Aliases, ShouldResemble, []string{"orders"})
			So(orders.SuggestedBecause, ShouldEqual, interfaces.CONCEPT_BECAUSE_UNDERSTOOD)
			So(orders.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_OBJECT)
			So(orders.State, ShouldEqual, interfaces.CONCEPT_STATE_PENDING)
			So(orders.Item, ShouldBeNil)
			// The requirement concept of the same name is the table's concept, not one of its own.
			So(orders.Sources, ShouldHaveLength, 2)
			So(orders.Sources[0].Kind, ShouldEqual, interfaces.CONCEPT_SOURCE_TABLE)
			So(orders.Sources[1].Kind, ShouldEqual, interfaces.CONCEPT_SOURCE_REQUIREMENT)
			So(conceptByKey(got, "requirement_concepts/k1"), ShouldBeNil)
			// A table semantic understanding has not named keeps its name, and is counted.
			customers := conceptByKey(got, "table_concepts/r_customers")
			So(customers.Name, ShouldEqual, "customers")
			So(customers.SuggestedBecause, ShouldEqual, interfaces.CONCEPT_BECAUSE_TABLE)
			So(got.NotUnderstood, ShouldEqual, 2)
			So(got.Counts[interfaces.CONCEPT_STATE_PENDING], ShouldEqual, 3)
		})

		Convey("On an existing network, what it has is listed in the model, and the tables it reads are no candidates\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(existingNetwork(), nil)

			got, err := svc.GetConcepts(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			order := conceptByKey(got, "object_types/supply_sales_order")
			So(order, ShouldNotBeNil)
			So(order.State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			So(order.Sources[0].Kind, ShouldEqual, interfaces.CONCEPT_SOURCE_NETWORK)
			So(conceptByKey(got, "object_types/supply_region"), ShouldNotBeNil)
			So(conceptByKey(got, "relation_types/supply_order_region").State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			// t_orders is the sales orders' table: no candidate of its own.
			So(conceptByKey(got, "table_concepts/t_orders"), ShouldBeNil)
			So(conceptByKey(got, "table_concepts/r_orders"), ShouldNotBeNil)
			So(conceptByKey(got, "table_concepts/r_customers"), ShouldNotBeNil)
			So(got.Counts[interfaces.CONCEPT_STATE_IN_MODEL], ShouldEqual, 3)
			// Only the candidates count: t_orders is named as the network names its sales orders.
			So(got.NotUnderstood, ShouldEqual, 1)
		})

		Convey("A table whose understanding completed is not counted, as the modeling page reads it done\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{}, nil)
			// customers completed, writing nothing it was sure of; the newest task of sales failed.
			tasks = []*interfaces.VegaSemanticTask{
				{ResourceID: "r_customers", Status: semanticCompleted},
				{ResourceID: "t_orders", Status: "failed"},
				{ResourceID: "t_orders", Status: semanticCompleted},
			}

			got, err := svc.GetConcepts(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.NotUnderstood, ShouldEqual, 1)
		})

		Convey("While drafting runs, the candidates stay listed\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{ProjectID: "p1",
				Status: interfaces.DRAFT_STATUS_DRAFTING}, true, nil)
			gs.EXPECT().ReadTree(gomock.Any(), "kn1", "refs/heads/project/p1").Return(map[string][]byte{}, nil)

			got, err := svc.GetConcepts(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.Concepts, ShouldHaveLength, 3)
		})

		Convey("Once drafted, a table's concept is its item's\n", func() {
			draft := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
				Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "orders", Name: "orders",
					Adopted: true, Table: interfaces.DraftTable{ResourceID: "r_orders", Name: "订单"}}}}}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_orders"}).Return(catalogTables()[:1], nil)

			got, err := svc.GetConcepts(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.Concepts, ShouldHaveLength, 1)
			So(got.Concepts[0].Key, ShouldEqual, "object_types/orders")
			So(got.Concepts[0].Name, ShouldEqual, "订单")
		})

		Convey("Understanding that only described a table names it as drafting did, and does not count it\n", func() {
			// Filling in only what was empty, understanding described shipments and left its name.
			shipments := &interfaces.VegaTable{ID: "r_shipments", CatalogID: "c1", Name: "public.shipments",
				Description: "发货单。每行一次发货", SourceSchema: "public", SourceName: "shipments"}
			draft := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
				Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{ID: "shipments", Name: "发货单",
					Adopted: true, Table: interfaces.DraftTable{ResourceID: "r_shipments", Name: "public.shipments"}}}}}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			va.EXPECT().GetTables(gomock.Any(), caller, []string{"r_shipments"}).Return([]*interfaces.VegaTable{shipments}, nil)

			got, err := svc.GetConcepts(ctx, token, "p1", caller)

			So(err, ShouldBeNil)
			So(got.Concepts[0].Name, ShouldEqual, "发货单")
			So(got.Concepts[0].SuggestedBecause, ShouldEqual, interfaces.CONCEPT_BECAUSE_UNDERSTOOD)
			So(got.NotUnderstood, ShouldEqual, 0)

			// Before drafting the candidate is named so too.
			before := projectConcepts(ctx, "p1", nil, nil, false, nil,
				&beforeDrafting{tables: []*interfaces.VegaTable{shipments}, language: interfaces.NAMING_LANGUAGE_ZH_CN}, nil)
			So(before.Concepts[0].Name, ShouldEqual, "发货单")
			So(before.Concepts[0].Aliases, ShouldResemble, []string{"shipments"})
			So(before.NotUnderstood, ShouldEqual, 0)
		})
	})
}

func Test_ConceptsAfterConfirming(t *testing.T) {
	Convey("Test the concepts of a confirmed model\n", t, func() {
		ctx := context.Background()
		draft := candidateDraft()
		// Items: adopted by drafting, no one judged its concept; stores: a person adopted it on the model.
		draft.Items.Confirmation = &interfaces.DraftConfirmation{UpdateTime: readTime, Commit: "c0ffee",
			By: reviewer, Time: 10, Files: []string{"object_types/items.bkn", "object_types/stores.bkn",
				"relation_types/items_to_stores.bkn"}}

		Convey("What went into the model is in the model, confirmed by who confirmed it\n", func() {
			concepts := conceptsOf(ctx, "p1", draft, nil, false)
			items := conceptByKey(concepts, "object_types/items")
			So(items.State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			So(items.Judged, ShouldResemble, &interfaces.DraftReview{By: reviewer, Time: 10})
			So(conceptByKey(concepts, "relation_types/items_to_stores").State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			So(conceptByKey(concepts, "object_types/stores").State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			// Not written, the visits and the notes stay as they were.
			So(conceptByKey(concepts, "object_types/visits").State, ShouldEqual, interfaces.CONCEPT_STATE_PENDING)
			So(concepts.Counts[interfaces.CONCEPT_STATE_IN_MODEL], ShouldEqual, 3)
		})

		Convey("A judgment stands over the confirmation\n", func() {
			draft.Items.Decisions = append(draft.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS,
				ItemID: "object_types/items", Action: interfaces.DRAFT_DECISION_JUDGE, After: interfaces.CONCEPT_VERDICT_EVENT,
				By: reviewer, Time: 5})
			items := conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/items")
			So(items.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_EVENT)
			So(items.State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
		})

		Convey("A confirmation under way, or one that did not commit, confirms nothing\n", func() {
			draft.Items.Confirmation.Commit = ""
			So(conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/items").State,
				ShouldEqual, interfaces.CONCEPT_STATE_PENDING)
		})

		Convey("A confirmation of an earlier draft, carried over by drafting again, confirms nothing\n", func() {
			draft.CreateTime = 11
			So(conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/items").State,
				ShouldEqual, interfaces.CONCEPT_STATE_PENDING)
		})

		Convey("An item taken out after the confirmation is out\n", func() {
			draft.Items.ObjectTypes[1].Adopted = false
			draft.Items.Decisions = append(draft.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES,
				ItemID: "items", Action: interfaces.DRAFT_DECISION_UNADOPT, By: reviewer, Time: 11})
			items := conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/items")
			So(items.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_NONE)
			So(items.State, ShouldEqual, interfaces.CONCEPT_STATE_JUDGED)
		})

		Convey("A requirement concept that only restates an item is no source of it\n", func() {
			req := readyRequirement()
			req.Spec.Concepts = []*interfaces.RequirementConcept{
				// Added by scenario drafting for the item it asks about, citing no document.
				{ID: "c3", Term: "Items", Category: "object", Source: interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS,
					MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "items"}},
				{ID: "k1", Term: "Store", Category: "object", Citations: []interfaces.RequirementCitation{{DocumentID: "d1"}},
					MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "stores"}},
			}
			concepts := conceptsOf(ctx, "p1", draft, req, true)
			So(conceptByKey(concepts, "object_types/items").Sources, ShouldHaveLength, 1)
			So(conceptByKey(concepts, "requirement_concepts/c3"), ShouldBeNil)
			stores := conceptByKey(concepts, "object_types/stores")
			So(stores.Sources, ShouldHaveLength, 2)
			So(stores.Sources[1].Name, ShouldEqual, "Store")
		})
	})
}
