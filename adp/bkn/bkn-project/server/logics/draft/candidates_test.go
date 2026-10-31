// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
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

var reviewer = interfaces.AccountInfo{ID: "u7", Type: "user"}

// candidateDraft has stores a person adopted, items drafting adopted that no one reviewed, visits
// waiting for an identity, notes a person left out with why, and relations between them.
func candidateDraft() *interfaces.Draft {
	ot := func(id string, name string, adopted bool, catalog string) *interfaces.DraftObjectType {
		return &interfaces.DraftObjectType{ID: id, Name: name, Adopted: adopted,
			Table: interfaces.DraftTable{ResourceID: "r_" + id, Name: id, CatalogID: catalog}}
	}
	visits := ot("visits", "Customer Visits", false, "c1")
	visits.NeedsIdentity = true
	items := ot("items", "Items", true, "c2")
	items.AddedByCatalog = "c2"
	return &interfaces.Draft{
		ProjectID: "p1", Status: interfaces.DRAFT_STATUS_READY, UpdateTime: readTime,
		Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{
				ot("stores", "Stores", true, "c1"), items, visits, ot("notes", "Notes", false, "c1"),
			},
			RelationTypes: []*interfaces.DraftRelationType{
				{ID: "items_to_stores", Name: "Store of item", Source: "items", Target: "stores", Adopted: true},
				{ID: "visits_to_stores", Name: "Store of visit", Source: "visits", Target: "stores",
					ProposedBecause: proposedEndNotAdopted},
			},
			Inputs: &interfaces.DraftInputs{Tables: []interfaces.DraftInputTable{{ResourceID: "r_stores", Rows: 12}}},
			Decisions: []*interfaces.DraftDecision{
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "stores", Action: interfaces.DRAFT_DECISION_RENAME, By: reviewer, Time: 1},
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "stores", Action: interfaces.DRAFT_DECISION_ADOPT, By: reviewer, Time: 2},
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "notes", Action: interfaces.DRAFT_DECISION_UNADOPT, By: reviewer, Time: 3,
					Reason: "Free text, not a business object"},
				// Renaming alone reviews nothing.
				{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES, ItemID: "items", Action: interfaces.DRAFT_DECISION_RENAME, By: reviewer, Time: 4},
			},
		},
	}
}

func readyRequirement() *interfaces.Requirement {
	return &interfaces.Requirement{ProjectID: "p1", Status: interfaces.REQUIREMENT_STATUS_READY, Spec: interfaces.RequirementSpec{
		Questions: []*interfaces.RequirementQuestion{{ID: "q1", Text: "Which items does a store sell?", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
			Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "items"}}}},
		Concepts: []*interfaces.RequirementConcept{
			{ID: "k1", Term: "Store", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "stores"}},
			// Mapped to nothing: its term is the visits' name, but for case and spaces.
			{ID: "k2", Term: " customer  visits "},
			// Mapped to nothing and named like no item: needs nothing.
			{ID: "k3", Term: "Customer"},
		},
	}}
}

func Test_Candidates(t *testing.T) {
	Convey("Test the draft's items as candidates\n", t, func() {
		draft := candidateDraft()
		find := func(c *interfaces.DraftCandidates, id string) interfaces.DraftCandidate {
			for _, x := range c.Candidates {
				if x.ID == id {
					return x
				}
			}
			panic(id)
		}

		Convey("Reviewed is the last decision on whether an item is in; a renaming alone is none\n", func() {
			c := candidatesOf(draft, nil, "")

			So(find(c, "stores").State, ShouldEqual, interfaces.CANDIDATE_STATE_ADOPTED)
			So(*find(c, "stores").Reviewed, ShouldResemble, interfaces.DraftReview{By: reviewer, Time: 2})
			So(find(c, "notes").State, ShouldEqual, interfaces.CANDIDATE_STATE_EXCLUDED)
			So(find(c, "notes").ExcludeReason, ShouldEqual, "Free text, not a business object")
			So(find(c, "items").State, ShouldEqual, interfaces.CANDIDATE_STATE_PENDING)
			So(find(c, "items").Reviewed, ShouldBeNil)
			So(find(c, "items").AddedByCatalog, ShouldEqual, "c2")
			So(find(c, "stores").Rows, ShouldEqual, 12)
			So(find(c, "visits").NeedsIdentity, ShouldBeTrue)
			So(c.Counts, ShouldResemble, map[string]int{interfaces.CANDIDATE_STATE_PENDING: 4,
				interfaces.CANDIDATE_STATE_ADOPTED: 1, interfaces.CANDIDATE_STATE_EXCLUDED: 1})
			So(find(c, "items").NeededByRequirements, ShouldBeNil)
			So(unreviewed(&draft.Items), ShouldEqual, 4)
		})

		Convey("An item adopted again after it was left out has no reason left\n", func() {
			draft.Items.Decisions = append(draft.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES,
				ItemID: "notes", Action: interfaces.DRAFT_DECISION_ADOPT, By: reviewer, Time: 5})
			draft.Items.ObjectTypes[3].Adopted = true

			So(find(candidatesOf(draft, nil, ""), "notes").ExcludeReason, ShouldBeEmpty)
		})

		Convey("The specification's questions and concepts needing an item put it first\n", func() {
			c := candidatesOf(draft, neededByRequirements(readyRequirement(), true, &draft.Items), "")

			So(find(c, "items").NeededByRequirements.Questions, ShouldResemble,
				[]interfaces.DraftNeedingQuestion{{ID: "q1", Text: "Which items does a store sell?"}})
			So(find(c, "stores").NeededByRequirements.Concepts, ShouldResemble, []interfaces.DraftNeedingConcept{{ID: "k1", Term: "Store"}})
			So(find(c, "visits").NeededByRequirements.Concepts, ShouldResemble,
				[]interfaces.DraftNeedingConcept{{ID: "k2", Term: " customer  visits "}})
			So(find(c, "notes").NeededByRequirements, ShouldBeNil)
			// Needed first, then not reviewed, then object types before relation types, by name.
			order := []string{}
			for _, x := range c.Candidates {
				order = append(order, x.ID)
			}
			So(order, ShouldResemble, []string{"visits", "items", "stores", "items_to_stores", "visits_to_stores", "notes"})
		})

		Convey("Without a ready specification nothing is needed by it\n", func() {
			req := readyRequirement()
			req.Status = interfaces.REQUIREMENT_STATUS_DRAFTING

			So(neededByRequirements(req, true, &draft.Items), ShouldBeNil)
			So(neededByRequirements(nil, false, &draft.Items), ShouldBeNil)
		})

		Convey("A catalog lists the candidates that come from it\n", func() {
			c := candidatesOf(draft, nil, "c2")

			ids := []string{}
			for _, x := range c.Candidates {
				ids = append(ids, x.ID)
			}
			So(ids, ShouldResemble, []string{"items", "items_to_stores"})
		})

		Convey("A relation a catalog added is that catalog's, whatever the catalog of its ends\n", func() {
			draft.Items.RelationTypes = append(draft.Items.RelationTypes, &interfaces.DraftRelationType{
				ID: "stores_to_items", Name: "Items of store", Source: "stores", Target: "items", AddedByCatalog: "c2"})

			ids := []string{}
			for _, x := range candidatesOf(draft, nil, "c2").Candidates {
				ids = append(ids, x.ID)
			}

			So(ids, ShouldContain, "stores_to_items")
			So(ids, ShouldNotContain, "stores")
		})

		Convey("Object types come before relation types of the same rank, whatever their names\n", func() {
			draft.Items.RelationTypes[0].Name = "A store of item"
			c := candidatesOf(draft, nil, "c2")

			So(c.Candidates[0].ID, ShouldEqual, "items")
			So(c.Candidates[1].ID, ShouldEqual, "items_to_stores")
		})

		Convey("Each decision on whether an item is in reviews it; a renaming, a description or a sync does not\n", func() {
			for action, reviews := range map[string]bool{
				interfaces.DRAFT_DECISION_ADOPT: true, interfaces.DRAFT_DECISION_UNADOPT: true,
				interfaces.DRAFT_DECISION_IDENTITY: true, interfaces.DRAFT_DECISION_SAVE: true,
				interfaces.DRAFT_DECISION_CREATE: true, interfaces.DRAFT_DECISION_RENAME: false,
				interfaces.DRAFT_DECISION_DESCRIBE: false, interfaces.DRAFT_DECISION_SYNC: false,
			} {
				d := candidateDraft()
				d.Items.Decisions = []*interfaces.DraftDecision{{Kind: interfaces.DRAFT_KIND_RELATION_TYPES,
					ItemID: "items_to_stores", Action: action, By: reviewer, Time: 7}}

				So(find(candidatesOf(d, nil, ""), "items_to_stores").Reviewed != nil, ShouldEqual, reviews)
			}
		})

		Convey("Accepted suggestions, as read back from storage, review what they list\n", func() {
			data, _ := json.Marshal(&interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_DRAFT,
				Action: interfaces.DRAFT_DECISION_ACCEPT_SUGGESTIONS, After: []string{"object_types/items"}, By: reviewer, Time: 9})
			var stored interfaces.DraftDecision
			So(json.Unmarshal(data, &stored), ShouldBeNil)
			draft.Items.Decisions = append(draft.Items.Decisions, &stored)

			So(find(candidatesOf(draft, nil, ""), "items").Reviewed.Time, ShouldEqual, 9)
		})
	})
}

func Test_CandidateService(t *testing.T) {
	Convey("Test reading candidates and accepting the suggestions\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, rqa: rqa}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
		draft := candidateDraft()
		codeOf := func(err error) (int, string) {
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			return httpErr.HTTPCode, httpErr.BaseError.ErrorCode
		}

		Convey("Candidates are read with the specification's needs\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(readyRequirement(), true, nil)

			c, err := svc.GetCandidates(ctx, token, "p1", "")

			So(err, ShouldBeNil)
			So(c.Candidates[0].ID, ShouldEqual, "visits")
			So(c.DraftUpdateTime, ShouldEqual, readTime)
		})

		Convey("Without a draft there are no candidates\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)

			_, err := svc.GetCandidates(ctx, token, "p1", "")

			status, _ := codeOf(err)
			So(status, ShouldEqual, http.StatusNotFound)
		})

		Convey("Accepting the suggestions reviews what drafting settled, and leaves what needs a person\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)

			got, err := svc.AcceptSuggestions(ctx, token, "p1", interfaces.DraftAcceptSuggestions{UpdateTime: readTime}, caller)

			So(err, ShouldBeNil)
			So(got.Accepted, ShouldResemble, []string{"object_types/items", "relation_types/items_to_stores"})
			So(got.Skipped, ShouldResemble, []string{"object_types/visits", "relation_types/visits_to_stores"})
			last := got.Draft.Items.Decisions[len(got.Draft.Items.Decisions)-1]
			So(last.Action, ShouldEqual, interfaces.DRAFT_DECISION_ACCEPT_SUGGESTIONS)
			So(last.By, ShouldResemble, caller)
			// Adoption stays as drafting suggested it.
			So(got.Draft.Items.ObjectTypes[1].Adopted, ShouldBeTrue)
			So(unreviewed(&got.Draft.Items), ShouldEqual, 2)
		})

		Convey("With nothing left to accept nothing is written\n", func() {
			draft.Items.ObjectTypes = draft.Items.ObjectTypes[2:3]
			draft.Items.RelationTypes = nil
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			got, err := svc.AcceptSuggestions(ctx, token, "p1", interfaces.DraftAcceptSuggestions{UpdateTime: readTime}, caller)

			So(err, ShouldBeNil)
			So(got.Accepted, ShouldBeEmpty)
			So(got.Skipped, ShouldResemble, []string{"object_types/visits"})
		})

		Convey("A draft changed since it was read is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)

			_, err := svc.AcceptSuggestions(ctx, token, "p1", interfaces.DraftAcceptSuggestions{UpdateTime: readTime - 1}, caller)

			_, code := codeOf(err)
			So(code, ShouldEqual, perrors.BknProject_Draft_Stale)
		})

		Convey("Leaving an item out records why; a reason without leaving out is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			out, in := false, true

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "items",
				interfaces.DraftItemEdit{UpdateTime: readTime, Adopted: &in, Reason: "why"}, caller)
			status, _ := codeOf(err)
			So(status, ShouldEqual, http.StatusBadRequest)

			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).Return(true, nil)
			got, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "items",
				interfaces.DraftItemEdit{UpdateTime: readTime, Adopted: &out, Reason: " Duplicate of stores "}, caller)
			So(err, ShouldBeNil)
			var unadopt *interfaces.DraftDecision
			for _, d := range got.Items.Decisions {
				if d.ItemID == "items" && d.Action == interfaces.DRAFT_DECISION_UNADOPT {
					unadopt = d
				}
			}
			So(unadopt.Reason, ShouldEqual, "Duplicate of stores")
		})

		Convey("A reason too long is refused\n", func() {
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()
			out := false
			long := make([]rune, interfaces.DRAFT_REASON_MAX_LENGTH+1)
			for i := range long {
				long[i] = '因'
			}

			_, err := svc.EditDraftItem(ctx, token, "p1", interfaces.DRAFT_KIND_OBJECT_TYPES, "items",
				interfaces.DraftItemEdit{UpdateTime: readTime, Adopted: &out, Reason: string(long)}, caller)

			status, _ := codeOf(err)
			So(status, ShouldEqual, http.StatusBadRequest)
		})
	})
}
