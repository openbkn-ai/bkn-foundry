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
	return &interfaces.Draft{Status: interfaces.DRAFT_STATUS_READY, Items: interfaces.DraftItems{
		ObjectTypes: []*interfaces.DraftObjectType{
			{ID: "sales_order", Name: "Sales order", Aliases: []string{"Order"}, Adopted: true},
			{ID: "customer", Name: "Customer", Adopted: true},
		},
		RelationTypes: []*interfaces.DraftRelationType{{ID: "sales_order_customer", Name: "Order of customer",
			Source: "sales_order", Target: "customer", Adopted: true}},
	}}
}

// confirmedDraft is testDraft confirmed: the kinetic run and scenario drafting rest on it.
func confirmedDraft() *interfaces.Draft {
	draft := testDraft()
	draft.Items.Confirmation = &interfaces.DraftConfirmation{Commit: "abc"}
	return draft
}

// withConcepts records a ready concepts run in req, as scenario drafting over documents needs.
func withConcepts(req *interfaces.Requirement) *interfaces.Requirement {
	if req.Spec.Runs == nil {
		req.Spec.Runs = map[string]*interfaces.RequirementRun{}
	}
	req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS] = &interfaces.RequirementRun{Status: interfaces.REQUIREMENT_RUN_READY, RunID: "c",
		Inputs: &interfaces.RequirementInputs{Documents: []interfaces.RequirementInputDocument{}}}
	return req
}

func cite(docID, quote string) agentCitation { return agentCitation{DocumentID: docID, Quote: quote} }

// built is what buildSpec adds to an empty specification.
func built(out *draftOutput, docs map[string]*interfaces.RequirementDocument, draft *interfaces.Draft) *interfaces.RequirementSpec {
	spec := emptySpec()
	buildSpec(out, docs, draft, spec)
	return spec
}

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

			spec := built(out, docs, draft)

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

			spec := built(out, docs, draft)

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

			spec := built(out, docs, draft)

			So(spec.Concepts[0].MapsTo, ShouldResemble, &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"})
			So(spec.Concepts[1].MapsTo, ShouldBeNil)
			So(spec.Concepts[2].MapsTo, ShouldBeNil)
			So(spec.Concepts[3].MapsTo.ID, ShouldEqual, "sales_order_customer")
		})

		Convey("Without a draft no concept maps to anything\n", func() {
			out := &draftOutput{Concepts: []agentConcept{{ID: "a", Term: "Customer", SupplementReason: "r",
				MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}}}}

			spec := built(out, docs, nil)

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

			spec := built(out, docs, draft)

			So(spec.Scenarios, ShouldBeEmpty)
			So(spec.Questions, ShouldBeEmpty)
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedScenario, Text: "Invented", Reason: rejectedQuoteNotFound},
				{Kind: rejectedScenario, Text: "No basis", Reason: rejectedQuoteNotFound},
				{Kind: rejectedScenario, Text: "Unknown document", Reason: rejectedUnknownDocument},
				{Kind: rejectedQuestion, Text: "Reworded?", Reason: rejectedQuoteNotFound},
			})
		})

		Convey("A question's numbers must be in its quotes too, unless it has none\n", func() {
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "Which requests wait over 5 days?", Citations: []agentCitation{cite("pol", "within 2 working days")}},
				{Text: "Which requests wait over 5 days?", SupplementReason: "asked by the business"},
			}}

			spec := built(out, docs, draft)

			So(spec.Questions, ShouldHaveLength, 1)
			So(spec.Questions[0].SupplementReason, ShouldEqual, "asked by the business")
			So(spec.Rejected[0].Reason, ShouldEqual, rejectedNumberNotInQuote)
		})

		Convey("A record's value the documents give elsewhere does not drop a question whose template holds its slot\n", func() {
			quote := []agentCitation{cite("prd", "A new date confirmed by email updates the agreed date directly.")}
			out := &draftOutput{Questions: []agentQuestion{
				// 2 is in another block of the documents, and the template names the order by its slot.
				{Text: "When is order 2 due?", Template: "When is order {order} due?", Citations: quote},
				// 7 is in no document: made up. Nor is 22, though 2 is.
				{Text: "When is order 7 due?", Template: "When is order {order} due?", Citations: quote},
				{Text: "When is order 22 due?", Template: "When is order {order} due?", Citations: quote},
				// The template says the number itself: a condition, not a record named.
				{Text: "Which orders wait 2 days?", Template: "Which orders wait 2 days?", Citations: quote},
				// No template to show it is a record's value; nor one without a slot, or one the text does not fill.
				{Text: "When is order 2 due?", Citations: quote},
				{Text: "Which orders wait over 2 days?", Template: "Which orders are late?", Citations: quote},
				{Text: "Which orders of 2 wait?", Template: "When is order {order} due?", Citations: quote},
				{Text: "When is order 2 due? Within 7 days.", Template: "When is order {order} due?", Citations: quote},
				// The number stands outside the slot: a condition.
				{Text: "When is order A due in 2 days?", Template: "When is order {order} due in 2 days?", Citations: quote},
			}}

			spec := built(out, docs, draft)

			So(spec.Questions, ShouldHaveLength, 1)
			So(spec.Questions[0].Text, ShouldEqual, "When is order 2 due?")
			So(spec.Questions[0].Template, ShouldEqual, "When is order {order} due?")
			So(spec.Rejected, ShouldHaveLength, 8)
			for _, r := range spec.Rejected {
				So(r.Reason, ShouldEqual, rejectedNumberNotInQuote)
			}
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

			spec := built(out, docs, withProps)

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
			spec = built(out, docs, withProps)
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
			spec = built(out, docs, nil)
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

		Convey("An input's example stands only as a whole value the documents state, and the question cites where\n", func() {
			exDocs := documentsByID([]*interfaces.RequirementDocument{doc("ex", interfaces.REQUIREMENT_DOC_PRD,
				"Staff ask which lines an order has, such as order SO-1001.",
				"For example, order SO-1002. Or (SO-1003).", "Lots LOT-7-B, then LOT-7.")})
			withProps := testDraft()
			withProps.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{
				{Name: "order_no", Type: "string"}, {Name: "signed_on", Type: "date"}}
			orderRef := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
			asked := func(text string, example string) agentQuestion {
				return agentQuestion{Text: text, Template: "Lines of {order}?",
					Citations: []agentCitation{cite("ex", "Staff ask which lines an order has")},
					Inputs: []agentInput{{Name: "order", ConceptID: "ord", Property: "order_no",
						Role: interfaces.REQUIREMENT_INPUT_RECORD, Example: example}}}
			}
			out := &draftOutput{
				Concepts: []agentConcept{{ID: "ord", Term: "Order", SupplementReason: "r", MapsTo: &orderRef}},
				Questions: []agentQuestion{
					// Stated in a block the question does not cite: that block is cited, once.
					asked("a", " SO-1002 "),
					// Within parentheses, still the whole value.
					asked("b", "SO-1003"),
					// Part of a longer value: not stated.
					asked("c", "SO-100"),
					// In no document.
					asked("d", "SO-9999"),
					// Stated in the block the question cites already, at its end: nothing to add.
					asked("e", "SO-1001"),
					// A hyphen continues a code; a later occurrence may still stand whole.
					asked("g", "LOT"),
					asked("h", "LOT-7"),
					// A reference time is taken from the data, never an example.
					{Text: "f", Template: "Orders before {as_of}?", SupplementReason: "r", Inputs: []agentInput{
						{Name: "as_of", ConceptID: "ord", Property: "signed_on", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME, Example: "SO-1002"}}},
				},
			}

			spec := built(out, exDocs, withProps)

			examples := []string{}
			for _, q := range spec.Questions {
				examples = append(examples, (*q.Inputs)[0].Example)
			}
			So(examples, ShouldResemble, []string{"SO-1002", "SO-1003", "", "", "SO-1001", "", "LOT-7", ""})
			So(spec.Questions[0].Citations, ShouldHaveLength, 2)
			So(spec.Questions[0].Citations[1].BlockID, ShouldEqual, "b2")
			So(spec.Questions[0].Citations[1].Quote, ShouldEqual, "For example, order SO-1002. Or (SO-1003).")
			So(spec.Questions[1].Citations, ShouldHaveLength, 2)
			So(spec.Questions[4].Citations, ShouldHaveLength, 1)
			So(spec.Questions[2].Citations, ShouldHaveLength, 1)
			reasons := map[string]string{}
			for _, r := range spec.Rejected {
				reasons[r.Kind+" "+r.Text] = r.Reason
			}
			So(reasons, ShouldResemble, map[string]string{
				"input_example order=SO-100 (c)":  rejectedExampleNotInDocuments,
				"input_example order=SO-9999 (d)": rejectedExampleNotInDocuments,
				"input_example order=LOT (g)":     rejectedExampleNotInDocuments,
			})
		})

		Convey("An example in a block the question cites only in part extends that citation to the block\n", func() {
			exDocs := documentsByID([]*interfaces.RequirementDocument{doc("ex", interfaces.REQUIREMENT_DOC_PRD,
				"Staff ask which lines an order has. For example, order SO-1002.")})
			withProps := testDraft()
			withProps.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{{Name: "order_no", Type: "string"}}
			orderRef := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
			out := &draftOutput{
				Concepts: []agentConcept{{ID: "ord", Term: "Order", SupplementReason: "r", MapsTo: &orderRef}},
				Questions: []agentQuestion{{Text: "a", Template: "Lines of {order}?",
					Citations: []agentCitation{cite("ex", "Staff ask which lines an order has")},
					Inputs: []agentInput{{Name: "order", ConceptID: "ord", Property: "order_no",
						Role: interfaces.REQUIREMENT_INPUT_RECORD, Example: "SO-1002"}}}},
			}

			spec := built(out, exDocs, withProps)

			So(spec.Questions[0].Citations, ShouldHaveLength, 1)
			So(spec.Questions[0].Citations[0].Quote, ShouldEqual, "Staff ask which lines an order has. For example, order SO-1002.")
			So(spec.Questions[0].Citations[0].Start, ShouldEqual, 0)
		})

		Convey("A question's layer is knowledge only when said so, or when it rests on no kinetic need\n", func() {
			spec := emptySpec()
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "amount over threshold"},
				{ID: "n2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "approval check"},
				{ID: "n3", Kind: interfaces.LOGIC_NEED_SKILL, Name: "Signed-Change"},
			}
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "a", Layer: "knowledge", SupplementReason: "r"},
				{Text: "b", Layer: "capability", SupplementReason: "r"},
				{Text: "c", Layer: "whatever", SupplementReason: "r"},
				// Said knowledge, but resting on a metric, a function or a skill: more than the ontology.
				{Text: "d", Layer: "knowledge", SupplementReason: "r", LogicNeedIDs: []string{"n1"}},
				{Text: "e", Layer: "knowledge", SupplementReason: "r", LogicNeedIDs: []string{"n2", "n2"}},
				// Kept needs may be named by their prefixed id.
				{Text: "f", Layer: "knowledge", SupplementReason: "r", LogicNeedIDs: []string{"kept-n3"}},
				// Blank ids are none: still knowledge.
				{Text: "g", Layer: "knowledge", SupplementReason: "r", LogicNeedIDs: []string{"", " "}},
				// A need the specification does not have is rejected; said capability for nothing that
				// stands, it is a lookup.
				{Text: "h", Layer: "capability", SupplementReason: "r", LogicNeedIDs: []string{"n9"}},
				{Text: "i", Layer: "capability", SupplementReason: "r", LogicNeedIDs: []string{"n9", "n1"}},
			}}

			buildSpec(out, docs, draft, spec)

			layers := []string{}
			for _, q := range spec.Questions {
				layers = append(layers, q.Layer)
			}
			k, c := interfaces.REQUIREMENT_LAYER_KNOWLEDGE, interfaces.REQUIREMENT_LAYER_CAPABILITY
			So(layers, ShouldResemble, []string{k, c, c, c, c, c, k, k, c})
			So(spec.Questions[3].LogicNeedIDs, ShouldResemble, []string{"n1"})
			So(spec.Questions[4].LogicNeedIDs, ShouldResemble, []string{"n2"})
			So(spec.Questions[5].LogicNeedIDs, ShouldResemble, []string{"n3"})
			So(spec.Questions[6].LogicNeedIDs, ShouldResemble, []string{})
			So(spec.Questions[8].LogicNeedIDs, ShouldResemble, []string{"n1"})
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedBasis, Text: "n9 (h)", Reason: rejectedUnknownLogicNeed},
				{Kind: rejectedBasis, Text: "n9 (i)", Reason: rejectedUnknownLogicNeed}})
			// A need serves the scenarios of the questions resting on it.
			So(spec.LogicNeeds[0].ScenarioIDs, ShouldResemble, []string{})
		})

		Convey("A filter backed by functions alone is a filter on properties: knowledge, its functions rejected\n", func() {
			spec := emptySpec()
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				{ID: "n1", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "window check"},
				{ID: "n2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "status check"},
				{ID: "n3", Kind: interfaces.LOGIC_NEED_METRIC, Name: "late count"},
				{ID: "n4", Kind: interfaces.LOGIC_NEED_SKILL, Name: "signed change"},
				{ID: "n5", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "approval check"},
			}
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "a", Type: "filter", Layer: "capability", SupplementReason: "r", LogicNeedIDs: []string{"n1", "n2"}},
				// A metric, a skill, or another type keeps what it rests on.
				{Text: "b", Type: "filter", Layer: "capability", SupplementReason: "r", LogicNeedIDs: []string{"n3", "n1"}},
				{Text: "c", Type: "filter", Layer: "capability", SupplementReason: "r", LogicNeedIDs: []string{"n1", "n4"}},
				{Text: "d", Type: "judge", Layer: "capability", SupplementReason: "r", LogicNeedIDs: []string{"n5"}},
			}}

			buildSpec(out, docs, draft, spec)

			So(spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(spec.Questions[0].LogicNeedIDs, ShouldResemble, []string{})
			for i, needs := range [][]string{{"n3", "n1"}, {"n1", "n4"}, {"n5"}} {
				So(spec.Questions[i+1].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
				So(spec.Questions[i+1].LogicNeedIDs, ShouldResemble, needs)
			}
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedBasis, Text: "window check (a)", Reason: rejectedFunctionOnFilter},
				{Kind: rejectedBasis, Text: "status check (a)", Reason: rejectedFunctionOnFilter}})
		})

		Convey("At most maxSpecScenarios scenarios and maxDraftConcepts concepts are kept, the first; the rest are rejected\n", func() {
			out := &draftOutput{}
			for i := range maxSpecScenarios + 1 {
				out.Scenarios = append(out.Scenarios, agentScenario{Name: fmt.Sprintf("s%d", i), SupplementReason: "r"})
			}
			// One that does not stand takes no place.
			out.Concepts = append(out.Concepts, agentConcept{Term: "unquoted"})
			for i := range maxDraftConcepts + 2 {
				out.Concepts = append(out.Concepts, agentConcept{Term: fmt.Sprintf("t%d", i), SupplementReason: "r"})
			}
			// Past the bound, one that would not stand anyway says why.
			out.Concepts = append(out.Concepts, agentConcept{Term: "late unquoted"})

			spec := built(out, docs, draft)

			So(spec.Scenarios, ShouldHaveLength, maxSpecScenarios)
			So(spec.Concepts, ShouldHaveLength, maxDraftConcepts)
			So(spec.Concepts[0].Term, ShouldEqual, "t0")
			reasons := map[string]string{}
			for _, r := range spec.Rejected {
				reasons[r.Kind+" "+r.Text] = r.Reason
			}
			So(reasons, ShouldResemble, map[string]string{
				"concept unquoted":                             rejectedQuoteNotFound,
				"concept late unquoted":                        rejectedQuoteNotFound,
				fmt.Sprintf("scenario s%d", maxSpecScenarios):  rejectedTooMany,
				fmt.Sprintf("concept t%d", maxDraftConcepts):   rejectedTooMany,
				fmt.Sprintf("concept t%d", maxDraftConcepts+1): rejectedTooMany,
			})
		})

		Convey("Drafting reads the extracted concepts and needs, maps only unmapped concepts, and adds only new ones\n", func() {
			customer := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}
			order := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"}
			badge := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "badge"}
			spec := emptySpec()
			spec.Concepts = []*interfaces.RequirementConcept{
				{ID: "c1", Term: "Client", Category: "object", Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS},
				{ID: "c2", Term: "Order", Category: "object", MapsTo: &order, Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS},
				{ID: "c3", Term: "Buyer", Category: "object", Edited: true, Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS},
				{ID: "c4", Term: "Badge", Category: "object", Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS},
			}
			spec.LastIDs["c"] = 4
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate"}}
			out := &draftOutput{
				Scenarios: []agentScenario{{ID: "sc", Name: "Chase late orders", SupplementReason: "r"}},
				ConceptMaps: []agentConceptMap{
					{ConceptID: "c1", MapsTo: &customer},
					// Mapped already, changed by a person, or to an item not adopted: left as is.
					{ConceptID: "kept-c2", MapsTo: &customer},
					{ConceptID: "c3", MapsTo: &customer},
					{ConceptID: "c4", MapsTo: &badge},
					{ConceptID: "c9", MapsTo: &customer},
				},
				Concepts: []agentConcept{
					// A concept extracted already is not added again; the Agent's id names it.
					{ID: "x", Term: " client ", SupplementReason: "r"},
					{ID: "y", Term: "Reminder", Category: "event", SupplementReason: "r"},
				},
				Questions: []agentQuestion{{Text: "Which orders of a client are late?", ScenarioIDs: []string{"sc"}, Layer: "knowledge",
					ConceptIDs: []string{"x", "c2", "y"}, LogicNeedIDs: []string{"n1"}, SupplementReason: "r"}},
			}

			buildSpec(out, docs, draft, spec)

			So(spec.Concepts[0].MapsTo, ShouldResemble, &customer)
			So(spec.Concepts[1].MapsTo, ShouldResemble, &order)
			So(spec.Concepts[2].MapsTo, ShouldBeNil)
			So(spec.Concepts[3].MapsTo, ShouldBeNil)
			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c1", "c2", "c3", "c4", "c5"})
			So(spec.Concepts[4].Source, ShouldEqual, interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS)
			q := spec.Questions[0]
			So(q.Basis.ConceptIDs, ShouldResemble, []string{"c1", "c2", "c5"})
			So(q.Needs, ShouldResemble, []interfaces.RequirementItemRef{customer, order})
			So(q.Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			So(spec.LogicNeeds[0].ScenarioIDs, ShouldResemble, []string{"s1"})
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

			message, omitted, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_ZH_CN, nil, tables, nil, emptySpec(), emptySpec())

			So(err, ShouldBeNil)
			So(omitted, ShouldEqual, 5)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.Tables, ShouldHaveLength, maxMessageTables)
			So(body.Tables[0].Name, ShouldEqual, "t0")
		})

		Convey("A drafted object type goes with its properties and primary keys, so an input can name one\n", func() {
			draft := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{{
				ID: "ot1", Name: "Item", PrimaryKeys: []string{"code"}, Adopted: true,
				Properties: []*interfaces.DraftProperty{{Name: "code", DisplayName: "Code", Type: "string"}, {Name: "made", Type: "date"}},
			}}}}

			message, _, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, draft, emptySpec(), emptySpec())

			So(err, ShouldBeNil)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.ObjectTypes, ShouldHaveLength, 1)
			So(body.ObjectTypes[0].PrimaryKeys, ShouldResemble, []string{"code"})
			So(body.ObjectTypes[0].Properties, ShouldResemble,
				[]messageProperty{{Name: "code", DisplayName: "Code", Type: "string"}, {Name: "made", Type: "date"}})
		})

		Convey("A document model goes without its evidence types and technical columns, which are not published\n", func() {
			doc := func(role string) *interfaces.DraftDocumentModel { return &interfaces.DraftDocumentModel{Role: role} }
			draft := &interfaces.Draft{Items: interfaces.DraftItems{
				ObjectTypes: []*interfaces.DraftObjectType{
					{ID: "contract_doc", Name: "Contract", PrimaryKeys: []string{"_id"}, Adopted: true, Document: doc("file"), Properties: []*interfaces.DraftProperty{
						{Name: "_id", Type: "string"}, {Name: "contract_no", DisplayName: "Contract number", Type: "string"},
						{Name: "document_id", Type: "string"}, {Name: "source_quote", Type: "text"}, {Name: "model_fingerprint", Type: "string"}}},
					{ID: "party_doc", Name: "Party", PrimaryKeys: []string{"party_name"}, Adopted: true, Document: doc("entity"), Properties: []*interfaces.DraftProperty{
						{Name: "_id", Type: "string"}, {Name: "party_name", Type: "string"}, {Name: "entity_id", Type: "string"}}},
					{ID: "source_document", Name: "Source document", PrimaryKeys: []string{"_id"}, Adopted: true, Document: doc("evidence_document"), Properties: []*interfaces.DraftProperty{{Name: "name", Type: "string"}}},
				},
				RelationTypes: []*interfaces.DraftRelationType{
					{ID: "signed_by", Source: "contract_doc", Target: "party_doc", Adopted: true},
					{ID: "contract_doc_evidence", Source: "contract_doc", Target: "source_document", Adopted: true},
				},
			}}

			message, _, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, draft, emptySpec(), emptySpec())

			So(err, ShouldBeNil)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.ObjectTypes, ShouldHaveLength, 2)
			So(body.ObjectTypes[0].PrimaryKeys, ShouldBeEmpty)
			So(body.ObjectTypes[0].Properties, ShouldResemble, []messageProperty{{Name: "contract_no", DisplayName: "Contract number", Type: "string"}})
			So(body.ObjectTypes[1].PrimaryKeys, ShouldResemble, []string{"party_name"})
			So(body.ObjectTypes[1].Properties, ShouldResemble, []messageProperty{{Name: "party_name", Type: "string"}})
			So(body.RelationTypes, ShouldHaveLength, 1)
			So(body.RelationTypes[0].ID, ShouldEqual, "signed_by")
		})

		Convey("Only adopted items go, with their descriptions: a candidate no one adopted is not the ontology\n", func() {
			draft := testDraft()
			draft.Items.ObjectTypes[0].Description = "What a customer orders"
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "candidate", Name: "Candidate"})
			draft.Items.RelationTypes = append(draft.Items.RelationTypes, &interfaces.DraftRelationType{ID: "maybe",
				Source: "sales_order", Target: "candidate"})

			message, _, _, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, draft, emptySpec(), emptySpec())

			So(err, ShouldBeNil)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.ObjectTypes, ShouldHaveLength, 2)
			So(body.ObjectTypes[0].Description, ShouldEqual, "What a customer orders")
			So(body.RelationTypes, ShouldHaveLength, 1)
			So(body.RelationTypes[0].ID, ShouldEqual, "sales_order_customer")
		})

		Convey("At most maxMessageProperties properties of an object type go, its keys and times first, and the rest are counted\n", func() {
			props := []*interfaces.DraftProperty{}
			for i := range maxMessageProperties + 3 {
				props = append(props, &interfaces.DraftProperty{Name: fmt.Sprintf("p%d", i), DisplayName: fmt.Sprintf("p%d", i), Type: "string"})
			}
			props = append(props, &interfaces.DraftProperty{Name: "made", Type: "datetime"}, &interfaces.DraftProperty{Name: "id", Type: "string"})
			draft := &interfaces.Draft{Items: interfaces.DraftItems{ObjectTypes: []*interfaces.DraftObjectType{
				{ID: "ot1", Name: "Item", PrimaryKeys: []string{"id"}, Properties: props, Adopted: true}}}}

			message, _, omitted, err := buildDraftMessage(interfaces.NAMING_LANGUAGE_EN_US, nil, nil, draft, emptySpec(), emptySpec())

			So(err, ShouldBeNil)
			So(omitted, ShouldEqual, 5)
			var body draftMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			sent := body.ObjectTypes[0].Properties
			So(sent, ShouldHaveLength, maxMessageProperties)
			So(sent[0], ShouldResemble, messageProperty{Name: "id", Type: "string"})
			So(sent[1], ShouldResemble, messageProperty{Name: "made", Type: "datetime"})
			// A display name the same as the name says nothing more.
			So(sent[2], ShouldResemble, messageProperty{Name: "p0", Type: "string"})
			So(body.ObjectTypes[0].PropertiesOmitted, ShouldEqual, 5)
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
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedDraft(), true, nil)
			// Scenarios are drafted over the concepts extracted from the documents.
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(withConcepts(&interfaces.Requirement{ProjectID: "p1", Version: 1,
				Status: interfaces.REQUIREMENT_STATUS_READY, UpdateTime: 900}), true, nil)
			// A run may wait for the Agent twice, the second time after an answer it could not use.
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), int64(1000)-2*agentDeadline.Milliseconds()-time.Minute.Milliseconds(), int64(900)).
				DoAndReturn(func(_ context.Context, req *interfaces.Requirement, _ int64, _ int64) (bool, error) {
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
			"concepts":[],"concept_maps":[],"questions":[]}`

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
			So(finished.Inputs, ShouldResemble, &interfaces.RequirementInputs{
				RulesVersion: requirementRulesVersion,
				Documents: []interfaces.RequirementInputDocument{
					{ID: "pol", SHA256: "h-pol", DocType: interfaces.REQUIREMENT_DOC_OTHER},
					{ID: "prd", SHA256: "h-prd", DocType: interfaces.REQUIREMENT_DOC_PRD},
				},
				CatalogIDs: []string{"c1", "c2"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US,
			})
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
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(testDraft(), true, nil)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{}`}, nil)
			expectFinish()

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
			So(finished.Spec.TablesOmitted, ShouldEqual, 3)
		})

		Convey("A draft that cannot be read once drafting runs fails it, the Agent not asked\n", func() {
			for _, read := range []func(){
				func() { da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil) },
				func() { da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, errors.New("db down")) },
			} {
				expectStart()
				va.EXPECT().ListTables(gomock.Any(), caller, gomock.Any()).Return(nil, nil).Times(2)
				read()
				expectFinish()

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				So(err, ShouldBeNil)
				So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_FAILED)
				So(finished.Error, ShouldEqual, failureInterrupted)
			}
		})

		Convey("Properties beyond what one drafting names of an object type are counted in the specification\n", func() {
			expectStart()
			va.EXPECT().ListTables(gomock.Any(), caller, gomock.Any()).Return(nil, nil).Times(2)
			wide := testDraft()
			for i := range maxMessageProperties + 2 {
				wide.Items.ObjectTypes[0].Properties = append(wide.Items.ObjectTypes[0].Properties,
					&interfaces.DraftProperty{Name: fmt.Sprintf("p%d", i), Type: "string"})
			}
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(wide, true, nil)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{}`}, nil)
			expectFinish()

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(finished.Spec.PropertiesOmitted, ShouldEqual, 2)
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
				// An answer that is no JSON is asked for again, once.
				{"output not JSON", func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil).Times(2)
					ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1",
						Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: "Here is the spec: {"}, nil).Times(2)
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
				// Only an answer that does not parse was asked for again.
				So(finished.Spec.DraftRetried, ShouldEqual, c.code == failureOutputInvalid)
			}
		})

		Convey("An answer the Agent could not give as the schema asks is asked for once more\n", func() {
			structured := &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_FAILED,
				FailureDetail: "StructuredOutputError: structured output failed: not supported natively and the prompt fallback was still invalid"}

			Convey("and the second answer is recorded\n", func() {
				expectStart()
				expectContext()
				ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil)
				ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(structured, nil)
				ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t2"}, nil)
				ba.EXPECT().GetTask(gomock.Any(), caller, "t2").Return(&interfaces.BknAgentTask{TaskID: "t2",
					Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil)
				expectFinish()

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				So(err, ShouldBeNil)
				So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
				So(finished.Spec.Scenarios, ShouldHaveLength, 1)
				So(finished.Spec.DraftRetried, ShouldBeTrue)
				So(finished.AgentTaskID, ShouldEqual, "t2")
			})

			Convey("and so is an answer that does not parse\n", func() {
				expectStart()
				expectContext()
				ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil)
				ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1",
					Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: "Here is the spec: {"}, nil)
				ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t2"}, nil)
				ba.EXPECT().GetTask(gomock.Any(), caller, "t2").Return(&interfaces.BknAgentTask{TaskID: "t2",
					Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil)
				expectFinish()

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				So(err, ShouldBeNil)
				So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_READY)
				So(finished.Spec.DraftRetried, ShouldBeTrue)
				So(finished.AgentTaskID, ShouldEqual, "t2")
			})

			Convey("and when it fails again, drafting fails as before, saying it was asked twice\n", func() {
				expectStart()
				expectContext()
				ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil).Times(2)
				ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(structured, nil).Times(2)
				expectFinish()

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				So(err, ShouldBeNil)
				So(finished.Status, ShouldEqual, interfaces.REQUIREMENT_STATUS_FAILED)
				So(finished.Error, ShouldEqual, failureAgentFailed)
				So(finished.Spec.DraftRetried, ShouldBeTrue)
			})

			Convey("but a drafting that fails otherwise, or answers at once, is asked once\n", func() {
				expectStart()
				expectContext()
				ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1"}, nil)
				ba.EXPECT().GetTask(gomock.Any(), caller, "t1").Return(&interfaces.BknAgentTask{TaskID: "t1",
					Status: interfaces.BKN_AGENT_TASK_FAILED, FailureDetail: "RateLimitError: structured output was fine"}, nil)
				expectFinish()

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				So(err, ShouldBeNil)
				So(finished.Error, ShouldEqual, failureAgentFailed)
				So(finished.Spec.DraftRetried, ShouldBeFalse)
			})
		})

		Convey("Drafting needs catalogs, and documents a drafting can read; documents are optional\n", func() {
			noCatalogs := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller}}
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(noCatalogs, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{
				{ID: "d1", Content: strings.Repeat("字", maxInputRunes+1)},
			}, nil)

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_NoCatalogs)
			_, err = svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_InputsTooLarge)
		})

		Convey("Drafting needs a confirmed model adopting an object type, documents or none\n", func() {
			notReady := confirmedDraft()
			notReady.Status = interfaces.DRAFT_STATUS_DRAFTING
			noneAdopted := confirmedDraft()
			for _, ot := range noneAdopted.Items.ObjectTypes {
				ot.Adopted = false
			}
			for _, d := range []*interfaces.Draft{nil, notReady, noneAdopted, testDraft()} {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
				ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, d != nil, nil)

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_ModelNotConfirmed)
			}
		})

		Convey("Drafting over documents needs their concepts extracted; without documents it needs none\n", func() {
			// Never ended ready: a first run going or failed has no inputs.
			running := withConcepts(&interfaces.Requirement{Version: 1})
			running.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Status = interfaces.REQUIREMENT_RUN_RUNNING
			running.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Inputs = nil
			failed := withConcepts(&interfaces.Requirement{Version: 1})
			failed.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Status = interfaces.REQUIREMENT_RUN_FAILED
			failed.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Inputs = nil
			for _, previous := range []*interfaces.Requirement{nil, {Version: 1}, running, failed} {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
				ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedDraft(), true, nil)
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(previous, previous != nil, nil)

				_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)

				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_ConceptsNotExtracted)
			}

			// A run that fails after one ended ready keeps its inputs: the concepts stand, drafting goes on.
			failedAgain := withConcepts(&interfaces.Requirement{Version: 1, UpdateTime: 5})
			failedAgain.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Status = interfaces.REQUIREMENT_RUN_FAILED
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedDraft(), true, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(failedAgain, true, nil)
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(5)).Return(false, nil)
			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)

			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedDraft(), true, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(0)).Return(false, nil)

			_, err = svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
		})

		Convey("A project drafting is not drafted again, and a store that fails says so\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).Times(2)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil).Times(2)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(withConcepts(&interfaces.Requirement{Version: 3}), true, nil).Times(2)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(confirmedDraft(), true, nil).Times(2)
			// Decisions are kept with the rest, never discarded unasked: drafting again is never refused for them.
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(0)).Return(false, nil)
			ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(0)).DoAndReturn(
				func(_ context.Context, req *interfaces.Requirement, _ int64, _ int64) (bool, error) {
					So(req.Version, ShouldEqual, 3)
					return false, errors.New("db down")
				})

			_, err := svc.GenerateRequirement(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
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
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(withConcepts(&interfaces.Requirement{Version: 3}), true, nil)
				draft := testDraft()
				draft.Items.Confirmation = &interfaces.DraftConfirmation{Commit: "abc", RequirementVersion: c.confirmed}
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
				want := c.want
				ra.EXPECT().StartDrafting(gomock.Any(), gomock.Any(), gomock.Any(), int64(0)).DoAndReturn(
					func(_ context.Context, req *interfaces.Requirement, _ int64, _ int64) (bool, error) {
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
			ra.EXPECT().ProjectsWithRunning(gomock.Any()).Return([]string{}, nil)
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
		da := pmock.NewMockDraftAccess(ctrl)
		// The ontology is read to mark what changed; tests of it are in stale_test.go.
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).AnyTimes()
		svc := &requirementService{ps: ps, ra: ra, da: da}
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
						{ID: "q2", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, LogicNeedIDs: []string{"n1"}},
						{ID: "q3", Layer: interfaces.REQUIREMENT_LAYER_KNOWLEDGE, Notes: []string{"stale"}},
						{ID: "q4", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, LogicNeedIDs: []string{"n2"}},
						{ID: "q5", Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY, LogicNeedIDs: []string{"n3"}},
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

		// An input's example is asked for, and described, since no prompt names it.
		input := schema.(map[string]any)["properties"].(map[string]any)["questions"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["inputs"].(map[string]any)["items"].(map[string]any)
		So(input["required"], ShouldContain, "example")
		So(input["properties"].(map[string]any)["example"].(map[string]any)["description"], ShouldNotBeEmpty)
	})
}
