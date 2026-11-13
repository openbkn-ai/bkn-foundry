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
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/projecthooks"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// matchDraft is conceptDraft with the dataset an extraction prepared for its document order, and an
// object type the network has that reads that dataset.
func matchDraft() *interfaces.Draft {
	draft := conceptDraft()
	items := &draft.Items
	items.DatasetPlans = []*interfaces.DraftDatasetPlan{{ID: "g1", Datasets: []interfaces.DraftDatasetSpec{
		{ObjectTypeID: "order_doc", ResourceID: "ds_orders", Name: "Order extraction"},
		{ObjectTypeID: "contract", ResourceID: "ds_contracts", Name: "Contract extraction"},
	}}}
	items.ObjectTypes = append(items.ObjectTypes, &interfaces.DraftObjectType{ID: "contract", Name: "Contract",
		Adopted: true, Existing: true, Table: interfaces.DraftTable{ResourceID: "ds_contracts", Name: "contracts"}})
	return draft
}

func matchRequirement() *interfaces.Requirement {
	req := conceptRequirement()
	table := func(id string) *interfaces.RequirementMatchTarget {
		return &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: id}
	}
	req.Spec.Concepts = append(req.Spec.Concepts,
		&interfaces.RequirementConcept{ID: "k6", Term: "Goods", Category: "object",
			Citations: []interfaces.RequirementCitation{{DocumentID: "d1", Quote: "goods on the shelf"}},
			Match:     &interfaces.RequirementMatch{Target: table("r_items"), Basis: "items lists the goods", By: interfaces.REQUIREMENT_MATCH_BY_AI}},
		&interfaces.RequirementConcept{ID: "k7", Term: "Purchase order", Category: "object",
			Match: &interfaces.RequirementMatch{Target: &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_OBJECT_TYPE,
				ID: "order_doc"}, By: interfaces.REQUIREMENT_MATCH_BY_PERSON, Confirmed: true}},
		&interfaces.RequirementConcept{ID: "k8", Term: "Credit limit", Category: "object",
			Match: &interfaces.RequirementMatch{Basis: "no table holds a limit", By: interfaces.REQUIREMENT_MATCH_BY_AI}},
		&interfaces.RequirementConcept{ID: "k9", Term: "Shelf", Category: "object",
			Match: &interfaces.RequirementMatch{Target: table("r_stores"), By: interfaces.REQUIREMENT_MATCH_BY_AI, Excluded: true}},
		// Its match names a table no concept reads any more.
		&interfaces.RequirementConcept{ID: "k10", Term: "Region", Category: "object",
			Match: &interfaces.RequirementMatch{Target: table("r_gone"), Basis: "regions", By: interfaces.REQUIREMENT_MATCH_BY_AI}},
	)
	return req
}

func requirementByID(c *interfaces.DraftConcepts, id string) *interfaces.DraftRequirementConcept {
	for i := range c.Requirements {
		if c.Requirements[i].ID == id {
			return &c.Requirements[i]
		}
	}
	return nil
}

func Test_ConceptBindings(t *testing.T) {
	Convey("Test the data each concept is bound to\n", t, func() {
		concepts := conceptsOf(context.Background(), "p1", matchDraft(), matchRequirement(), true)

		Convey("An object read from a table is bound to the table\n", func() {
			So(conceptByKey(concepts, "object_types/stores").Binding, ShouldResemble,
				&interfaces.DraftConceptBinding{Kind: interfaces.CONCEPT_BINDING_TABLE, ID: "r_stores", Name: "stores"})
			So(conceptByKey(concepts, "value_types/currency").Binding.Kind, ShouldEqual, interfaces.CONCEPT_BINDING_TABLE)
			So(conceptByKey(concepts, "relation_types/items_to_stores").Binding.ID, ShouldEqual, "r_items")
		})

		Convey("A document's object is bound to the dataset its extraction prepared\n", func() {
			So(conceptByKey(concepts, "object_types/order_doc").Binding, ShouldResemble,
				&interfaces.DraftConceptBinding{Kind: interfaces.CONCEPT_BINDING_DATASET, ID: "ds_orders", Name: "Order extraction"})
		})

		Convey("What the network has is bound to the dataset it reads, when an extraction prepared it\n", func() {
			So(conceptByKey(concepts, "object_types/contract").Binding.Kind, ShouldEqual, interfaces.CONCEPT_BINDING_DATASET)
		})

		Convey("Before drafting, what the network reads outside the catalogs' tables is a dataset\n", func() {
			existing := interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{
				{ID: "stores", Name: "Store", Adopted: true, Existing: true, Table: interfaces.DraftTable{ResourceID: "r_stores"}},
				{ID: "letters", Name: "Letter", Adopted: true, Existing: true, Table: interfaces.DraftTable{ResourceID: "ds_letters"}},
			}}
			before := &beforeDrafting{existing: existing, tables: []*interfaces.VegaTable{{ID: "r_stores", Name: "stores"}}}
			got := projectConcepts(context.Background(), "p1", nil, nil, false, nil, before, nil)
			So(conceptByKey(got, "object_types/stores").Binding.Kind, ShouldEqual, interfaces.CONCEPT_BINDING_TABLE)
			So(conceptByKey(got, "object_types/letters").Binding.Kind, ShouldEqual, interfaces.CONCEPT_BINDING_DATASET)
		})
	})
}

func Test_RequirementMatches(t *testing.T) {
	Convey("Test matching requirement concepts to the data\n", t, func() {
		concepts := conceptsOf(context.Background(), "p1", matchDraft(), matchRequirement(), true)

		Convey("A mapping drafting made is a match no one confirmed\n", func() {
			store := requirementByID(concepts, "k1")
			So(store.Match.Key, ShouldEqual, "object_types/stores")
			So(store.Match.Target, ShouldResemble, &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: "r_stores"})
			So(store.State, ShouldEqual, interfaces.REQUIREMENT_MATCH_PENDING)
		})

		Convey("A term naming an object matches it by name\n", func() {
			visits := requirementByID(concepts, "k2")
			So(visits.Match.Key, ShouldEqual, "object_types/visits")
			So(visits.By, ShouldEqual, interfaces.REQUIREMENT_MATCH_BY_NAME)
		})

		Convey("AI's match names the object read from the table, with its basis and the quote\n", func() {
			goods := requirementByID(concepts, "k6")
			So(goods.Match.Key, ShouldEqual, "object_types/items")
			So(goods.Basis, ShouldEqual, "items lists the goods")
			So(goods.Quote, ShouldEqual, "goods on the shelf")
			So(goods.State, ShouldEqual, interfaces.REQUIREMENT_MATCH_PENDING)
			// It is the object's source, as a mapping is.
			So(conceptByKey(concepts, "object_types/items").Sources, ShouldContain,
				interfaces.DraftConceptSource{Kind: interfaces.CONCEPT_SOURCE_REQUIREMENT, ID: "k6", Name: "Goods", DocumentIDs: []string{"d1"}})
			So(conceptByKey(concepts, "requirement_concepts/k6"), ShouldBeNil)
		})

		Convey("A person's choice is confirmed, and a knowledge-base object is named by its object type\n", func() {
			order := requirementByID(concepts, "k7")
			So(order.State, ShouldEqual, interfaces.REQUIREMENT_MATCH_CONFIRMED)
			So(order.Match.Binding.Kind, ShouldEqual, interfaces.CONCEPT_BINDING_DATASET)
			So(order.Match.Target, ShouldResemble, &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_OBJECT_TYPE, ID: "order_doc"})
		})

		Convey("Nothing in the data: unmatched, with why; never an object of its own\n", func() {
			limit := requirementByID(concepts, "k8")
			So(limit.Match, ShouldBeNil)
			So(limit.State, ShouldEqual, interfaces.REQUIREMENT_MATCH_UNMATCHED)
			So(limit.Basis, ShouldEqual, "no table holds a limit")
			So(requirementByID(concepts, "k3").State, ShouldEqual, interfaces.REQUIREMENT_MATCH_UNMATCHED)
			So(requirementByID(concepts, "k10").State, ShouldEqual, interfaces.REQUIREMENT_MATCH_UNMATCHED)
		})

		Convey("Left out keeps what it was matched to; not ontology is left out; evidence is not listed\n", func() {
			shelf := requirementByID(concepts, "k9")
			So(shelf.State, ShouldEqual, interfaces.REQUIREMENT_MATCH_EXCLUDED)
			So(shelf.Match.Key, ShouldEqual, "object_types/stores")
			audit := requirementByID(concepts, "k4")
			So(audit.State, ShouldEqual, interfaces.REQUIREMENT_MATCH_EXCLUDED)
			So(audit.Basis, ShouldEqual, "Technical record")
			So(requirementByID(concepts, "k5"), ShouldBeNil)
		})

		Convey("A person takes a concept judged no part of the ontology back in: it waits for data\n", func() {
			req := matchRequirement()
			req.Spec.Concepts[3].Match = &interfaces.RequirementMatch{By: interfaces.REQUIREMENT_MATCH_BY_PERSON}
			got := conceptsOf(context.Background(), "p1", matchDraft(), req, true)
			So(requirementByID(got, "k4").State, ShouldEqual, interfaces.REQUIREMENT_MATCH_UNMATCHED)
		})

		Convey("Before drafting, AI's match names a table's candidate\n", func() {
			before := &beforeDrafting{tables: []*interfaces.VegaTable{{ID: "r_items", Name: "items"}}}
			got := projectConcepts(context.Background(), "p1", nil, matchRequirement(), true, nil, before, nil)
			goods := requirementByID(got, "k6")
			So(goods.Match.Key, ShouldEqual, "table_concepts/r_items")
			So(goods.Match.Target, ShouldResemble, &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: "r_items"})
		})
	})
}

// readableHooks are conceptHooks of an edition that reads document items.
type readableHooks struct{ conceptHooks }

func (readableHooks) CheckRead(context.Context, string, *interfaces.Draft) error { return nil }

func Test_MergeAcrossBindings(t *testing.T) {
	defer projecthooks.SetForTest(projecthooks.Hooks{Drafts: readableHooks{}})()
	Convey("Test that concepts bound to different data are never merged\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, rqa: rqa}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(matchRequirement(), true, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(matchDraft(), true, nil).AnyTimes()

		Convey("A table's object is not merged into a knowledge-base extraction's\n", func() {
			_, err := svc.MergeConcept(ctx, token, "p1", interfaces.DraftConceptMerge{UpdateTime: readTime,
				Key: "object_types/items", Into: "object_types/order_doc"}, caller)
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			So(httpErr.HTTPCode, ShouldEqual, http.StatusBadRequest)
			So(httpErr.BaseError.ErrorCode, ShouldEqual, perrors.BknProject_Draft_CrossBindingMerge)
		})

		Convey("Ask AI's merge across them is dropped\n", func() {
			concepts := conceptsOf(ctx, "p1", matchDraft(), matchRequirement(), true)
			_, reason := checkConceptRevision(ctx, &matchDraft().Items, concepts,
				conceptRevisionOutput{Field: interfaces.CONCEPT_REVISION_MERGE, Key: "object_types/items", Into: "object_types/order_doc"})
			So(reason, ShouldEqual, askDroppedInvalid)
		})

		Convey("Within one kind, merging is recorded\n", func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
			got, err := svc.MergeConcept(ctx, token, "p1", interfaces.DraftConceptMerge{UpdateTime: readTime,
				Key: "object_types/items", Into: "object_types/stores"}, caller)
			So(err, ShouldBeNil)
			So(conceptByKey(got, "object_types/stores").Merged, ShouldHaveLength, 1)
		})
	})
}
