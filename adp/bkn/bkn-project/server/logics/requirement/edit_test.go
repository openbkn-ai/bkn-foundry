// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

var (
	orderRef    = interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
	customerRef = interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}
	relationRef = interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "sales_order_customer"}
)

func ptr[T any](v T) *T { return &v }

// readySpec is a drafted specification: q1 follows its concepts, q2 has needs a person set.
func readySpec() *interfaces.Requirement {
	cite := []interfaces.RequirementCitation{{DocumentID: "prd", Quote: "q", BlockID: "b1", Start: 0, End: 1}}
	return &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY, UpdateTime: 7,
		Spec: interfaces.RequirementSpec{
			Scenarios: []*interfaces.RequirementScenario{{ID: "s1", Name: "Answer", Citations: cite}},
			Concepts: []*interfaces.RequirementConcept{
				{ID: "c1", Term: "Order", Category: "object", MapsTo: &orderRef, Citations: cite},
				{ID: "c2", Term: "Customer", Category: "object", Citations: cite},
			},
			Questions: []*interfaces.RequirementQuestion{
				{ID: "q1", Text: "Which orders?", ScenarioIDs: []string{"s1"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
					Basis: interfaces.RequirementBasis{ConceptIDs: []string{"c1", "c2"}}, Needs: []interfaces.RequirementItemRef{orderRef},
					NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS, Citations: cite},
				{ID: "q2", Text: "Whose orders?", ScenarioIDs: []string{"s1"}, Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
					Basis: interfaces.RequirementBasis{ConceptIDs: []string{"c1"}}, Needs: []interfaces.RequirementItemRef{customerRef},
					NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_PERSON, Citations: cite},
			},
			Rules: []*interfaces.RequirementRule{{ID: "r1", Statement: "Signed changes only", Citations: cite}},
			Conflicts: []*interfaces.RequirementConflict{{ID: "k1", Topic: "Email", Kind: conflictRule,
				Sides: []interfaces.RequirementConflictSide{
					{RequirementCitation: interfaces.RequirementCitation{DocumentID: "prd", Quote: "a"}},
					{RequirementCitation: interfaces.RequirementCitation{DocumentID: "pol", Quote: "b"}},
				}}},
			Choices: []*interfaces.RequirementChoice{{ID: "f1", Question: "Event?", RecommendedOptionID: "o1",
				Options: []interfaces.RequirementOption{{ID: "o1", Label: "Event"}, {ID: "o2", Label: "Property"}}}},
			Rejected: []*interfaces.RequirementRejected{},
		}}
}

func Test_RequirementEdits(t *testing.T) {
	Convey("Test requirement edits\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, now: func() int64 { return 100 }}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(testDraft(), true, nil).AnyTimes()

		var saved *interfaces.Requirement
		expectRead := func() { ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(readySpec(), true, nil) }
		expectSave := func() {
			ra.EXPECT().UpdateRequirement(gomock.Any(), gomock.Any(), int64(7)).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement, _ int64) (bool, error) {
					saved = req
					return true, nil
				})
		}

		Convey("An edit changes the fields it sets, marks the item edited and moves the update time\n", func() {
			expectRead()
			expectSave()

			req, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_SCENARIOS, "s1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Name: ptr("Answer date requests"), Actor: ptr("Sales")}, caller)

			So(err, ShouldBeNil)
			So(req.UpdateTime, ShouldEqual, 100)
			s := saved.Spec.Scenarios[0]
			So(s.Name, ShouldEqual, "Answer date requests")
			So(s.Actor, ShouldEqual, "Sales")
			So(s.Edited, ShouldBeTrue)
			// A person changed it: it is theirs, kept when drafting again.
			So(s.Accepted, ShouldBeTrue)
			So(s.Citations, ShouldHaveLength, 1)
		})

		Convey("Review accepts and unaccepts scenarios and questions, and nothing else\n", func() {
			spec := readySpec()
			spec.Spec.Questions[1].Accepted = true
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
			expectSave()

			_, err := svc.ReviewItems(ctx, token, "p1", interfaces.RequirementReview{UpdateTime: 7,
				Accept: []interfaces.RequirementReviewItem{{Kind: interfaces.REQUIREMENT_KIND_SCENARIOS, ID: "s1"},
					{Kind: interfaces.REQUIREMENT_KIND_QUESTIONS, ID: "q1"}},
				Unaccept: []interfaces.RequirementReviewItem{{Kind: interfaces.REQUIREMENT_KIND_QUESTIONS, ID: "q2"}}}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Scenarios[0].Accepted, ShouldBeTrue)
			So(saved.Spec.Questions[0].Accepted, ShouldBeTrue)
			So(saved.Spec.Questions[1].Accepted, ShouldBeFalse)
			// Accepting changes no content: the item is not marked edited. It is accepted as the
			// ontology is now; unaccepting keeps the signature it had.
			So(saved.Spec.Questions[0].Edited, ShouldBeFalse)
			So(saved.Spec.Questions[0].OntologySignature, ShouldEqual, ontologySignature(testDraft(), saved.Spec.Questions[0]))
			So(saved.Spec.Questions[0].OntologySignature, ShouldNotBeEmpty)
			So(saved.Spec.Questions[1].OntologySignature, ShouldBeEmpty)
		})

		Convey("A review of nothing, of an item twice, of another kind or of a missing item is refused\n", func() {
			q1 := interfaces.RequirementReviewItem{Kind: interfaces.REQUIREMENT_KIND_QUESTIONS, ID: "q1"}
			_, err := svc.ReviewItems(ctx, token, "p1", interfaces.RequirementReview{UpdateTime: 7}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)

			for _, review := range []interfaces.RequirementReview{
				{UpdateTime: 7, Accept: []interfaces.RequirementReviewItem{q1}, Unaccept: []interfaces.RequirementReviewItem{q1}},
				{UpdateTime: 7, Accept: []interfaces.RequirementReviewItem{{Kind: interfaces.REQUIREMENT_KIND_RULES, ID: "r1"}}},
			} {
				expectRead()
				_, err := svc.ReviewItems(ctx, token, "p1", review, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			}
			expectRead()
			_, err = svc.ReviewItems(ctx, token, "p1", interfaces.RequirementReview{UpdateTime: 7,
				Accept: []interfaces.RequirementReviewItem{{Kind: interfaces.REQUIREMENT_KIND_QUESTIONS, ID: "q9"}}}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("Setting a question's needs makes them the person's, checked against the draft\n", func() {
			expectRead()
			expectSave()

			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Needs: &[]interfaces.RequirementItemRef{relationRef, relationRef}}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{relationRef})
			So(saved.Spec.Questions[0].NeedsSource, ShouldEqual, interfaces.REQUIREMENT_NEEDS_FROM_PERSON)
		})

		Convey("An edit is refused when it is incomplete, too long, of another kind's field or refers to nothing\n", func() {
			for _, c := range []struct {
				kind, id string
				edit     interfaces.RequirementItemEdit
			}{
				{interfaces.REQUIREMENT_KIND_SCENARIOS, "s1", interfaces.RequirementItemEdit{UpdateTime: 7, Name: ptr("  ")}},
				{interfaces.REQUIREMENT_KIND_SCENARIOS, "s1", interfaces.RequirementItemEdit{UpdateTime: 7, Name: ptr(strings.Repeat("名", maxItemNameRunes+1))}},
				{interfaces.REQUIREMENT_KIND_RULES, "r1", interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr(strings.Repeat("a", maxItemTextRunes+1))}},
				{interfaces.REQUIREMENT_KIND_RULES, "r1", interfaces.RequirementItemEdit{UpdateTime: 7, Text: ptr("not a rule's")}},
				{interfaces.REQUIREMENT_KIND_RULES, "r1", interfaces.RequirementItemEdit{UpdateTime: 7}},
				{interfaces.REQUIREMENT_KIND_QUESTIONS, "q1", interfaces.RequirementItemEdit{UpdateTime: 7, Layer: ptr("other")}},
				{interfaces.REQUIREMENT_KIND_QUESTIONS, "q1", interfaces.RequirementItemEdit{UpdateTime: 7, ScenarioIDs: &[]string{"s9"}}},
				{interfaces.REQUIREMENT_KIND_QUESTIONS, "q1", interfaces.RequirementItemEdit{UpdateTime: 7,
					Needs: &[]interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "carrier"}}}},
				{interfaces.REQUIREMENT_KIND_QUESTIONS, "q1", interfaces.RequirementItemEdit{UpdateTime: 7,
					Needs: &[]interfaces.RequirementItemRef{{Kind: "table", ID: "customer"}}}},
				{interfaces.REQUIREMENT_KIND_CONCEPTS, "c1", interfaces.RequirementItemEdit{UpdateTime: 7, Category: ptr("thing")}},
			} {
				expectRead()

				_, err := svc.EditItem(ctx, token, "p1", c.kind, c.id, c.edit, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			}
		})

		Convey("An edit needs the update time read, a ready specification, and an item that exists\n", func() {
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
				interfaces.RequirementItemEdit{Statement: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_MissingUpdateTime)

			expectRead()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r9",
				interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)

			expectRead()
			_, err = svc.EditItem(ctx, token, "p1", "tables", "r1", interfaces.RequirementItemEdit{UpdateTime: 7}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)

			expectRead()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
				interfaces.RequirementItemEdit{UpdateTime: 6, Statement: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Stale)

			expectRead()
			ra.EXPECT().UpdateRequirement(gomock.Any(), gomock.Any(), int64(7)).Return(false, nil)
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Stale)

			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Status: interfaces.REQUIREMENT_STATUS_DRAFTING}, true, nil)
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)

			for _, none := range []*interfaces.Requirement{nil, {Status: interfaces.REQUIREMENT_STATUS_FAILED}} {
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(none, none != nil, nil)
				_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
					interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("x")}, caller)
				shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
			}
		})

		Convey("Only the owner edits\n", func() {
			other := pmock.NewMockProjectService(ctrl)
			other.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{
				ID: "p1", Owner: interfaces.AccountInfo{ID: "u2"}}}, nil)
			svc.ps = other

			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
		})

		Convey("An added item gets the next id and is the person's\n", func() {
			expectRead()
			expectSave()

			_, err := svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS,
				interfaces.RequirementItemEdit{UpdateTime: 7, Text: ptr("Which customers?"), Needs: &[]interfaces.RequirementItemRef{customerRef}}, caller)

			So(err, ShouldBeNil)
			q := saved.Spec.Questions[2]
			So(q.ID, ShouldEqual, "q3")
			So(q.Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(q.Needs, ShouldResemble, []interfaces.RequirementItemRef{customerRef})
			So(q.NeedsSource, ShouldEqual, interfaces.REQUIREMENT_NEEDS_FROM_PERSON)
			So(q.Edited, ShouldBeTrue)
			So(q.Accepted, ShouldBeTrue)
			// Added as the ontology is now.
			So(q.OntologySignature, ShouldEqual, ontologySignature(testDraft(), q))
			So(q.Citations, ShouldResemble, []interfaces.RequirementCitation{})
		})

		Convey("An id freed by a deletion is not given again\n", func() {
			spec := readySpec()
			spec.Spec.Rules = []*interfaces.RequirementRule{{ID: "r4"}}
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
			expectSave()

			_, err := svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES,
				interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("Codes have seven digits")}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Rules[1].ID, ShouldEqual, "r5")
		})

		Convey("Removing the item with the highest id does not free that id\n", func() {
			expectRead()
			expectSave()
			_, err := svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q2", 7, caller)
			So(err, ShouldBeNil)

			after := saved
			after.UpdateTime = 7
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(after, true, nil)
			expectSave()
			_, err = svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS,
				interfaces.RequirementItemEdit{UpdateTime: 7, Text: ptr("Which carriers?")}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Questions[len(saved.Spec.Questions)-1].ID, ShouldEqual, "q3")
		})

		Convey("An added item needs what identifies it, and a kind the specification has\n", func() {
			expectRead()
			_, err := svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_CONCEPTS,
				interfaces.RequirementItemEdit{UpdateTime: 7, Definition: ptr("A buyer")}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)

			expectRead()
			_, err = svc.AddItem(ctx, token, "p1", "tables", interfaces.RequirementItemEdit{UpdateTime: 7, Name: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("Deleting a concept takes it out of the questions, and needs that follow concepts follow\n", func() {
			expectRead()
			expectSave()

			_, err := svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, "c1", 7, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Concepts, ShouldHaveLength, 1)
			So(saved.Spec.Questions[0].Basis.ConceptIDs, ShouldResemble, []string{"c2"})
			So(saved.Spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{})
			So(saved.Spec.Questions[1].Basis.ConceptIDs, ShouldResemble, []string{})
			So(saved.Spec.Questions[1].Needs, ShouldResemble, []interfaces.RequirementItemRef{customerRef})
		})

		Convey("Deleting a scenario takes it out of the questions\n", func() {
			expectRead()
			expectSave()

			_, err := svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_SCENARIOS, "s1", 7, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Scenarios, ShouldBeEmpty)
			So(saved.Spec.Questions[0].ScenarioIDs, ShouldResemble, []string{})
		})

		Convey("A question with a person's needs goes back to following its concepts\n", func() {
			expectRead()
			expectSave()

			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q2",
				interfaces.RequirementItemEdit{UpdateTime: 7, FollowConcepts: ptr(true)}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Questions[1].NeedsSource, ShouldEqual, interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS)
			So(saved.Spec.Questions[1].Needs, ShouldResemble, []interfaces.RequirementItemRef{orderRef})

			expectRead()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q2",
				interfaces.RequirementItemEdit{UpdateTime: 7, FollowConcepts: ptr(true),
					Needs: &[]interfaces.RequirementItemRef{customerRef}}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
		})

		Convey("Mapping a concept updates the questions that follow their concepts, and only those\n", func() {
			expectRead()
			expectSave()

			_, err := svc.MapConcept(ctx, token, "p1", "c2", interfaces.RequirementMapping{UpdateTime: 7, MapsTo: &customerRef}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Concepts[1].MapsTo, ShouldResemble, &customerRef)
			So(saved.Spec.Concepts[1].Edited, ShouldBeTrue)
			So(saved.Spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{orderRef, customerRef})
			So(saved.Spec.Questions[1].Needs, ShouldResemble, []interfaces.RequirementItemRef{customerRef})
		})

		Convey("Two concepts of one item make one need\n", func() {
			expectRead()
			expectSave()

			_, err := svc.MapConcept(ctx, token, "p1", "c2", interfaces.RequirementMapping{UpdateTime: 7, MapsTo: &orderRef}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{orderRef})
		})

		Convey("A concept is unmapped with no item, and mapped only to an item the draft has\n", func() {
			expectRead()
			expectSave()

			_, err := svc.MapConcept(ctx, token, "p1", "c1", interfaces.RequirementMapping{UpdateTime: 7}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Concepts[0].MapsTo, ShouldBeNil)
			So(saved.Spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{})

			expectRead()
			_, err = svc.MapConcept(ctx, token, "p1", "c1", interfaces.RequirementMapping{UpdateTime: 7,
				MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "customer"}}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)

			expectRead()
			_, err = svc.MapConcept(ctx, token, "p1", "c9", interfaces.RequirementMapping{UpdateTime: 7}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("A conflict is resolved by a side or a conclusion of its own, with a reason\n", func() {
			expectRead()
			expectSave()
			_, err := svc.ResolveConflict(ctx, token, "p1", "k1", interfaces.RequirementResolve{UpdateTime: 7,
				DocumentID: "pol", Reason: "The policy governs"}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Conflicts[0].Resolution, ShouldResemble, &interfaces.RequirementResolution{
				DocumentID: "pol", Reason: "The policy governs", By: caller, Time: 100})
			So(saved.Spec.Conflicts[0].Deferred, ShouldBeFalse)

			expectRead()
			expectSave()
			_, err = svc.ResolveConflict(ctx, token, "p1", "k1", interfaces.RequirementResolve{UpdateTime: 7,
				Text: "Email only proposes", Reason: "Both are partly right"}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Conflicts[0].Resolution.Text, ShouldEqual, "Email only proposes")
		})

		Convey("A conflict handed to the business stays unresolved, with why\n", func() {
			expectRead()
			expectSave()

			_, err := svc.ResolveConflict(ctx, token, "p1", "k1", interfaces.RequirementResolve{UpdateTime: 7,
				Deferred: true, Reason: "Sales must confirm"}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.Conflicts[0].Deferred, ShouldBeTrue)
			So(saved.Spec.Conflicts[0].Resolution, ShouldResemble, &interfaces.RequirementResolution{
				Reason: "Sales must confirm", By: caller, Time: 100})
		})

		Convey("A resolution needs a reason, and a side of the conflict or a conclusion\n", func() {
			for _, resolve := range []interfaces.RequirementResolve{
				{UpdateTime: 7, DocumentID: "pol"},
				{UpdateTime: 7, Deferred: true},
				// Handing a conflict to the business is instead of resolving it, not besides.
				{UpdateTime: 7, Deferred: true, Reason: "ask", DocumentID: "pol"},
				{UpdateTime: 7, Deferred: true, Reason: "ask", Text: "email proposes"},
				{UpdateTime: 7, Reason: "r"},
				{UpdateTime: 7, DocumentID: "std", Reason: "r"},
			} {
				expectRead()
				_, err := svc.ResolveConflict(ctx, token, "p1", "k1", resolve, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidResolution)
			}
			expectRead()
			_, err := svc.ResolveConflict(ctx, token, "p1", "k9", interfaces.RequirementResolve{UpdateTime: 7,
				DocumentID: "pol", Reason: "r"}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("A choice is decided by one of its options\n", func() {
			expectRead()
			expectSave()
			_, err := svc.DecideChoice(ctx, token, "p1", "f1", interfaces.RequirementDecide{UpdateTime: 7,
				OptionID: "o2", Basis: "Changes are rare"}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Choices[0].Decision, ShouldResemble, &interfaces.RequirementChoiceDecision{
				OptionID: "o2", Basis: "Changes are rare", By: caller, Time: 100})

			expectRead()
			_, err = svc.DecideChoice(ctx, token, "p1", "f1", interfaces.RequirementDecide{UpdateTime: 7, OptionID: "o9"}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidOption)
		})
	})
}

func Test_QuestionInputEdits(t *testing.T) {
	Convey("A person's inputs of a question are checked as drafting checks them\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, now: func() int64 { return 100 }}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).AnyTimes()
		draft := testDraft()
		draft.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{
			{Name: "order_no", Type: "string"}, {Name: "order_date", Type: "date"}}
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()

		withInputs := func() *interfaces.Requirement {
			req := readySpec()
			req.Spec.Questions[0].Template = "Lines of {order}?"
			req.Spec.Questions[0].Inputs = &[]interfaces.RequirementInput{
				{Name: "order", ConceptID: "c1", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_RECORD}}
			return req
		}
		var saved *interfaces.Requirement
		expectRead := func() { ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(withInputs(), true, nil) }
		expectSave := func() {
			ra.EXPECT().UpdateRequirement(gomock.Any(), gomock.Any(), int64(7)).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement, _ int64) (bool, error) {
					saved = req
					return true, nil
				})
		}
		record := func(name, concept, property string) interfaces.RequirementInput {
			return interfaces.RequirementInput{Name: name, ConceptID: concept, Property: property,
				Role: interfaces.REQUIREMENT_INPUT_RECORD}
		}
		asOf := interfaces.RequirementInput{Name: "as_of", ConceptID: "c1", Property: "order_date",
			Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}

		Convey("Inputs set with their template stand, and a question set to none has none\n", func() {
			expectRead()
			expectSave()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Template: ptr("Lines of {order} in the 30 days before {as_of}?"),
					Inputs:         &[]interfaces.RequirementInput{record("order", "c1", "order_no"), asOf},
					RelativeWindow: ptr(true)}, caller)
			So(err, ShouldBeNil)
			q := saved.Spec.Questions[0]
			So(*q.Inputs, ShouldResemble, []interfaces.RequirementInput{record("order", "c1", "order_no"), asOf})
			So(q.RelativeWindow, ShouldBeTrue)
			So(q.Edited, ShouldBeTrue)

			expectRead()
			expectSave()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{}}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Questions[0].Inputs, ShouldNotBeNil)
			So(*saved.Spec.Questions[0].Inputs, ShouldBeEmpty)
		})

		Convey("Deleting a concept leaves a question that loses an input to it with inputs unknown\n", func() {
			expectRead()
			expectSave()
			_, err := svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, "c1", 7, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Questions[0].Inputs, ShouldBeNil)
			So(saved.Spec.Questions[1].Inputs, ShouldBeNil)

			// A question whose inputs are of other concepts keeps them.
			expectRead()
			expectSave()
			_, err = svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, "c2", 7, caller)
			So(err, ShouldBeNil)
			So(*saved.Spec.Questions[0].Inputs, ShouldHaveLength, 1)
		})

		Convey("Inputs that would not stand are refused, naming the input\n", func() {
			for _, c := range []struct {
				edit interfaces.RequirementItemEdit
				says string
			}{
				{interfaces.RequirementItemEdit{UpdateTime: 7, Template: ptr("Lines of a given order?")}, inputNotInTemplate},
				{interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{record("order", "c9", "order_no")}}, inputUnknownConcept},
				{interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{record("order", "c1", "amount")}}, inputUnknownProperty},
				{interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{record("order", "c2", "")}}, inputConceptNotMapped},
				{interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{record("order", "c1", "order_no"),
					record("order", "c1", "order_date")}}, inputDuplicateName},
				{interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{{Name: "order", ConceptID: "c1",
					Property: "order_no", Role: "kind"}}}, inputUnknownRole},
				{interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{{Name: "order", ConceptID: "c1",
					Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}}}, inputNotTime},
				{interfaces.RequirementItemEdit{UpdateTime: 7, RelativeWindow: ptr(true)}, rejectedWindowWithoutRefTime},
			} {
				expectRead()
				_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1", c.edit, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
				So(err.Error(), ShouldContainSubstring, c.says)
			}
		})

		Convey("A person's inputs are their own: a key the server took is no longer marked\n", func() {
			expectRead()
			expectSave()
			keyed := record("order", "c1", "order_no")
			keyed.PropertyFromKey = true
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{keyed}}, caller)
			So(err, ShouldBeNil)
			So(*saved.Spec.Questions[0].Inputs, ShouldResemble, []interfaces.RequirementInput{record("order", "c1", "order_no")})
		})

		Convey("A person's example stands only as a whole value the documents state, and the question cites where\n", func() {
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{
				doc("ex", interfaces.REQUIREMENT_DOC_PRD, "Lines of an order.", "For example, order SO-1002.")}, nil).Times(3)
			withExample := func(example string) *[]interfaces.RequirementInput {
				in := record("order", "c1", "order_no")
				in.Example = example
				return &[]interfaces.RequirementInput{in}
			}

			expectRead()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: withExample("SO-100")}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			So(err.Error(), ShouldContainSubstring, "input order: "+rejectedExampleNotInDocuments)

			expectRead()
			expectSave()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: withExample(" SO-1002")}, caller)
			So(err, ShouldBeNil)
			q := saved.Spec.Questions[0]
			So((*q.Inputs)[0].Example, ShouldEqual, "SO-1002")
			So(q.Citations[len(q.Citations)-1].Quote, ShouldEqual, "For example, order SO-1002.")

			// A reference time's example is dropped: a moment is taken from the data.
			expectRead()
			expectSave()
			asOfExample := asOf
			asOfExample.Example = "SO-1002"
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Template: ptr("Lines of {order} before {as_of}?"),
					Inputs: &[]interfaces.RequirementInput{record("order", "c1", "order_no"), asOfExample}}, caller)
			So(err, ShouldBeNil)
			So((*saved.Spec.Questions[0].Inputs)[1].Example, ShouldEqual, "")
		})

		Convey("A question added with an example is checked against the documents too\n", func() {
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{
				doc("ex", interfaces.REQUIREMENT_DOC_PRD, "For example, order SO-1002.")}, nil)
			expectRead()
			expectSave()
			in := record("order", "c1", "order_no")
			in.Example = "SO-1002"
			_, err := svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS,
				interfaces.RequirementItemEdit{UpdateTime: 7, Text: ptr("Lines of a given order?"), Template: ptr("Lines of {order}?"),
					Inputs: &[]interfaces.RequirementInput{in}}, caller)
			So(err, ShouldBeNil)
			added := saved.Spec.Questions[len(saved.Spec.Questions)-1]
			So((*added.Inputs)[0].Example, ShouldEqual, "SO-1002")
			So(added.Citations, ShouldHaveLength, 1)
		})

		Convey("Inputs without an example need no documents\n", func() {
			expectRead()
			expectSave()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Inputs: &[]interfaces.RequirementInput{record("order", "c1", "order_no")}}, caller)
			So(err, ShouldBeNil)
		})

		Convey("A drafted window without a reference time does not hold up an edit of the wording\n", func() {
			unbound := withInputs()
			unbound.Spec.Questions[0].RelativeWindow = true
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(unbound, true, nil)
			expectSave()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Template: ptr("Lines of order {order}?")}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Questions[0].Template, ShouldEqual, "Lines of order {order}?")
		})

		Convey("A question drafted before inputs takes a window or a template edit as before\n", func() {
			expectRead()
			expectSave()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q2",
				interfaces.RequirementItemEdit{UpdateTime: 7, Template: ptr("Whose orders?"), RelativeWindow: ptr(true)}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Questions[1].Inputs, ShouldBeNil)
		})
	})
}

// needsSpec is readySpec with kinetic needs: q1 rests on n1, n2 is of the concept c2.
func needsSpec() *interfaces.Requirement {
	req := readySpec()
	req.Spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
		{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate", SuggestedMetricType: "atomic",
			ObjectTypeIDs: []string{"sales_order", "customer"},
			Properties:    []interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "amount"}},
			ConceptIDs:    []string{"c1"}, ScenarioIDs: []string{"s1"}, Source: interfaces.LOGIC_NEED_FROM_ANALYSIS},
		{ID: "n2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check", ObjectTypeIDs: []string{},
			Properties: []interfaces.RequirementPropertyRef{}, ConceptIDs: []string{"c2", "c1"}, ScenarioIDs: []string{},
			Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS},
	}
	req.Spec.Questions[0].Layer = interfaces.REQUIREMENT_LAYER_CAPABILITY
	req.Spec.Questions[0].LogicNeedIDs = []string{"n1"}
	req.Spec.Questions[1].LogicNeedIDs = []string{}
	req.Spec.LastIDs = map[string]int{"n": 3}
	return req
}

func Test_LogicNeedEdits(t *testing.T) {
	Convey("Test editing kinetic needs and what rests on them\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, now: func() int64 { return 100 }}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(kineticDraft(), true, nil).AnyTimes()

		var saved *interfaces.Requirement
		expectRead := func() { ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(needsSpec(), true, nil) }
		expectSave := func() {
			ra.EXPECT().UpdateRequirement(gomock.Any(), gomock.Any(), int64(7)).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement, _ int64) (bool, error) {
					saved = req
					return true, nil
				})
		}

		Convey("An action type names the api or mcp_tool need that executes it, and loses it when that one goes\n", func() {
			withActions := func(executedBy string) *interfaces.Requirement {
				req := needsSpec()
				req.Spec.LogicNeeds = append(req.Spec.LogicNeeds,
					&interfaces.RequirementLogicNeed{ID: "n5", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Send reminder",
						ExecutedBy: executedBy},
					&interfaces.RequirementLogicNeed{ID: "n6", Kind: interfaces.LOGIC_NEED_API, Name: "Reminder service"},
					&interfaces.RequirementLogicNeed{ID: "n7", Kind: interfaces.LOGIC_NEED_MCP_TOOL, Name: "Mail tool"})
				req.Spec.LastIDs = map[string]int{"n": 7}
				return req
			}
			edit := func(from *interfaces.Requirement, id string, e interfaces.RequirementItemEdit) error {
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(from, true, nil)
				e.UpdateTime = 7
				_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, id, e, caller)
				return err
			}
			executor := func(id string) string {
				i, _ := find(saved.Spec.LogicNeeds, id, logicNeedID)
				return saved.Spec.LogicNeeds[i].ExecutedBy
			}

			for _, by := range []string{"n6", "n7"} {
				expectSave()
				So(edit(withActions(""), "n5", interfaces.RequirementItemEdit{ExecutedBy: ptr(by)}), ShouldBeNil)
				So(executor("n5"), ShouldEqual, by)
			}
			// "" says nothing executes it.
			expectSave()
			So(edit(withActions("n6"), "n5", interfaces.RequirementItemEdit{ExecutedBy: ptr("")}), ShouldBeNil)
			So(executor("n5"), ShouldEqual, "")

			// Only an api or mcp_tool need of the specification executes, and only an action type.
			for _, c := range []struct {
				id, by string
			}{{"n5", "n1"}, {"n5", "n5"}, {"n5", "n9"}, {"n1", "n6"}} {
				err := edit(withActions(""), c.id, interfaces.RequirementItemEdit{ExecutedBy: ptr(c.by)})
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			}

			// It goes when the need named is removed or becomes another kind, or the action type does.
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(withActions("n6"), true, nil)
			expectSave()
			_, err := svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n6", 7, caller)
			So(err, ShouldBeNil)
			So(executor("n5"), ShouldEqual, "")
			expectSave()
			So(edit(withActions("n6"), "n6", interfaces.RequirementItemEdit{LogicKind: ptr(interfaces.LOGIC_NEED_SKILL)}), ShouldBeNil)
			So(executor("n5"), ShouldEqual, "")
			expectSave()
			So(edit(withActions("n6"), "n5", interfaces.RequirementItemEdit{LogicKind: ptr(interfaces.LOGIC_NEED_FUNCTION)}), ShouldBeNil)
			So(executor("n5"), ShouldEqual, "")
			// An api made an mcp_tool still executes it.
			expectSave()
			So(edit(withActions("n6"), "n6", interfaces.RequirementItemEdit{LogicKind: ptr(interfaces.LOGIC_NEED_MCP_TOOL)}), ShouldBeNil)
			So(executor("n5"), ShouldEqual, "n6")
		})

		Convey("A person adds a need of a kind and name, on object types and properties of the draft\n", func() {
			expectRead()
			expectSave()

			_, err := svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, interfaces.RequirementItemEdit{UpdateTime: 7,
				LogicKind: ptr(interfaces.LOGIC_NEED_METRIC), Name: ptr(" Reply time "), Definition: ptr("hours to the first reply"),
				ObjectTypeIDs:       &[]string{"sales_order", "sales_order"},
				Properties:          &[]interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "agreed_on"}},
				SuggestedMetricType: ptr("derived")}, caller)

			So(err, ShouldBeNil)
			n := saved.Spec.LogicNeeds[2]
			// No id given before is given again.
			So(n.ID, ShouldEqual, "n4")
			So(n.Name, ShouldEqual, "Reply time")
			So(n.Source, ShouldEqual, interfaces.LOGIC_NEED_FROM_PERSON)
			So(n.Edited, ShouldBeTrue)
			So(n.Accepted, ShouldBeTrue)
			So(n.ObjectTypeIDs, ShouldResemble, []string{"sales_order"})
			So(n.Properties, ShouldResemble, []interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "agreed_on"}})
			So(n.SuggestedMetricType, ShouldEqual, "derived")
			So(n.Citations, ShouldResemble, []interfaces.RequirementCitation{})
		})

		Convey("Changing a linked need's kind to one implemented by another kind of thing drops its link\n", func() {
			linked := func() *interfaces.Requirement {
				spec := needsSpec()
				spec.Spec.LogicNeeds[0].Link = &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: "m1"}
				spec.Spec.LogicNeeds[1].Link = &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_FUNCTION, ID: "f1", BoxID: "b"}
				return spec
			}
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(linked(), true, nil)
			expectSave()

			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n1", interfaces.RequirementItemEdit{UpdateTime: 7,
				LogicKind: ptr(interfaces.LOGIC_NEED_SKILL)}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.LogicNeeds[0].Link, ShouldBeNil)

			// A function made an API is still implemented by a function: the link stays.
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(linked(), true, nil)
			expectSave()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n2", interfaces.RequirementItemEdit{UpdateTime: 7,
				LogicKind: ptr(interfaces.LOGIC_NEED_API)}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.LogicNeeds[1].Link, ShouldNotBeNil)
		})

		Convey("An edit changes the need's fields; properties go with the object types they belong to\n", func() {
			expectRead()
			expectSave()

			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n1", interfaces.RequirementItemEdit{UpdateTime: 7,
				LogicKind: ptr(interfaces.LOGIC_NEED_FUNCTION), ObjectTypeIDs: &[]string{"customer"}}, caller)

			So(err, ShouldBeNil)
			n := saved.Spec.LogicNeeds[0]
			So(n.Kind, ShouldEqual, interfaces.LOGIC_NEED_FUNCTION)
			So(n.ObjectTypeIDs, ShouldResemble, []string{"customer"})
			So(n.Properties, ShouldBeEmpty)
			// Only a metric suggests a metric type.
			So(n.SuggestedMetricType, ShouldEqual, "")
			So(n.Edited, ShouldBeTrue)
			So(n.Accepted, ShouldBeTrue)
		})

		Convey("A need rests on the model: an object type drafted but not adopted is refused, as runs leave it out\n", func() {
			expectRead()

			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n1", interfaces.RequirementItemEdit{UpdateTime: 7,
				ObjectTypeIDs: &[]string{"carrier"}}, caller)

			So(err, ShouldNotBeNil)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
		})

		Convey("A need's edit is refused when it names what is not, or sets another kind's field\n", func() {
			for _, edit := range []interfaces.RequirementItemEdit{
				{UpdateTime: 7, LogicKind: ptr("dashboard")},
				{UpdateTime: 7, Name: ptr(" ")},
				{UpdateTime: 7, ObjectTypeIDs: &[]string{"nope"}},
				{UpdateTime: 7, Properties: &[]interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "missing"}}},
				// A property of an object type the need is not on.
				{UpdateTime: 7, ObjectTypeIDs: &[]string{"customer"},
					Properties: &[]interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "amount"}}},
				{UpdateTime: 7, SuggestedMetricType: ptr("guess")},
				{UpdateTime: 7, Text: ptr("a question's")},
			} {
				expectRead()

				_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n1", edit, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			}
			for _, edit := range []interfaces.RequirementItemEdit{
				{UpdateTime: 7, Name: ptr("x")},
				{UpdateTime: 7, LogicKind: ptr(interfaces.LOGIC_NEED_API)},
			} {
				expectRead()

				_, err := svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, edit, caller)
				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
			}
			expectRead()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n9",
				interfaces.RequirementItemEdit{UpdateTime: 7, Name: ptr("x")}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("Setting the needs a question rests on sets its layer, unless the person sets that too\n", func() {
			expectRead()
			expectSave()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q2",
				interfaces.RequirementItemEdit{UpdateTime: 7, LogicNeedIDs: &[]string{"n2", "n2", "n1"}}, caller)
			So(err, ShouldBeNil)
			q2 := saved.Spec.Questions[1]
			So(q2.LogicNeedIDs, ShouldResemble, []string{"n2", "n1"})
			So(q2.Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			// The needs serve the scenarios of the questions resting on them.
			So(saved.Spec.LogicNeeds[1].ScenarioIDs, ShouldResemble, []string{"s1"})

			expectRead()
			expectSave()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, LogicNeedIDs: &[]string{}}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(saved.Spec.LogicNeeds[0].ScenarioIDs, ShouldResemble, []string{})

			expectRead()
			expectSave()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, LogicNeedIDs: &[]string{},
					Layer: ptr(interfaces.REQUIREMENT_LAYER_CAPABILITY)}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)

			expectRead()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1",
				interfaces.RequirementItemEdit{UpdateTime: 7, LogicNeedIDs: &[]string{"n9"}}, caller)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InvalidItem)
		})

		Convey("Deleting a need takes it from the questions resting on it, and its id is not given again\n", func() {
			expectRead()
			expectSave()

			_, err := svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n1", 7, caller)

			So(err, ShouldBeNil)
			So(ids(saved.Spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2"})
			So(saved.Spec.Questions[0].LogicNeedIDs, ShouldResemble, []string{})
			// Left resting on nothing, it is noted when read.
			So(questionNotes(saved.Spec.Questions[0]), ShouldResemble, []string{interfaces.QUESTION_NOTE_CAPABILITY_WITHOUT_BASIS})
			So(saved.Spec.LastIDs["n"], ShouldEqual, 3)

			expectRead()
			_, err = svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, "n9", 7, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("Deleting a concept takes it from the needs; deleting a question, its scenarios from the needs\n", func() {
			expectRead()
			expectSave()
			_, err := svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, "c2", 7, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.LogicNeeds[1].ConceptIDs, ShouldResemble, []string{"c1"})

			expectRead()
			expectSave()
			_, err = svc.DeleteItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_QUESTIONS, "q1", 7, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.LogicNeeds[0].ScenarioIDs, ShouldResemble, []string{})
		})

		Convey("Review accepts and unaccepts needs\n", func() {
			spec := needsSpec()
			spec.Spec.LogicNeeds[1].Accepted = true
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
			expectSave()

			_, err := svc.ReviewItems(ctx, token, "p1", interfaces.RequirementReview{UpdateTime: 7,
				Accept:   []interfaces.RequirementReviewItem{{Kind: interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, ID: "n1"}},
				Unaccept: []interfaces.RequirementReviewItem{{Kind: interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, ID: "n2"}}}, caller)

			So(err, ShouldBeNil)
			So(saved.Spec.LogicNeeds[0].Accepted, ShouldBeTrue)
			So(saved.Spec.LogicNeeds[1].Accepted, ShouldBeFalse)
			So(saved.Spec.LogicNeeds[0].Edited, ShouldBeFalse)

			expectRead()
			_, err = svc.ReviewItems(ctx, token, "p1", interfaces.RequirementReview{UpdateTime: 7,
				Accept: []interfaces.RequirementReviewItem{{Kind: interfaces.REQUIREMENT_KIND_LOGIC_NEEDS, ID: "n9"}}}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
		})

		Convey("A concept may be a kinetic need, and one a person adds is theirs\n", func() {
			expectRead()
			expectSave()
			_, err := svc.AddItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, interfaces.RequirementItemEdit{UpdateTime: 7,
				Term: ptr("Late rate"), Category: ptr(interfaces.REQUIREMENT_CONCEPT_KINETIC)}, caller)
			So(err, ShouldBeNil)
			c := saved.Spec.Concepts[2]
			So(c.Category, ShouldEqual, interfaces.REQUIREMENT_CONCEPT_KINETIC)
			So(c.Source, ShouldEqual, interfaces.REQUIREMENT_CONCEPT_FROM_PERSON)

			expectRead()
			expectSave()
			_, err = svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_CONCEPTS, "c2", interfaces.RequirementItemEdit{UpdateTime: 7,
				Category: ptr(interfaces.REQUIREMENT_CONCEPT_KINETIC)}, caller)
			So(err, ShouldBeNil)
			So(saved.Spec.Concepts[1].Category, ShouldEqual, interfaces.REQUIREMENT_CONCEPT_KINETIC)
		})

		Convey("No edit is made while a concepts or kinetic run goes, unless it went stale\n", func() {
			for _, kind := range []string{interfaces.REQUIREMENT_RUN_CONCEPTS, interfaces.REQUIREMENT_RUN_KINETIC} {
				going := needsSpec()
				going.Spec.Runs = map[string]*interfaces.RequirementRun{kind: {Status: interfaces.REQUIREMENT_RUN_RUNNING, StartTime: 90}}
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(going, true, nil)

				_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
					interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("x")}, caller)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
			}

			// A run recorded going since before the service could have finished it does not hold edits.
			stale := needsSpec()
			stale.Spec.Runs = map[string]*interfaces.RequirementRun{interfaces.REQUIREMENT_RUN_CONCEPTS: {
				Status: interfaces.REQUIREMENT_RUN_RUNNING, StartTime: 100 - 2*agentDeadline.Milliseconds() - time.Minute.Milliseconds() - 1}}
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(stale, true, nil)
			expectSave()
			_, err := svc.EditItem(ctx, token, "p1", interfaces.REQUIREMENT_KIND_RULES, "r1",
				interfaces.RequirementItemEdit{UpdateTime: 7, Statement: ptr("x")}, caller)
			So(err, ShouldBeNil)
		})
	})
}
