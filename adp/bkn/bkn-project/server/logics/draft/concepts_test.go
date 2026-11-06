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

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

// conceptDraft is candidateDraft with a code table, a document order with its evidence, and the
// evidence references and occurrences drafting adds for it.
func conceptDraft() *interfaces.Draft {
	draft := candidateDraft()
	items := &draft.Items
	items.ValueTypes = []*interfaces.DraftValueType{{ID: "currency", Name: "Currency",
		CodeTable: interfaces.DraftTable{ResourceID: "r_currency", Name: "currency_codes"}}}
	evidence := func(id string, role string) *interfaces.DraftObjectType {
		return &interfaces.DraftObjectType{ID: id, Name: id, Document: &interfaces.DraftDocumentModel{Role: role, Layer: "evidence"}}
	}
	items.ObjectTypes = append(items.ObjectTypes,
		&interfaces.DraftObjectType{ID: "order_doc", Name: "Order", Document: &interfaces.DraftDocumentModel{Role: "entity", Layer: "entity",
			Citations: []interfaces.DraftSourceCitation{{FileID: "f1"}, {FileID: "f1"}, {FileID: "f2"}}}},
		evidence("source_block", "evidence_block"), evidence("source_image", "evidence_image"), evidence("source_media", "evidence_media"))
	link := func(id string, source string, target string, role string) *interfaces.DraftRelationType {
		return &interfaces.DraftRelationType{ID: id, Name: id, Source: source, Target: target,
			Document: &interfaces.DraftDocumentModel{Role: role, Layer: "evidence"}}
	}
	items.RelationTypes = append(items.RelationTypes,
		link("order_block", "order_doc", "source_block", evidenceReferenceRole),
		link("order_image", "order_doc", "source_image", evidenceReferenceRole),
		link("media_image", "source_media", "source_image", evidenceOccurrenceRole))
	return draft
}

func conceptRequirement() *interfaces.Requirement {
	req := readyRequirement()
	req.Spec.Concepts = []*interfaces.RequirementConcept{
		{ID: "k1", Term: "Store", Category: "object", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "stores"},
			Citations: []interfaces.RequirementCitation{{DocumentID: "d1"}, {DocumentID: "d1"}}},
		// Named like the visits, but for case and spaces.
		{ID: "k2", Term: " customer  visits ", Category: "object"},
		{ID: "k3", Term: "On-time delivery rate", Category: "object", Definition: "Share of orders delivered on time"},
		{ID: "k4", Term: "Audit log", Category: "not_ontology", NotOntologyReason: "Technical record"},
		// Evidence follows the business items: no concept of its own.
		{ID: "k5", Term: "Source page", Category: "evidence"},
	}
	return req
}

func conceptByKey(c *interfaces.DraftConcepts, key string) *interfaces.DraftConcept {
	return findConcept(c, key)
}

func Test_ConceptProjection(t *testing.T) {
	Convey("Test projecting a project's concepts\n", t, func() {
		ctx := context.Background()
		draft := conceptDraft()
		concepts := conceptsOf(ctx, "p1", draft, conceptRequirement(), true)

		Convey("One concept per item that is no evidence, then the requirement concepts no item is\n", func() {
			keys := []string{}
			for _, c := range concepts.Concepts {
				keys = append(keys, c.Key)
			}
			So(keys, ShouldHaveLength, 10)
			for _, gone := range []string{"object_types/source_block", "object_types/source_image", "object_types/source_media",
				"relation_types/order_block", "relation_types/media_image", "requirement_concepts/k1",
				"requirement_concepts/k2", "requirement_concepts/k5"} {
				So(keys, ShouldNotContain, gone)
			}
			So(concepts.DraftUpdateTime, ShouldEqual, readTime)
		})

		Convey("Each concept says where it was found\n", func() {
			stores := conceptByKey(concepts, "object_types/stores")
			So(stores.Sources, ShouldResemble, []interfaces.DraftConceptSource{
				{Kind: interfaces.CONCEPT_SOURCE_TABLE, ID: "r_stores", Name: "stores"},
				{Kind: interfaces.CONCEPT_SOURCE_REQUIREMENT, ID: "k1", Name: "Store", DocumentIDs: []string{"d1"}},
			})
			So(conceptByKey(concepts, "object_types/visits").Sources[1].ID, ShouldEqual, "k2")
			order := conceptByKey(concepts, "object_types/order_doc")
			So(order.Sources, ShouldResemble, []interfaces.DraftConceptSource{
				{Kind: interfaces.CONCEPT_SOURCE_DOCUMENT, ID: "f1"}, {Kind: interfaces.CONCEPT_SOURCE_DOCUMENT, ID: "f2"},
			})
			So(order.SuggestedBecause, ShouldEqual, "document_entity")
			// A relation without a table of its own is found in its source's table.
			So(conceptByKey(concepts, "relation_types/items_to_stores").Sources[0].ID, ShouldEqual, "r_items")
		})

		Convey("A code table is a value list, not an object\n", func() {
			currency := conceptByKey(concepts, "value_types/currency")
			So(currency.Suggested, ShouldEqual, interfaces.CONCEPT_VERDICT_VALUES)
			So(currency.SuggestedBecause, ShouldEqual, "code_table")
			So(currency.Sources[0].Name, ShouldEqual, "currency_codes")
		})

		Convey("What a person decided on the model is the concept's verdict; the rest wait\n", func() {
			stores := conceptByKey(concepts, "object_types/stores")
			So(stores.State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			So(stores.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_OBJECT)
			notes := conceptByKey(concepts, "object_types/notes")
			So(notes.State, ShouldEqual, interfaces.CONCEPT_STATE_JUDGED)
			So(notes.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_NONE)
			So(notes.Reason, ShouldEqual, "Free text, not a business object")
			// Adopted by drafting, reviewed by no one: still pending.
			items := conceptByKey(concepts, "object_types/items")
			So(items.State, ShouldEqual, interfaces.CONCEPT_STATE_PENDING)
			So(items.Item.Adopted, ShouldBeTrue)
		})

		Convey("Requirement concepts no item is keep the specification's category\n", func() {
			rate := conceptByKey(concepts, "requirement_concepts/k3")
			So(rate.Suggested, ShouldEqual, interfaces.CONCEPT_VERDICT_OBJECT)
			So(rate.Item, ShouldBeNil)
			So(rate.Description, ShouldEqual, "Share of orders delivered on time")
			log := conceptByKey(concepts, "requirement_concepts/k4")
			So(log.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_NONE)
			So(log.Reason, ShouldEqual, "Technical record")
		})

		Convey("Pending concepts come first, and the counts follow the states and verdicts\n", func() {
			So(concepts.Concepts[0].State, ShouldEqual, interfaces.CONCEPT_STATE_PENDING)
			So(concepts.Counts[interfaces.CONCEPT_STATE_IN_MODEL], ShouldEqual, 1)
			So(concepts.Counts[interfaces.CONCEPT_STATE_JUDGED], ShouldEqual, 1)
			So(concepts.Counts[interfaces.CONCEPT_STATE_PENDING], ShouldEqual, 8)
			So(concepts.Counts["verdict:"+interfaces.CONCEPT_VERDICT_VALUES], ShouldEqual, 1)
		})

		Convey("Without a ready draft only the requirement concepts are listed, and none can be judged\n", func() {
			drafting := &interfaces.Draft{ProjectID: "p1", Status: interfaces.DRAFT_STATUS_DRAFTING}
			got := conceptsOf(ctx, "p1", drafting, conceptRequirement(), true)
			So(got.DraftUpdateTime, ShouldEqual, 0)
			So(got.Concepts, ShouldHaveLength, 4)
			So(conceptsOf(ctx, "p1", nil, nil, false).Concepts, ShouldBeEmpty)
		})

		Convey("A judgment is the verdict until a person decides on the item on the model after it\n", func() {
			items := &draft.Items
			items.Decisions = append(items.Decisions,
				&interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS, ItemID: "object_types/items",
					Action: interfaces.DRAFT_DECISION_JUDGE, After: interfaces.CONCEPT_VERDICT_EVENT, By: reviewer, Time: 5})
			So(conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/items").Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_EVENT)

			findObjectType(items, "items").Adopted = false
			items.Decisions = append(items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_OBJECT_TYPES,
				ItemID: "items", Action: interfaces.DRAFT_DECISION_UNADOPT, Reason: "Duplicate", By: reviewer, Time: 6})
			got := conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/items")
			So(got.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_NONE)
			So(got.Reason, ShouldEqual, "Duplicate")
		})

		Convey("A judgment read back from the database holds its verdict as a string\n", func() {
			var after any = interfaces.CONCEPT_VERDICT_KINETIC
			draft.Items.Decisions = append(draft.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS,
				ItemID: "requirement_concepts/k3", Action: interfaces.DRAFT_DECISION_JUDGE, After: after, By: reviewer, Time: 7})
			rate := conceptByKey(conceptsOf(ctx, "p1", draft, conceptRequirement(), true), "requirement_concepts/k3")
			So(rate.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_KINETIC)
			So(rate.State, ShouldEqual, interfaces.CONCEPT_STATE_JUDGED)
		})

		Convey("A merged concept gives its sources to the one it went into, and comes back when taken out\n", func() {
			merge := func(action string, into any, time int64) {
				draft.Items.Decisions = append(draft.Items.Decisions, &interfaces.DraftDecision{Kind: interfaces.DRAFT_KIND_CONCEPTS,
					ItemID: "object_types/order_doc", Action: action, After: into, By: reviewer, Time: time})
			}
			merge(interfaces.DRAFT_DECISION_MERGE, "object_types/items", 8)
			got := conceptsOf(ctx, "p1", draft, nil, false)
			So(conceptByKey(got, "object_types/order_doc"), ShouldBeNil)
			items := conceptByKey(got, "object_types/items")
			So(items.Merged, ShouldResemble, []interfaces.DraftConceptRef{{Key: "object_types/order_doc", Name: "Order"}})
			So(items.Sources, ShouldHaveLength, 3)

			merge(interfaces.DRAFT_DECISION_UNMERGE, nil, 9)
			So(conceptByKey(conceptsOf(ctx, "p1", draft, nil, false), "object_types/order_doc"), ShouldNotBeNil)
		})
	})
}

func Test_FollowEvidence(t *testing.T) {
	Convey("Test evidence following its business items\n", t, func() {
		draft := conceptDraft()
		items := &draft.Items
		order := findObjectType(items, "order_doc")

		Convey("Adopting a business item adopts the evidence it references and what ties that evidence\n", func() {
			order.Adopted = true
			decisions := followEvidence(items)
			for _, id := range []string{"source_block", "source_image", "source_media"} {
				So(findObjectType(items, id).Adopted, ShouldBeTrue)
			}
			for _, id := range []string{"order_block", "order_image", "media_image"} {
				So(findRelationType(items, id).Adopted, ShouldBeTrue)
			}
			So(decisions, ShouldHaveLength, 6)
			So(decisions[0].Action, ShouldEqual, interfaces.DRAFT_DECISION_ADOPT)
			// Following again changes nothing.
			So(followEvidence(items), ShouldBeEmpty)

			Convey("and leaving it out leaves out the evidence no adopted business item needs\n", func() {
				order.Adopted = false
				So(followEvidence(items), ShouldHaveLength, 6)
				So(findObjectType(items, "source_media").Adopted, ShouldBeFalse)
				So(findRelationType(items, "order_block").Adopted, ShouldBeFalse)
			})
		})

		Convey("Evidence counts neither as unreviewed nor as undecided\n", func() {
			before := unreviewed(items)
			for _, u := range undecidedItems(items) {
				So(u.ItemID, ShouldNotStartWith, "source_")
				So(u.ItemID, ShouldNotEqual, "order_block")
			}
			So(before, ShouldEqual, unreviewed(&candidateDraft().Items)+1)
		})

		Convey("A draft without evidence is left alone\n", func() {
			So(followEvidence(&candidateDraft().Items), ShouldBeNil)
		})
	})
}

func Test_ConceptService(t *testing.T) {
	Convey("Test judging and merging concepts\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		rqa := pmock.NewMockRequirementAccess(ctrl)
		svc := &draftService{setting: testSetting, da: da, ps: ps, rqa: rqa}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
		rqa.EXPECT().GetRequirement(gomock.Any(), "p1").Return(conceptRequirement(), true, nil).AnyTimes()
		draft := candidateDraft()
		var written *interfaces.Draft
		write := func() {
			da.EXPECT().UpdateDraft(gomock.Any(), gomock.Any(), readTime).DoAndReturn(
				func(_ context.Context, d *interfaces.Draft, _ int64) (bool, error) {
					written = d
					return true, nil
				})
		}
		judge := func(key string, verdict string, reason string) (*interfaces.DraftConcepts, error) {
			return svc.JudgeConcept(ctx, token, "p1", interfaces.DraftConceptJudge{UpdateTime: readTime, Key: key,
				Verdict: verdict, Reason: reason}, caller)
		}
		codeOf := func(err error) (int, string) {
			var httpErr *httperrors.HTTPError
			So(errors.As(err, &httpErr), ShouldBeTrue)
			return httpErr.HTTPCode, httpErr.BaseError.ErrorCode
		}
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil).AnyTimes()

		Convey("Concepts are read without a draft too\n", func() {
			ctrl2 := gomock.NewController(t)
			none := pmock.NewMockDraftAccess(ctrl2)
			none.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			got, err := (&draftService{setting: testSetting, da: none, ps: ps, rqa: rqa}).GetConcepts(ctx, token, "p1")
			So(err, ShouldBeNil)
			So(got.DraftUpdateTime, ShouldEqual, 0)
			// With no item to be, every requirement concept stands on its own.
			So(got.Concepts, ShouldHaveLength, 4)
		})

		Convey("Judging an item as an object adopts it in the same write\n", func() {
			write()
			got, err := judge("object_types/items", interfaces.CONCEPT_VERDICT_EVENT, "Has a time and parties")
			So(err, ShouldBeNil)
			items := conceptByKey(got, "object_types/items")
			So(items.State, ShouldEqual, interfaces.CONCEPT_STATE_IN_MODEL)
			So(items.Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_EVENT)
			So(items.Reason, ShouldEqual, "Has a time and parties")
			So(items.Judged.By, ShouldResemble, caller)
			var judgment, adoption *interfaces.DraftDecision
			for _, d := range written.Items.Decisions {
				switch {
				case d.Kind == interfaces.DRAFT_KIND_CONCEPTS:
					judgment = d
				case d.ItemID == "items" && d.Action == interfaces.DRAFT_DECISION_ADOPT:
					adoption = d
				}
			}
			So(judgment.Before, ShouldEqual, interfaces.CONCEPT_VERDICT_OBJECT)
			// Adopting what drafting adopted is a person confirming it, in the same write.
			So(adoption.Time, ShouldEqual, judgment.Time)
		})

		Convey("A reason is trimmed, kept with the judgment, and goes only with leaving the item out\n", func() {
			write()
			got, err := judge("object_types/items", interfaces.CONCEPT_VERDICT_OBJECT, "  Sold in stores  ")
			So(err, ShouldBeNil)
			So(conceptByKey(got, "object_types/items").Reason, ShouldEqual, "Sold in stores")
			for _, d := range written.Items.Decisions {
				if d.Kind == interfaces.DRAFT_KIND_OBJECT_TYPES && d.ItemID == "items" {
					So(d.Reason, ShouldBeEmpty)
				}
			}
		})

		Convey("Judging it not included leaves it out, with the relations to it\n", func() {
			write()
			got, err := judge("object_types/items", interfaces.CONCEPT_VERDICT_NONE, "Duplicate of stores")
			So(err, ShouldBeNil)
			So(findObjectType(&written.Items, "items").Adopted, ShouldBeFalse)
			So(findRelationType(&written.Items, "items_to_stores").Adopted, ShouldBeFalse)
			So(conceptByKey(got, "object_types/items").State, ShouldEqual, interfaces.CONCEPT_STATE_JUDGED)
		})

		Convey("An item that cannot be adopted yet keeps the judgment and is blocked\n", func() {
			write()
			got, err := judge("object_types/visits", interfaces.CONCEPT_VERDICT_OBJECT, "")
			So(err, ShouldBeNil)
			visits := conceptByKey(got, "object_types/visits")
			So(visits.State, ShouldEqual, interfaces.CONCEPT_STATE_BLOCKED)
			So(visits.Blocked, ShouldEqual, interfaces.CONCEPT_BLOCKED_NEEDS_IDENTITY)
			So(findObjectType(&written.Items, "visits").Adopted, ShouldBeFalse)

			Convey("as is a relation whose end waits\n", func() {
				draft.UpdateTime = readTime
				write()
				got, err := judge("relation_types/visits_to_stores", interfaces.CONCEPT_VERDICT_RELATION, "")
				So(err, ShouldBeNil)
				So(conceptByKey(got, "relation_types/visits_to_stores").Blocked, ShouldEqual, interfaces.CONCEPT_BLOCKED_END_NOT_ADOPTED)
			})
		})

		Convey("A requirement concept is judged with no item to adopt\n", func() {
			write()
			got, err := judge("requirement_concepts/k3", interfaces.CONCEPT_VERDICT_KINETIC, "Computed from deliveries")
			So(err, ShouldBeNil)
			So(conceptByKey(got, "requirement_concepts/k3").Verdict, ShouldEqual, interfaces.CONCEPT_VERDICT_KINETIC)
			So(written.Items.Decisions[len(written.Items.Decisions)-1].ItemID, ShouldEqual, "requirement_concepts/k3")
		})

		Convey("An unknown verdict, a long reason or an unknown concept are refused\n", func() {
			_, err := judge("object_types/items", "metric", "")
			status, _ := codeOf(err)
			So(status, ShouldEqual, http.StatusBadRequest)

			long := make([]rune, conceptReasonMaxLength+1)
			for i := range long {
				long[i] = '因'
			}
			_, err = judge("object_types/items", interfaces.CONCEPT_VERDICT_NONE, string(long))
			status, _ = codeOf(err)
			So(status, ShouldEqual, http.StatusBadRequest)

			_, err = judge("object_types/nothing", interfaces.CONCEPT_VERDICT_NONE, "")
			status, _ = codeOf(err)
			So(status, ShouldEqual, http.StatusNotFound)
		})

		Convey("Judging on a draft changed since it was read is refused\n", func() {
			_, err := svc.JudgeConcept(ctx, token, "p1", interfaces.DraftConceptJudge{UpdateTime: readTime - 1,
				Key: "object_types/items", Verdict: interfaces.CONCEPT_VERDICT_NONE}, caller)
			status, _ := codeOf(err)
			So(status, ShouldEqual, http.StatusConflict)
		})

		Convey("Merging records the merge; the item merged keeps its adoption\n", func() {
			write()
			got, err := svc.MergeConcept(ctx, token, "p1", interfaces.DraftConceptMerge{UpdateTime: readTime,
				Key: "requirement_concepts/k3", Into: "object_types/stores"}, caller)
			So(err, ShouldBeNil)
			So(conceptByKey(got, "object_types/stores").Merged, ShouldResemble,
				[]interfaces.DraftConceptRef{{Key: "requirement_concepts/k3", Name: "On-time delivery rate"}})
			So(conceptByKey(got, "requirement_concepts/k3"), ShouldBeNil)

			Convey("and taking it out again brings it back\n", func() {
				draft.UpdateTime = readTime
				write()
				got, err := svc.MergeConcept(ctx, token, "p1", interfaces.DraftConceptMerge{UpdateTime: readTime,
					Key: "requirement_concepts/k3"}, caller)
				So(err, ShouldBeNil)
				So(conceptByKey(got, "requirement_concepts/k3"), ShouldNotBeNil)
			})
		})

		Convey("A concept is not merged into itself, nor into one merged away\n", func() {
			_, err := svc.MergeConcept(ctx, token, "p1", interfaces.DraftConceptMerge{UpdateTime: readTime,
				Key: "object_types/stores", Into: "object_types/stores"}, caller)
			status, _ := codeOf(err)
			So(status, ShouldEqual, http.StatusNotFound)

			_, err = svc.MergeConcept(ctx, token, "p1", interfaces.DraftConceptMerge{UpdateTime: readTime,
				Key: "object_types/stores"}, caller)
			status, _ = codeOf(err)
			So(status, ShouldEqual, http.StatusNotFound)
		})
	})
}
