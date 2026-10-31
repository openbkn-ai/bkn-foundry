// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"net/http"
	"strings"
	"testing"

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
