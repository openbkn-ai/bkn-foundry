// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_ProposedMatch(t *testing.T) {
	Convey("Test the match pass concepts proposes\n", t, func() {
		docs := documentsByID(testDocuments())
		tables := []*interfaces.VegaTable{{ID: "t_cust", Name: "Customers"}, {ID: "t_ord", Name: "Orders"}}
		out := &conceptsOutput{Concepts: []agentConcept{
			{Term: "Client", Category: "object", Citations: []agentCitation{cite("prd", "a customer's request")},
				Match: &agentMatch{Table: "Customers", Basis: "the customers table records clients"}},
			{Term: "Buyer", Category: "object", Citations: []agentCitation{cite("prd", "a customer's request")},
				Match: &agentMatch{ObjectTypeID: "customer", Table: "Orders", Basis: "a buyer is a customer"}},
			{Term: "Credit limit", Category: "object", Citations: []agentCitation{cite("prd", "a customer's request")},
				Match: &agentMatch{Table: "Limits", Basis: "no table holds a limit"}},
			{Term: "Carrier", Category: "object", Citations: []agentCitation{cite("prd", "a customer's request")},
				Match: &agentMatch{ObjectTypeID: "carrier"}},
		}}
		spec := emptySpec()
		buildConcepts(out, docs, testDraft(), tables, spec)

		So(spec.Concepts, ShouldHaveLength, 4)
		Convey("A table is named as the message listed it, and stored by its id\n", func() {
			So(spec.Concepts[0].Match, ShouldResemble, &interfaces.RequirementMatch{
				Target: &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: "t_cust"},
				Basis:  "the customers table records clients", By: interfaces.REQUIREMENT_MATCH_BY_AI})
		})
		Convey("An adopted object type comes before a table\n", func() {
			So(spec.Concepts[1].Match.Target, ShouldResemble,
				&interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_OBJECT_TYPE, ID: "customer"})
		})
		Convey("What names nothing listed is no target; the basis says why\n", func() {
			So(spec.Concepts[2].Match.Target, ShouldBeNil)
			So(spec.Concepts[2].Match.Basis, ShouldEqual, "no table holds a limit")
			So(spec.Concepts[3].Match.Target, ShouldBeNil)
		})
		Convey("The concepts pass asks for a match on every concept\n", func() {
			concept := conceptsResponseFormat()["properties"].(map[string]any)["concepts"].(map[string]any)["items"].(map[string]any)
			So(concept["properties"], ShouldContainKey, "match")
			So(concept["properties"], ShouldContainKey, "term")
		})
	})
}

func Test_MatchConcept(t *testing.T) {
	Convey("Test matching a requirement concept to the data\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, now: func() int64 { return 100 }}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller, CatalogIDs: []string{"cat"}}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).AnyTimes()
		draft := testDraft()
		draft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "t_cust"}
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
		va.EXPECT().ListTables(gomock.Any(), gomock.Any(), "cat").Return([]*interfaces.VegaTable{{ID: "t_ship", Name: "Shipments"}}, nil).AnyTimes()

		proposed := &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: "t_cust"}
		read := func() {
			spec := readySpec()
			spec.Spec.Concepts[1].Match = &interfaces.RequirementMatch{Target: proposed, Basis: "clients", By: interfaces.REQUIREMENT_MATCH_BY_AI}
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
		}
		var saved *interfaces.Requirement
		save := func() {
			ra.EXPECT().UpdateRequirement(gomock.Any(), gomock.Any(), int64(7)).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement, _ int64) (bool, error) {
					saved = req
					return true, nil
				})
		}
		match := func(edit interfaces.RequirementMatchEdit) error {
			edit.UpdateTime = 7
			_, err := svc.MatchConcept(ctx, token, "p1", "c2", edit, caller)
			return err
		}

		Convey("Confirming the proposal keeps its basis, and maps the object drafted from the table\n", func() {
			read()
			save()
			So(match(interfaces.RequirementMatchEdit{Target: proposed}), ShouldBeNil)
			c := saved.Spec.Concepts[1]
			So(c.Match, ShouldResemble, &interfaces.RequirementMatch{Target: proposed, Basis: "clients",
				By: interfaces.REQUIREMENT_MATCH_BY_AI, Confirmed: true})
			So(c.MapsTo, ShouldResemble, &customerRef)
			So(c.Edited, ShouldBeTrue)
			// The questions that follow their concepts need what it maps to.
			So(saved.Spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{orderRef, customerRef})
		})

		Convey("Choosing another table of the catalogs is the person's match\n", func() {
			read()
			save()
			target := &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: "t_ship"}
			So(match(interfaces.RequirementMatchEdit{Target: target}), ShouldBeNil)
			So(saved.Spec.Concepts[1].Match, ShouldResemble, &interfaces.RequirementMatch{Target: target,
				By: interfaces.REQUIREMENT_MATCH_BY_PERSON, Confirmed: true})
			So(saved.Spec.Concepts[1].MapsTo, ShouldBeNil)
		})

		Convey("Leaving it out keeps what it was matched to; nothing in the data clears it\n", func() {
			read()
			save()
			So(match(interfaces.RequirementMatchEdit{Excluded: true}), ShouldBeNil)
			So(saved.Spec.Concepts[1].Match.Excluded, ShouldBeTrue)
			So(saved.Spec.Concepts[1].Match.Target, ShouldResemble, proposed)

			read()
			save()
			So(match(interfaces.RequirementMatchEdit{}), ShouldBeNil)
			So(saved.Spec.Concepts[1].Match, ShouldResemble, &interfaces.RequirementMatch{By: interfaces.REQUIREMENT_MATCH_BY_PERSON})
		})

		Convey("Only a table of the catalogs or an object type of the draft is a target\n", func() {
			read()
			err := match(interfaces.RequirementMatchEdit{Target: &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_TABLE, ID: "elsewhere"}})
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			read()
			err = match(interfaces.RequirementMatchEdit{Target: &interfaces.RequirementMatchTarget{Kind: interfaces.REQUIREMENT_MATCH_OBJECT_TYPE, ID: "carrier"}})
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			err = match(interfaces.RequirementMatchEdit{Target: &interfaces.RequirementMatchTarget{Kind: "relation_type", ID: "x"}})
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
		})
	})
}
