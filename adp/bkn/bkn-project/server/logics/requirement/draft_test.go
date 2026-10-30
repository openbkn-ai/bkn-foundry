// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// The documents of the tests: a requirement document and a policy that disagree on one rule.
func testDocuments() []*interfaces.RequirementDocument {
	return []*interfaces.RequirementDocument{
		doc("prd", interfaces.REQUIREMENT_DOC_PRD,
			"Sales staff answer a customer's request for a new date and show where the current date comes from.",
			"A new date confirmed by email updates the agreed date directly."),
		doc("pol", interfaces.REQUIREMENT_DOC_POLICY,
			"Requests by email do not change the agreed date; only a signed change does.",
			"Sales answer every request within 2 working days."),
	}
}

func testDraft() *interfaces.Draft {
	return &interfaces.Draft{Items: interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			{ID: "sales_order", Name: "Sales order", Aliases: []string{"Order"}},
			{ID: "customer", Name: "Customer"},
		},
		RelationTypes: []*interfaces.DraftRelationType{{ID: "sales_order_customer", Name: "Order of customer",
			Source: "sales_order", Target: "customer"}},
	}}
}

func cite(docID, quote string) agentCitation { return agentCitation{DocumentID: docID, Quote: quote} }

func Test_BuildSpec(t *testing.T) {
	Convey("Test buildSpec\n", t, func() {
		docs := documentsByID(testDocuments())
		draft := testDraft()

		Convey("Items get the server's ids, and references between them follow\n", func() {
			out := &draftOutput{
				Scenarios: []agentScenario{{ID: "answer", Name: "Answer date requests",
					Citations: []agentCitation{cite("prd", "answer a customer's request for a new date")}}},
				Concepts: []agentConcept{
					{ID: "order", Term: "Order", Category: "object", Citations: []agentCitation{cite("prd", "the current date comes from")},
						MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}},
					{ID: "agreed", Term: "Agreed date", Category: "property", SupplementReason: "needed to answer"},
				},
				Questions: []agentQuestion{{ID: "q-a", Text: "Where does the current date come from?", ScenarioIDs: []string{"answer", "nope"},
					Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, ConceptIDs: []string{"order", "agreed", "order", "nope"},
					Citations: []agentCitation{cite("prd", "show where the current date comes from")}}},
			}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Scenarios[0].ID, ShouldEqual, "s1")
			So(spec.Concepts[0].ID, ShouldEqual, "c1")
			So(spec.Concepts[1].ID, ShouldEqual, "c2")
			q := spec.Questions[0]
			So(q.ID, ShouldEqual, "q1")
			So(q.ScenarioIDs, ShouldResemble, []string{"s1"})
			So(q.Basis.ConceptIDs, ShouldResemble, []string{"c1", "c2"})
			// Needs follow the concepts that map to drafted items, once each.
			So(q.Needs, ShouldResemble, []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}})
			So(q.NeedsSource, ShouldEqual, interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS)
			So(q.Citations[0].BlockID, ShouldEqual, "b1")
			So(spec.Rejected, ShouldBeEmpty)
		})

		Convey("Two concepts of one drafted item make one need\n", func() {
			ref := &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
			out := &draftOutput{
				Concepts: []agentConcept{
					{ID: "a", Term: "Order", SupplementReason: "r", MapsTo: ref},
					{ID: "b", Term: "Sales order", SupplementReason: "r", MapsTo: ref},
				},
				Questions: []agentQuestion{{Text: "t", ConceptIDs: []string{"a", "b"}, SupplementReason: "r"}},
			}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Questions[0].Basis.ConceptIDs, ShouldResemble, []string{"c1", "c2"})
			So(spec.Questions[0].Needs, ShouldResemble, []interfaces.RequirementItemRef{*ref})
		})

		Convey("A concept maps only to an item the draft has, of that kind\n", func() {
			out := &draftOutput{Concepts: []agentConcept{
				{ID: "a", Term: "Customer", SupplementReason: "r", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}},
				{ID: "b", Term: "Carrier", SupplementReason: "r", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "carrier"}},
				{ID: "c", Term: "Buyer", SupplementReason: "r", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "customer"}},
				{ID: "d", Term: "Belongs to", SupplementReason: "r", MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: "sales_order_customer"}},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Concepts[0].MapsTo, ShouldResemble, &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"})
			So(spec.Concepts[1].MapsTo, ShouldBeNil)
			So(spec.Concepts[2].MapsTo, ShouldBeNil)
			So(spec.Concepts[3].MapsTo.ID, ShouldEqual, "sales_order_customer")
		})

		Convey("Without a draft no concept maps to anything\n", func() {
			out := &draftOutput{Concepts: []agentConcept{{ID: "a", Term: "Customer", SupplementReason: "r",
				MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}}}}

			spec, _ := buildSpec(out, docs, nil)

			So(spec.Concepts[0].MapsTo, ShouldBeNil)
		})

		Convey("Items whose quotes are not found, or with no basis at all, are rejected with why\n", func() {
			out := &draftOutput{
				Scenarios: []agentScenario{
					{Name: "Invented", Citations: []agentCitation{cite("prd", "something no document says")}},
					{Name: "No basis"},
					{Name: "Unknown document", Citations: []agentCitation{cite("gone", "answer a customer's request")}},
				},
				Questions: []agentQuestion{{Text: "Reworded?", Citations: []agentCitation{cite("pol", "Email requests never change dates")},
					SupplementReason: "a supplement does not save a quote not found"}},
			}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Scenarios, ShouldBeEmpty)
			So(spec.Questions, ShouldBeEmpty)
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedScenario, Text: "Invented", Reason: rejectedQuoteNotFound},
				{Kind: rejectedScenario, Text: "No basis", Reason: rejectedQuoteNotFound},
				{Kind: rejectedScenario, Text: "Unknown document", Reason: rejectedUnknownDocument},
				{Kind: rejectedQuestion, Text: "Reworded?", Reason: rejectedQuoteNotFound},
			})
		})

		Convey("A rule needs a quote, and its numbers must be in it\n", func() {
			out := &draftOutput{Rules: []agentRule{
				{Statement: "Answer within 2 working days", CompilesTo: "metric_definition",
					Citations: []agentCitation{cite("pol", "within 2 working days")}},
				{Statement: "Answer within 3 working days", Citations: []agentCitation{cite("pol", "within 2 working days")}},
				{Statement: "Only signed changes count"},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Rules, ShouldHaveLength, 1)
			So(spec.Rules[0].ID, ShouldEqual, "r1")
			So(spec.Rules[0].CompilesTo, ShouldEqual, "metric_definition")
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedRule, Text: "Answer within 3 working days", Reason: rejectedNumberNotInQuote},
				{Kind: rejectedRule, Text: "Only signed changes count", Reason: rejectedQuoteNotFound},
			})
		})

		Convey("A question's numbers must be in its quotes too, unless it has none\n", func() {
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "Which requests wait over 5 days?", Citations: []agentCitation{cite("pol", "within 2 working days")}},
				{Text: "Which requests wait over 5 days?", SupplementReason: "asked by the business"},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Questions, ShouldHaveLength, 1)
			So(spec.Questions[0].SupplementReason, ShouldEqual, "asked by the business")
			So(spec.Rejected[0].Reason, ShouldEqual, rejectedNumberNotInQuote)
		})

		Convey("A question's inputs are kept only when they stand; the others are listed rejected\n", func() {
			withProps := testDraft()
			withProps.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{
				{Name: "order_no", Type: "string"}, {Name: "signed_on", Type: "date"}}
			withProps.Items.ObjectTypes[0].PrimaryKeys = []string{"order_no"}
			orderRef := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
			record := func(name, concept, property string) agentInput {
				return agentInput{Name: name, ConceptID: concept, Property: property, Role: interfaces.REQUIREMENT_INPUT_RECORD}
			}
			out := &draftOutput{
				Concepts: []agentConcept{{ID: "ord", Term: "Order", SupplementReason: "r", MapsTo: &orderRef},
					{ID: "loose", Term: "Channel", SupplementReason: "r"}},
				Questions: []agentQuestion{
					{Text: "a", Template: "Lines of order {order} by {missing} as {kind}?", SupplementReason: "r", Inputs: []agentInput{
						record("order", "ord", "order_no"),
						record("order", "ord", "order_no"),                                    // the same name again
						record("nobody", "ghost", "order_no"),                                 // no such concept
						record("missing", "ord", "nope"),                                      // no such property: the key
						record("absent", "ord", "order_no"),                                   // not in the template
						{Name: "kind", ConceptID: "ord", Property: "order_no", Role: "guess"}, // no such role
					}},
					// A concept that maps to no object type of the draft names no field.
					{Text: "b", Template: "Orders of {channel}?", SupplementReason: "r", Inputs: []agentInput{
						record("channel", "loose", "anything")}},
					// No input value at all.
					{Text: "c", Template: "Which orders are late?", SupplementReason: "r", Inputs: []agentInput{}},
					// Given inputs, none standing: not known, as before questions had inputs.
					{Text: "d", Template: "Lines of an order?", SupplementReason: "r", Inputs: []agentInput{record("order", "ord", "order_no")}},
					// A period counted back: its reference time is an input on a time property.
					{Text: "e", Template: "Orders of {order} in the 30 days before {as_of}?", SupplementReason: "r", RelativeWindow: true,
						Inputs: []agentInput{record("order", "ord", "order_no"),
							{Name: "as_of", ConceptID: "ord", Property: "signed_on", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}}},
					// Without one it depends on when it is asked: the question stays, noted when read.
					{Text: "f", Template: "Orders of {order} in the last 30 days?", SupplementReason: "r", RelativeWindow: true,
						Inputs: []agentInput{record("order", "ord", "order_no")}},
					// A reference time on a property that is no time: rejected, and the question stays.
					{Text: "g", Template: "Orders before {as_of}?", SupplementReason: "r", RelativeWindow: true,
						Inputs: []agentInput{{Name: "as_of", ConceptID: "ord", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}}},
					// The same, with a reference time on it.
					{Text: "h", Template: "Sales of {channel} before {as_of}?", SupplementReason: "r", Inputs: []agentInput{
						record("channel", "loose", "anything"),
						{Name: "as_of", ConceptID: "loose", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}}},
					// A record named by no property: the key names it.
					{Text: "i", Template: "Lines of order {order}?", SupplementReason: "r", Inputs: []agentInput{record("order", "ord", "")}},
					// A reference time is a time, never the key.
					{Text: "j", Template: "Orders before {as_of}?", SupplementReason: "r", Inputs: []agentInput{
						{Name: "as_of", ConceptID: "ord", Property: "nope", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}}},
				},
			}

			spec, _ := buildSpec(out, docs, withProps)

			byText := map[string]*interfaces.RequirementQuestion{}
			for _, q := range spec.Questions {
				byText[q.Text] = q
			}
			So(*byText["a"].Inputs, ShouldResemble, []interfaces.RequirementInput{
				{Name: "order", ConceptID: "c1", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_RECORD},
				{Name: "missing", ConceptID: "c1", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_RECORD, PropertyFromKey: true}})
			So(*byText["i"].Inputs, ShouldResemble, []interfaces.RequirementInput{
				{Name: "order", ConceptID: "c1", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_RECORD, PropertyFromKey: true}})
			So(byText["b"].Inputs, ShouldBeNil)
			So(*byText["c"].Inputs, ShouldBeEmpty)
			So(byText["d"].Inputs, ShouldBeNil)
			So(*byText["e"].Inputs, ShouldHaveLength, 2)
			So(byText["e"].RelativeWindow, ShouldBeTrue)
			So(byText["f"].RelativeWindow, ShouldBeTrue)
			So(*byText["f"].Inputs, ShouldHaveLength, 1)
			So(byText["g"].RelativeWindow, ShouldBeTrue)
			So(byText["g"].Inputs, ShouldBeNil)
			So(byText["h"].Inputs, ShouldBeNil)
			So(spec.Questions, ShouldHaveLength, 10)
			reasons := map[string]string{}
			for _, r := range spec.Rejected {
				reasons[r.Kind+" "+r.Text] = r.Reason
			}
			So(reasons["input order.order_no (a)"], ShouldEqual, inputDuplicateName)
			So(reasons["input kind.order_no (a)"], ShouldEqual, inputUnknownRole)
			So(reasons["input nobody.order_no (a)"], ShouldEqual, inputUnknownConcept)
			So(reasons["input absent.order_no (a)"], ShouldEqual, inputNotInTemplate)
			So(reasons["input order.order_no (d)"], ShouldEqual, inputNotInTemplate)
			So(reasons["input reference_time (f)"], ShouldEqual, rejectedWindowWithoutRefTime)
			So(reasons["input as_of.order_no (g)"], ShouldEqual, inputNotTime)
			So(reasons["input channel.anything (b)"], ShouldEqual, inputConceptNotMapped)
			So(reasons["input as_of (h)"], ShouldEqual, inputConceptNotMapped)
			So(reasons["input as_of.nope (j)"], ShouldEqual, inputUnknownProperty)
			// No question is dropped for its inputs: a rejected reference time is the input's alone.
			So(reasons, ShouldHaveLength, 11)

			// A key of several fields names no record alone: the property stays unknown.
			withProps.Items.ObjectTypes[0].PrimaryKeys = []string{"order_no", "signed_on"}
			spec, _ = buildSpec(out, docs, withProps)
			reasons = map[string]string{}
			for _, r := range spec.Rejected {
				reasons[r.Kind+" "+r.Text] = r.Reason
			}
			So(reasons["input missing.nope (a)"], ShouldEqual, inputUnknownProperty)
			So(reasons["input order (i)"], ShouldEqual, inputUnknownProperty)
			// Nor does the key stand in for a reference time.
			So(reasons["input as_of.order_no (g)"], ShouldEqual, inputNotTime)

			// Before the project has a draft, a concept maps to nothing yet: its property cannot be
			// checked and stands, but a reference time with no property cannot be told a time.
			spec, _ = buildSpec(out, docs, nil)
			byText, reasons = map[string]*interfaces.RequirementQuestion{}, map[string]string{}
			for _, q := range spec.Questions {
				byText[q.Text] = q
			}
			for _, r := range spec.Rejected {
				reasons[r.Kind+" "+r.Text] = r.Reason
			}
			So(*byText["b"].Inputs, ShouldResemble, []interfaces.RequirementInput{
				{Name: "channel", ConceptID: "c2", Property: "anything", Role: interfaces.REQUIREMENT_INPUT_RECORD}})
			So(reasons["input as_of (h)"], ShouldEqual, inputNotTime)
			So(*byText["h"].Inputs, ShouldHaveLength, 1)
			So(reasons["input missing.nope (a)"], ShouldEqual, "")
		})

		Convey("A question's layer is knowledge only when said so\n", func() {
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "a", Layer: "knowledge", SupplementReason: "r"},
				{Text: "b", Layer: "capability", SupplementReason: "r"},
				{Text: "c", Layer: "whatever", SupplementReason: "r"},
				// Said knowledge, but resting on a metric, a function or a skill: more than the ontology.
				{Text: "d", Layer: "knowledge", SupplementReason: "r", Metrics: []string{"amount over threshold"}},
				{Text: "e", Layer: "knowledge", SupplementReason: "r", Functions: []string{"approval check"}},
				{Text: "f", Layer: "knowledge", SupplementReason: "r", Skills: []string{"Signed-Change"}},
				// Blank names are none: still knowledge, and not kept.
				{Text: "g", Layer: "knowledge", SupplementReason: "r", Metrics: []string{"", " "}, Functions: []string{" "}},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(spec.Questions[1].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			So(spec.Questions[2].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			for _, q := range spec.Questions[3:6] {
				So(q.Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			}
			So(spec.Questions[6].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(spec.Questions[6].Basis.Metrics, ShouldResemble, []string{})
			So(spec.Questions[3].Basis.Metrics, ShouldResemble, []string{"amount over threshold"})
		})

		Convey("A skill stands in a question's basis only when the documents name it\n", func() {
			out := &draftOutput{
				// A skill the Agent designed for a scenario in the same pass names nothing the documents ask for.
				Scenarios: []agentScenario{{Name: "Answer", SupplementReason: "r", Capabilities: []interfaces.RequirementCapability{
					{Kind: "skill", Name: "Date lookup Skill"}}}},
				Questions: []agentQuestion{
					// A lookup the Agent rests on a serving skill of its own: knowledge after all.
					{Text: "a", Layer: "capability", SupplementReason: "r", Skills: []string{"Date lookup Skill", " ", "__"}},
					// Named by the documents, compared without case, spaces, underscores or hyphens.
					{Text: "b", Layer: "knowledge", SupplementReason: "r", Skills: []string{"signed_change", "Date lookup Skill"}},
					// Metrics and functions are named by the Agent: they stand as given.
					{Text: "c", Layer: "knowledge", SupplementReason: "r", Functions: []string{"approval check"}, Skills: []string{"Date lookup Skill"}},
				},
			}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(spec.Questions[0].Basis.Skills, ShouldResemble, []string{})
			So(spec.Questions[1].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			So(spec.Questions[1].Basis.Skills, ShouldResemble, []string{"signed_change"})
			So(spec.Questions[2].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			So(spec.Questions[2].Basis.Functions, ShouldResemble, []string{"approval check"})
			So(spec.Scenarios[0].Capabilities, ShouldHaveLength, 1)
			rejected := []interfaces.RequirementRejected{}
			for _, r := range spec.Rejected {
				rejected = append(rejected, *r)
			}
			So(rejected, ShouldResemble, []interfaces.RequirementRejected{
				{Kind: rejectedBasis, Text: "Date lookup Skill (a)", Reason: rejectedBasisNotInDocuments},
				{Kind: rejectedBasis, Text: "__ (a)", Reason: rejectedBasisNotInDocuments},
				{Kind: rejectedBasis, Text: "Date lookup Skill (b)", Reason: rejectedBasisNotInDocuments},
				{Kind: rejectedBasis, Text: "Date lookup Skill (c)", Reason: rejectedBasisNotInDocuments}})
		})

		Convey("A filter backed by functions alone is a filter on properties: knowledge, its functions rejected\n", func() {
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "a", Type: "filter", Layer: "capability", SupplementReason: "r", Functions: []string{"window check", "status check"}},
				// A metric, a skill the documents name, or another type keeps what it rests on.
				{Text: "b", Type: "filter", Layer: "capability", SupplementReason: "r", Metrics: []string{"late count"}, Functions: []string{"window check"}},
				{Text: "c", Type: "filter", Layer: "capability", SupplementReason: "r", Functions: []string{"window check"}, Skills: []string{"signed change"}},
				{Text: "d", Type: "judge", Layer: "capability", SupplementReason: "r", Functions: []string{"approval check"}},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(spec.Questions[0].Basis.Functions, ShouldResemble, []string{})
			for i, fns := range [][]string{{"window check"}, {"window check"}, {"approval check"}} {
				So(spec.Questions[i+1].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
				So(spec.Questions[i+1].Basis.Functions, ShouldResemble, fns)
			}
			rejected := []interfaces.RequirementRejected{}
			for _, r := range spec.Rejected {
				rejected = append(rejected, *r)
			}
			So(rejected, ShouldResemble, []interfaces.RequirementRejected{
				{Kind: rejectedBasis, Text: "window check (a)", Reason: rejectedFunctionOnFilter},
				{Kind: rejectedBasis, Text: "status check (a)", Reason: rejectedFunctionOnFilter}})
		})

		Convey("A conflict keeps the sides found, needs two documents, and gets the authority's suggestion\n", func() {
			out := &draftOutput{Conflicts: []agentConflict{
				{Topic: "Email confirmation", Kind: conflictRule, Sides: []agentConflictSide{
					{DocumentID: "prd", Quote: "confirmed by email updates the agreed date", Position: "email is enough"},
					{DocumentID: "pol", Quote: "Requests by email do not change the agreed date", Position: "signed only"},
					{DocumentID: "pol", Quote: "not in the policy", Position: "dropped"},
				}},
				{Topic: "One-sided", Kind: conflictRule, Sides: []agentConflictSide{
					{DocumentID: "prd", Quote: "confirmed by email updates the agreed date"},
					{DocumentID: "pol", Quote: "not in the policy"},
				}},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Conflicts, ShouldHaveLength, 1)
			k := spec.Conflicts[0]
			So(k.ID, ShouldEqual, "k1")
			So(k.Sides, ShouldHaveLength, 2)
			So(k.Sides[1].Position, ShouldEqual, "signed only")
			So(k.Sides[1].BlockID, ShouldEqual, "b1")
			So(k.SuggestedDocumentID, ShouldEqual, "pol")
			So(k.Resolution, ShouldBeNil)
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedConflict, Text: "One-sided", Reason: rejectedQuoteNotFound},
			})
		})

		Convey("A choice needs a quote and two options; a recommendation outside them is dropped\n", func() {
			options := []agentOption{{ID: "a", Label: "As an event"}, {ID: "b", Label: "As a property"}}
			out := &draftOutput{Choices: []agentChoice{
				{Question: "Model date changes?", Options: options, RecommendedOptionID: "a",
					Citations: []agentCitation{cite("pol", "only a signed change does")}},
				{Question: "Odd recommendation?", Options: options, RecommendedOptionID: "z",
					Citations: []agentCitation{cite("pol", "only a signed change does")}},
				{Question: "One option?", Options: options[:1], Citations: []agentCitation{cite("pol", "only a signed change does")}},
				{Question: "No quote?", Options: options},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Choices, ShouldHaveLength, 2)
			So(spec.Choices[0].ID, ShouldEqual, "f1")
			So(spec.Choices[0].Options[0].ID, ShouldEqual, "o1")
			So(spec.Choices[0].RecommendedOptionID, ShouldEqual, "o1")
			So(spec.Choices[1].RecommendedOptionID, ShouldEqual, "")
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedChoice, Text: "One option?", Reason: rejectedTooFewOptions},
				{Kind: rejectedChoice, Text: "No quote?", Reason: rejectedQuoteNotFound},
			})
		})

		Convey("Document types the agent suggests are kept only for known documents and types\n", func() {
			out := &draftOutput{DocTypes: []agentDocType{
				{DocumentID: "prd", DocType: interfaces.REQUIREMENT_DOC_PRD},
				{DocumentID: "pol", DocType: "manual"},
				{DocumentID: "gone", DocType: interfaces.REQUIREMENT_DOC_POLICY},
			}}

			_, types := buildSpec(out, docs, draft)

			So(types, ShouldResemble, map[string]string{"prd": interfaces.REQUIREMENT_DOC_PRD})
		})
	})
}

func Test_BuildDraftMessage(t *testing.T) {
	Convey("Test buildDraftMessage\n", t, func() {
		Convey("At most maxMessageTables tables go to the Agent, the first ones listed, and the rest are counted\n", func() {
			tables := make([]*interfaces.VegaTable, maxMessageTables+5)
			for i := range tables {
				tables[i] = &interfaces.VegaTable{Name: fmt.Sprintf("t%d", i)}
			}

			message, omitted, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_ZH_CN, nil, tables, nil)

			So(err, ShouldBeNil)
			So(omitted, ShouldEqual, 5)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.Tables, ShouldHaveLength, maxMessageTables)
			So(body.Tables[0].Name, ShouldEqual, "t0")
		})

		Convey("A drafted object type goes with its properties and primary keys, so an input can name one\n", func() {
			draft := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{
				ID: "ot1", Name: "Item", PrimaryKeys: []string{"code"},
				Properties: []*interfaces.DraftProperty{{Name: "code", DisplayName: "Code", Type: "string"}, {Name: "made", Type: "date"}},
			}}}}

			message, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, draft)

			So(err, ShouldBeNil)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.ObjectTypes, ShouldHaveLength, 1)
			So(body.ObjectTypes[0].PrimaryKeys, ShouldResemble, []string{"code"})
			So(body.ObjectTypes[0].Properties, ShouldResemble,
				[]messageProperty{{Name: "code", DisplayName: "Code", Type: "string"}, {Name: "made", Type: "date"}})
		})
	})
}

func Test_RequirementDrafting(t *testing.T) {
	Convey("Test requirement drafting\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		// The clock moves only as drafting waits.
		slept, clock := 0, int64(1000)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return clock }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(d time.Duration) { slept++; clock += d.Milliseconds() }}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller,
			CatalogIDs: []string{"c2", "c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}}
		docs := testDocuments()
		docs[1].DocType = interfaces.REQUIREMENT_DOC_OTHER
		for _, d := range docs {
			d.SHA256 = "h-" + d.ID
		}

		expectStart := func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), int64(1000)-agentDeadline.Milliseconds()-time.Minute.Milliseconds(), false).
				DoAndReturn(func(_ context.Context, req *interfaces.Requirement, _ int64, _ bool) (bool, error) {
					So(req.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_DRAFTING)
					So(req.Version, ShouldEqual, 1)
					So(req.RunID, ShouldEqual, "run1")
					So(req.Drafter, ShouldResemble, caller)
					return true, nil
				})
		}
		expectContext := func() {
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return([]*interfaces.VegaTable{{Name: "orders", Description: "Sales orders"}}, nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(nil, errors.New("no access"))
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(testDraft(), true, nil)
		}
		var finished *interfaces.Requirement
		expectFinish := func() {
			ra.EXPECT().FinishDrafting(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement) (bool, error) {
					finished = req
					return true, nil
				})
		}
		output := `{"scenarios":[{"id":"s","name":"Answer date requests","citations":[{"document_id":"prd","quote":"answer a customer's request for a new date"}]}],
			"concepts":[],"questions":[],"rules":[{"statement":"Answer within 2 working days","compiles_to":"metric_definition","citations":[{"document_id":"pol","quote":"within 2 working days"}]}],
			"conflicts":[],"choices":[],"doc_types":[{"document_id":"pol","doc_type":"policy"},{"document_id":"prd","doc_type":"standard"}]}`

		Convey("Drafting runs the Agent as the caller and records the checked specification\n", func() {
			expectStart()
			expectContext()
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					So(req.AgentID, ShouldEqual, requirementAgentID)
					So(req.ResponseFormat["type"], ShouldEqual, "object")
					So(req.Message, ShouldStartWith, "pass draft\nlanguage: en-US\n")
					So(req.Message, ShouldContainSubstring, `"id":"b2","text":"A new date confirmed by email updates the agreed date directly."`)
					So(req.Message, ShouldContainSubstring, `"name":"orders","description":"Sales orders"`)
					So(req.Message, ShouldContainSubstring, `{"id":"sales_order","name":"Sales order","aliases":["Order"],"properties":[]}`)
					So(req.Message, ShouldContainSubstring, `{"id":"sales_order_customer","name":"Order of customer","source":"sales_order","target":"customer"}`)
					return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_PENDING}, nil
				})
			ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil)
			ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(nil, errors.New("blip"))
			ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil)
			// Only a document still typed other takes the suggested type.
			ra.EXPECT().SuggestDocumentType(gomock.Any(), "p1", "pol", interfaces.REQUIREMENT_DOC_POLICY, gomock.Any()).Return(true, nil)
			expectFinish()

			req, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(req.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_DRAFTING)
			So(slept, ShouldEqual, 3)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(finished.RunID, ShouldEqual, "run1")
			So(finished.AgentTaskID, ShouldEqual, "t1")
			So(finished.Spec.Scenarios[0].ID, ShouldEqual, "s1")
			So(finished.Spec.TablesOmitted, ShouldEqual, 0)
			// A catalog whose tables could not be listed is named, so no one takes its tables as seen.
			So(finished.Spec.CatalogsUnlisted, ShouldResemble, []string{"c1"})
			So(finished.Spec.Rules[0].Citations[0].DocumentID, ShouldEqual, "pol")
			So(finished.Inputs, ShouldResemble, &interfaces.RequirementInputs{
				RulesVersion: requirementRulesVersion,
				Documents: []interfaces.RequirementInputDocument{
					{ID: "pol", SHA256: "h-pol", DocType: interfaces.REQUIREMENT_DOC_POLICY},
					{ID: "prd", SHA256: "h-prd", DocType: interfaces.REQUIREMENT_DOC_PRD},
				},
				CatalogIDs: []string{"c1", "c2"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US,
			})
		})

		Convey("A type the owner set while drafting ran is kept, and the specification records what it read\n", func() {
			expectStart()
			expectContext()
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil)
			// The owner typed pol meanwhile: the conditional write changes nothing.
			ra.EXPECT().SuggestDocumentType(gomock.Any(), "p1", "pol", interfaces.REQUIREMENT_DOC_POLICY, gomock.Any()).Return(false, nil)
			expectFinish()

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			// Drafting read pol as other: once its type differs, the specification reads as stale.
			So(finished.Inputs.Documents[0], ShouldResemble, interfaces.RequirementInputDocument{
				ID: "pol", SHA256: "h-pol", DocType: interfaces.REQUIREMENT_DOC_OTHER})
		})

		Convey("Drafting ends at its deadline even when reads of the task never come back\n", func() {
			previous := taskReadTimeout
			taskReadTimeout = 5 * time.Millisecond
			defer func() { taskReadTimeout = previous }()
			expectStart()
			expectContext()
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil)
			reads := 0
			ba.EXPECT().GetTask(gomock.Any(), caller, "t1").DoAndReturn(
				func(readCtx context.Context, _ interfaces.BknAgentAccount, _ string) (*interfaces.BknAgentTask, error) {
					reads++
					<-readCtx.Done()
					return nil, readCtx.Err()
				}).AnyTimes()
			expectFinish()

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_FAILED)
			So(finished.Error, ShouldEqual, failureAgentTimeout)
			So(reads, ShouldBeLessThanOrEqualTo, int(agentDeadline/pollInterval))
			So(clock-1000, ShouldBeGreaterThanOrEqualTo, agentDeadline.Milliseconds())
		})

		Convey("Tables beyond what one drafting names are counted in the specification\n", func() {
			expectStart()
			many := make([]*interfaces.VegaTable, maxMessageTables+3)
			for i := range many {
				many[i] = &interfaces.VegaTable{Name: fmt.Sprintf("t%d", i)}
			}
			va.EXPECT().ListTables(gomock.Any(), caller, "c2").Return(many, nil)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return(nil, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{}`}, nil)
			expectFinish()

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(finished.Spec.TablesOmitted, ShouldEqual, 3)
		})

		Convey("Each way the Agent fails is recorded as its code, with an empty specification\n", func() {
			for _, c := range []struct {
				name   string
				expect func()
				code   string
			}{
				// bkn-agent refused the request itself: an Agent missing, a response format it does not
				// take, no account. These are no outage, and are told apart from one.
				{"Agent missing", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
						Return(nil, &interfaces.BknAgentError{HTTPCode: http.StatusNotFound, Code: "BknAgent.NotFound"})
				}, failureAgentRejected},
				{"request refused", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
						Return(nil, &interfaces.BknAgentError{HTTPCode: http.StatusBadRequest, Code: "BknAgent.ParamError.FormatError"})
				}, failureAgentRejected},
				{"no account", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
						Return(nil, &interfaces.BknAgentError{HTTPCode: http.StatusUnauthorized, Code: "BknAgent.Auth.AccountRequired"})
				}, failureAgentRejected},
				// bkn-agent could not be reached, or failed in itself.
				{"bkn-agent down", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
						Return(nil, &interfaces.BknAgentError{HTTPCode: http.StatusServiceUnavailable})
				}, failureAgentUnavailable},
				{"redirected away", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
						Return(nil, &interfaces.BknAgentError{HTTPCode: http.StatusFound})
				}, failureAgentUnavailable},
				{"no connection", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("connection refused"))
				}, failureAgentUnavailable},
				{"task failed", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil)
					ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1",
						Status: interfaces.BKN_AGENT_TASK_FAILED, FailureDetail: "TimeoutError"}, nil)
				}, failureAgentFailed},
				{"never finishes", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil)
					ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1",
						Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil).Times(int(agentDeadline / pollInterval))
				}, failureAgentTimeout},
				{"output not JSON", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil)
					ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1",
						Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: "Here is the spec: {"}, nil)
				}, failureOutputInvalid},
			} {
				clock = 1000
				expectStart()
				expectContext()
				c.expect()
				expectFinish()

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				So(err, ShouldBeNil)
				So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_FAILED)
				So(finished.Error, ShouldEqual, c.code)
				So(finished.Spec.Scenarios, ShouldBeEmpty)
			}
		})

		Convey("Drafting needs documents and catalogs, and documents a drafting can read\n", func() {
			noCatalogs := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(noCatalogs, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{
				{ID: "d1", Content: strings.Repeat("字", maxInputRunes+1)},
			}, nil)

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_NoDocuments)
			_, err = svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_NoCatalogs)
			_, err = svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InputsTooLarge)
		})

		Convey("A project drafting, or with decisions not discarded, is not drafted again\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).Times(3)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil).Times(3)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Version: 3}, true, nil).Times(3)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).Times(3)
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(false, nil)
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(false, interfaces.ErrRequirementHasDecisions)
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), true).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement, _ int64, _ bool) (bool, error) {
					So(req.Version, ShouldEqual, 3)
					return false, errors.New("db down")
				})

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
			_, err = svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_HasDecisions)
			_, err = svc.GenerateRequirement(ctx, token, "p1", caller, true)
			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError)
		})

		Convey("Drafting again after a confirmation made against rq-n starts rq-n+1\n", func() {
			for _, c := range []struct {
				confirmed int
				want      int
			}{{3, 4}, {2, 3}, {0, 3}} {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
				ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Version: 3}, true, nil)
				draft := testDraft()
				draft.Items.Confirmation = &interfaces.DraftConfirmation{RequirementVersion: c.confirmed}
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
				want := c.want
				ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), false).DoAndReturn(
					func(_ context.Context, req *interfaces.Requirement, _ int64, _ bool) (bool, error) {
						So(req.Version, ShouldEqual, want)
						return false, nil
					})

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
			}
		})

		Convey("A draft that cannot be read stops drafting\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Version: 3}, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, errors.New("db down"))

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusInternalServerError, perrors.BknProject_Requirement_InternalError)
		})

		Convey("Only the owner drafts\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{
				ID: "p1", Owner: interfaces.AccountInfo{ID: "u2"}}}, nil)

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
		})

		Convey("FailInterrupted fails what a previous run left drafting\n", func() {
			ra.EXPECT().FailDrafting(gomock.Any(), failureInterrupted, int64(1000)).Return(int64(1), nil)
			ra.EXPECT().FailGenerating(gomock.Any(), failureInterrupted, int64(1000)).Return(int64(0), nil)

			So(svc.FailInterrupted(ctx), ShouldBeNil)
		})
	})
}

func Test_GetRequirement(t *testing.T) {
	Convey("Test GetRequirement\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", CatalogIDs: []string{"c1"},
			NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN}}
		docs := []*interfaces.RequirementDocument{{ID: "d1", SHA256: "h1", DocType: interfaces.REQUIREMENT_DOC_PRD}}
		inputs := requirementInputsOf(docs, project.Project)
		ready := func() *interfaces.Requirement {
			return &interfaces.Requirement{ProjectID: "p1", Version: 1, Status: interfaces.REQUIREMENT_STATUS_READY,
				Inputs: inputs, Spec: interfaces.RequirementSpec{
					Conflicts: []*interfaces.RequirementConflict{{ID: "k1"}, {ID: "k2", Deferred: true},
						{ID: "k3", Resolution: &interfaces.RequirementResolution{Reason: "r"}},
						{ID: "k4", Resolution: &interfaces.RequirementResolution{Reason: "r"}, Deferred: true}},
					Choices: []*interfaces.RequirementChoice{{ID: "f1"}, {ID: "f2", Decision: &interfaces.RequirementChoiceDecision{OptionID: "o1"}}},
					Questions: []*interfaces.RequirementQuestion{
						{ID: "q1", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY},
						{ID: "q2", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, Basis: interfaces.RequirementBasis{Functions: []string{"f"}}},
						{ID: "q3", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, Notes: []string{"stale"}},
						{ID: "q4", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, Basis: interfaces.RequirementBasis{Metrics: []string{"m"}}},
						{ID: "q5", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, Basis: interfaces.RequirementBasis{Skills: []string{"s"}}},
						// A period counted back from a time no input gives, inputs known or not.
						{ID: "q6", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, RelativeWindow: true,
							Inputs: &[]interfaces.RequirementInput{{Name: "o", Role: interfaces.REQUIREMENT_INPUT_RECORD}}},
						{ID: "q7", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, RelativeWindow: true},
						{ID: "q8", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, RelativeWindow: true,
							Inputs: &[]interfaces.RequirementInput{{Name: "t", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME}}},
						// An input whose property the server took from the object type's key.
						{ID: "q9", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE,
							Inputs: &[]interfaces.RequirementInput{{Name: "o", Role: interfaces.REQUIREMENT_INPUT_RECORD, PropertyFromKey: true}}},
					},
				}}
		}

		Convey("A specification drafted from the current inputs is not stale, and counts what is open\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(ready(), true, nil)
			ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return(docs, nil)

			view, err := svc.GetRequirement(ctx, token, "p1")

			So(err, ShouldBeNil)
			So(view.Stale, ShouldBeFalse)
			So(view.UnresolvedConflicts, ShouldEqual, 3)
			// A capability question that names nothing it rests on is noted, when read.
			notes := [][]string{}
			for _, q := range view.Spec.Questions {
				notes = append(notes, q.Notes)
			}
			So(notes, ShouldResemble, [][]string{{interfaces.QUESTION_NOTE_CAPABILITY_WITHOUT_BASIS}, nil, nil, nil, nil,
				{interfaces.QUESTION_NOTE_REFERENCE_TIME_UNBOUND}, {interfaces.QUESTION_NOTE_REFERENCE_TIME_UNBOUND}, nil,
				{interfaces.QUESTION_NOTE_INPUT_PROPERTY_FROM_KEY}})
			So(view.UndecidedChoices, ShouldEqual, 1)
			body, _ := json.Marshal(view)
			So(string(body), ShouldContainSubstring, `"stale":false`)
			So(string(body), ShouldContainSubstring, `"version":1`)
		})

		Convey("A change to a document, its type, the documents, catalogs or language makes it stale\n", func() {
			for _, change := range []func() ([]*interfaces.RequirementDocument, *interfaces.Project){
				func() ([]*interfaces.RequirementDocument, *interfaces.Project) {
					return []*interfaces.RequirementDocument{{ID: "d1", SHA256: "h2", DocType: interfaces.REQUIREMENT_DOC_PRD}}, project.Project
				},
				func() ([]*interfaces.RequirementDocument, *interfaces.Project) {
					return []*interfaces.RequirementDocument{{ID: "d1", SHA256: "h1", DocType: interfaces.REQUIREMENT_DOC_POLICY}}, project.Project
				},
				func() ([]*interfaces.RequirementDocument, *interfaces.Project) {
					return append(docs, &interfaces.RequirementDocument{ID: "d2", SHA256: "h3"}), project.Project
				},
				func() ([]*interfaces.RequirementDocument, *interfaces.Project) {
					return []*interfaces.RequirementDocument{}, project.Project
				},
				func() ([]*interfaces.RequirementDocument, *interfaces.Project) {
					p := *project.Project
					p.CatalogIDs = []string{"c1", "c9"}
					return docs, &p
				},
				func() ([]*interfaces.RequirementDocument, *interfaces.Project) {
					p := *project.Project
					p.NamingLanguage = interfaces.NAMING_LANGUAGE_EN_US
					return docs, &p
				},
			} {
				current, p := change()
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: p}, nil)
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(ready(), true, nil)
				ra.EXPECT().ListDocuments(gomock.Any(), "p1").Return(current, nil)

				view, err := svc.GetRequirement(ctx, token, "p1")
				So(err, ShouldBeNil)
				So(view.Stale, ShouldBeTrue)
			}
		})

		Convey("A specification drafting or failed is not stale, and none is 404\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil).Times(2)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Status: interfaces.REQUIREMENT_STATUS_DRAFTING}, true, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)

			view, err := svc.GetRequirement(ctx, token, "p1")
			So(err, ShouldBeNil)
			So(view.Stale, ShouldBeFalse)
			_, err = svc.GetRequirement(ctx, token, "p1")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
		})
	})
}

// bkn-agent refuses a response_format that is not a valid JSON Schema with 400
// BknAgent.ParamError.FormatError before drafting starts, so every keyword must hold
// what JSON Schema allows: "required" an array of the object's own properties, never null.
func Test_DraftResponseFormatIsValidJSONSchema(t *testing.T) {
	Convey("The response format of pass draft is a valid JSON Schema", t, func() {
		b, err := json.Marshal(draftResponseFormat())
		So(err, ShouldBeNil)
		var schema any
		So(json.Unmarshal(b, &schema), ShouldBeNil)
		So(schema.(map[string]any)["type"], ShouldEqual, "object")

		var problems []string
		var walk func(path string, v any)
		walk = func(path string, v any) {
			switch v := v.(type) {
			case nil:
				problems = append(problems, path+" is null")
			case map[string]any:
				if req, ok := v["required"]; ok {
					names, isList := req.([]any)
					props, _ := v["properties"].(map[string]any)
					if !isList {
						problems = append(problems, path+"/required is not an array")
					}
					for _, n := range names {
						if _, ok := props[n.(string)]; !ok {
							problems = append(problems, fmt.Sprintf("%s/required names %v, not a property", path, n))
						}
					}
				}
				if enum, ok := v["enum"]; ok {
					if values, isList := enum.([]any); !isList || len(values) == 0 {
						problems = append(problems, path+"/enum is not a non-empty array")
					}
				}
				for k, child := range v {
					walk(path+"/"+k, child)
				}
			case []any:
				for i, child := range v {
					walk(fmt.Sprintf("%s[%d]", path, i), child)
				}
			}
		}
		walk("", schema)
		So(problems, ShouldBeEmpty)
	})
}
