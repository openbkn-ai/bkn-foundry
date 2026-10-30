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

// guidanceDraft is a draft whose object types have properties the test questions can name.
func guidanceDraft() *interfaces.Draft {
	d := testDraft()
	d.Items.ObjectTypes[1].Properties = []*interfaces.DraftProperty{
		{Name: "customer_id", DisplayName: "Customer ID", Type: "string", MappedField: "cust_id"},
		{Name: "full_name", DisplayName: "Full name", Type: "string", MappedField: "name"},
	}
	return d
}

func Test_BuildGuidance(t *testing.T) {
	Convey("Test the guidance message and tests\n", t, func() {
		spec := readySpec().Spec
		spec.Concepts[1].MapsTo = &customerRef
		q := spec.Questions[0]

		Convey("The message carries the question, its scenarios, the rules, the concepts and their items' structure\n", func() {
			draft := guidanceDraft()
			draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "supplier", Name: "Supplier"})
			message, err := buildGuidanceMessage(interfaces.NAMING_LANGUAGE_EN_US, &spec, q, draft)

			So(err, ShouldBeNil)
			So(message, ShouldStartWith, "pass guidance\nlanguage: en-US\n")
			var body guidanceMessage
			So(json.Unmarshal([]byte(message[strings.Index(message, "{"):]), &body), ShouldBeNil)
			So(body.Question.ID, ShouldEqual, "q1")
			So(body.Question.Text, ShouldEqual, "Which orders?")
			So(message, ShouldNotContainSubstring, `"inputs"`)
			withNone := *q
			withNone.Inputs = &[]interfaces.RequirementInput{}
			withNone.RelativeWindow = true
			message, err = buildGuidanceMessage(interfaces.NAMING_LANGUAGE_EN_US, &spec, &withNone, draft)
			So(err, ShouldBeNil)
			So(message, ShouldContainSubstring, `"inputs":[]`)
			So(message, ShouldContainSubstring, `"relative_window":true`)
			So(message, ShouldNotContainSubstring, `"server_tests"`)
			// The Agent learns whether the server writes the template and not_exist tests.
			named := *q
			named.Template = "Orders of {customer}?"
			named.Inputs = &[]interfaces.RequirementInput{{Name: "customer", ConceptID: "c2", Property: "customer_id",
				Role: interfaces.REQUIREMENT_INPUT_RECORD}}
			message, err = buildGuidanceMessage(interfaces.NAMING_LANGUAGE_EN_US, &spec, &named, draft)
			So(err, ShouldBeNil)
			So(message, ShouldContainSubstring, `"server_tests":true`)
			So(body.Scenarios[0].ID, ShouldEqual, "s1")
			So(body.Rules[0].Statement, ShouldEqual, "Signed changes only")
			So(body.Concepts, ShouldHaveLength, 2)
			So(body.Concepts[1].MapsTo, ShouldResemble, &customerRef)
			// Only the drafted items the concepts map to, with the properties values belong to.
			So(body.ObjectTypes, ShouldHaveLength, 2)
			So(body.ObjectTypes[1].Properties, ShouldResemble, []messageProperty{
				{Name: "customer_id", DisplayName: "Customer ID", Type: "string"},
				{Name: "full_name", DisplayName: "Full name", Type: "string"},
			})
		})

		Convey("The message carries the items of the path that joins what the question needs\n", func() {
			draft := &interfaces.Draft{Items: interfaces.DraftItems{
				ObjectTypes: []*interfaces.DraftObjectType{{ID: "sales_order", Name: "Order"}, {ID: "customer", Name: "Customer"},
					{ID: "invoice", Name: "Invoice"}},
				RelationTypes: []*interfaces.DraftRelationType{
					{ID: "inv_order", Source: "invoice", Target: "sales_order", Adopted: true},
					{ID: "inv_customer", Source: "invoice", Target: "customer", Adopted: true},
				},
			}}
			// It names the order and the customer only; invoices join them in this draft.
			named := &interfaces.RequirementQuestion{ID: "q9", Text: "Whose orders?", NeedsSource: interfaces.REQUIREMENT_NEEDS_FROM_CONCEPTS,
				Needs: []interfaces.RequirementItemRef{{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "sales_order"},
					{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}}}
			bare := spec
			bare.Concepts = nil

			message, err := buildGuidanceMessage(interfaces.NAMING_LANGUAGE_EN_US, &bare, named, draft)

			So(err, ShouldBeNil)
			So(message, ShouldContainSubstring, `"id":"invoice"`)
			So(message, ShouldContainSubstring, `"id":"inv_order"`)
			So(message, ShouldContainSubstring, `"id":"inv_customer"`)
		})

		Convey("Tests get the server's ids, at most maxTests, and are not checked yet\n", func() {
			out := &guidanceOutput{Guidance: "Use the relation.", OutOfScope: []string{"Parent company?"}}
			for i := range maxTests + 2 {
				out.Tests = append(out.Tests, agentTest{Text: fmt.Sprintf("Orders %d of 10001?", i), Kind: "paraphrase",
					Expect: interfaces.TEST_EXPECT_EXISTS, Slots: []agentSlot{{ConceptID: "c2", Property: "customer_id", Value: "10001"}}})
			}
			out.Tests[1].Text = "  "

			tests, leftOut := buildTests(out, nil, nil, nil)

			So(tests, ShouldHaveLength, maxTests)
			// The blank one, and the one past the most kept.
			So(leftOut, ShouldEqual, 2)
			So(tests[0].ID, ShouldEqual, "t1")
			// The blank one is left out: the second test is the third written.
			So(tests[1].ID, ShouldEqual, "t2")
			So(tests[1].Text, ShouldEqual, "Orders 2 of {{slot 1}}?")
			// No document says 10001: the Agent made it up, so the server fills it.
			So(tests[0].Slots, ShouldResemble, []interfaces.QuestionTestSlot{{ConceptID: "c2", Property: "customer_id", Value: ""}})
			So(tests[0].Check, ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkPending})
		})

		Convey("A value the Agent gives stays only when a document says it\n", func() {
			docs := []*interfaces.RequirementDocument{doc("prd", interfaces.REQUIREMENT_DOC_PRD, "例如客户 １０００１ 的订单。")}
			tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{{Text: "Orders of 10001 for Acme Ltd, {{slot 3}}?", Slots: []agentSlot{
				{ConceptID: "c2", Property: "customer_id", Value: "10001"},
				{ConceptID: "c2", Property: "full_name", Value: "Acme Ltd"},
				{ConceptID: "c2", Property: "customer_id", Value: "  "},
			}}}}, docs, nil, nil)

			So(tests[0].Slots[0].Value, ShouldEqual, "10001")
			So(tests[0].Slots[1].Value, ShouldEqual, "")
			So(tests[0].Slots[2].Value, ShouldEqual, "")
		})

		Convey("A value made up is replaced in the text by its slot, for the server's value to take its place\n", func() {
			tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{{Text: "Orders of 20001 and of Acme?", Slots: []agentSlot{
				{ConceptID: "c2", Property: "customer_id", Value: "20001"},
				{ConceptID: "c2", Property: "full_name", Value: "Acme"},
			}}}}, nil, nil, nil)

			So(tests[0].Text, ShouldEqual, "Orders of {{slot 1}} and of {{slot 2}}?")
			So(tests[0].Slots[0].Value, ShouldEqual, "")
		})

		Convey("A value is replaced only where it stands as a whole, not within another or a slot\n", func() {
			for _, c := range []struct {
				text   string
				values []string
				want   string
			}{
				// The second value is not looked for in the first one's placeholder.
				{"Orders of customer 20001 at level 1?", []string{"20001", "1"}, "Orders of customer {{slot 1}} at level {{slot 2}}?"},
				{"Top 10 orders of customer 1?", []string{"1"}, "Top 10 orders of customer {{slot 1}}?"},
				{"Orders of 1 and of 1?", []string{"1", "1"}, "Orders of {{slot 1}} and of {{slot 2}}?"},
				{"客户20001的订单？", []string{"20001"}, "客户{{slot 1}}的订单？"},
			} {
				at := agentTest{Text: c.text}
				for _, v := range c.values {
					at.Slots = append(at.Slots, agentSlot{ConceptID: "c2", Property: "customer_id", Value: v})
				}

				tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{at}}, nil, nil, nil)

				So(tests[0].Text, ShouldEqual, c.want)
			}
		})

		Convey("A test is left out when its text does not say a made-up value as written, or where an empty slot stands\n", func() {
			tests, leftOut := buildTests(&guidanceOutput{Tests: []agentTest{
				{Text: "Orders in 2024?", Slots: []agentSlot{{ConceptID: "c1", Property: "year", Value: "20"}}},
				{Text: "Orders of customer 10,001?", Slots: []agentSlot{{ConceptID: "c2", Property: "customer_id", Value: "10001"}}},
				// The second value stands only within the first.
				{Text: "Orders of Acme Ltd?", Slots: []agentSlot{{ConceptID: "c2", Property: "full_name", Value: "Acme Ltd"},
					{ConceptID: "c2", Property: "short_name", Value: "Acme"}}},
				{Text: "Orders of a customer?", Slots: []agentSlot{{ConceptID: "c2", Property: "customer_id", Value: ""}}},
				{Text: "Orders of {{slot 1}}?", Slots: []agentSlot{{ConceptID: "c2", Property: "customer_id", Value: " "}}},
			}}, nil, nil, nil)

			So(tests, ShouldHaveLength, 1)
			So(leftOut, ShouldEqual, 4)
			So(tests[0].ID, ShouldEqual, "t1")
			So(tests[0].Text, ShouldEqual, "Orders of {{slot 1}}?")
		})

		Convey("A question with inputs keeps the tests that name them, and a question with none keeps no test\n", func() {
			slot := func(concept, property, value string) agentSlot {
				return agentSlot{ConceptID: concept, Property: property, Value: value}
			}
			out := &guidanceOutput{Tests: []agentTest{
				{Text: "Lines of {{slot 1}}?", Slots: []agentSlot{slot("c1", "order_no", "")}},
				// An answer field as a slot.
				{Text: "Lines of {{slot 1}} for {{slot 2}}?", Slots: []agentSlot{slot("c1", "order_no", ""), slot("c3", "amount", "")}},
				{Text: "Lines of an order?"},
				{Text: "{{slot 1}} and {{slot 2}} are one order?", Expect: interfaces.TEST_EXPECT_SAME_RECORD,
					Slots: []agentSlot{slot("c1", "order_no", ""), slot("c1", "order_date", "")}},
				// The same property twice names nothing a second way.
				{Text: "{{slot 1}} and {{slot 2}} are one order?", Expect: interfaces.TEST_EXPECT_SAME_RECORD,
					Slots: []agentSlot{slot("c1", "order_no", ""), slot("c1", "order_no", "")}},
				{Text: "{{slot 1}} and {{slot 2}} are one order?", Expect: interfaces.TEST_EXPECT_SAME_RECORD,
					Slots: []agentSlot{slot("c1", "order_no", ""), slot("c2", "customer_id", "")}},
				// One other way to name the question's record is enough: the server's test names it too.
				{Text: "The order of {{slot 1}}?", Expect: interfaces.TEST_EXPECT_SAME_RECORD, Slots: []agentSlot{slot("c1", "order_date", "")}},
				{Text: "The order {{slot 1}}?", Expect: interfaces.TEST_EXPECT_SAME_RECORD, Slots: []agentSlot{slot("c1", "order_no", "")}},
			}}
			asked := &interfaces.RequirementQuestion{Inputs: &[]interfaces.RequirementInput{
				{Name: "order", ConceptID: "c1", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_RECORD},
				{Name: "customer", ConceptID: "c2", Property: "customer_id", Role: interfaces.REQUIREMENT_INPUT_RECORD}}}

			tests, leftOut := buildTests(out, nil, asked, nil)
			// After the two the server writes from the template.
			So(tests, ShouldHaveLength, 5)
			So(tests[2].Text, ShouldEqual, "Lines of {{slot 1}}?")
			So(tests[3].Slots[1].Property, ShouldEqual, "order_date")
			So(tests[4].Text, ShouldEqual, "The order of {{slot 1}}?")
			So(leftOut, ShouldEqual, 5)

			tests, leftOut = buildTests(out, nil, &interfaces.RequirementQuestion{Inputs: &[]interfaces.RequirementInput{}}, nil)
			So(tests, ShouldBeEmpty)
			So(leftOut, ShouldEqual, 8)

			// Drafted before inputs: as before, but for the two naming the same record by one value,
			// with no template test of the server's to check them against.
			tests, leftOut = buildTests(out, nil, &interfaces.RequirementQuestion{}, nil)
			So(tests, ShouldHaveLength, 6)
			So(leftOut, ShouldEqual, 2)
		})

		Convey("The server writes the template asked of a record that exists and of one that does not\n", func() {
			asked := &interfaces.RequirementQuestion{Template: "Stock of {item} on {as_of} at {site}?",
				Inputs: &[]interfaces.RequirementInput{
					{Name: "as_of", ConceptID: "c1", Property: "stock_date", Role: interfaces.REQUIREMENT_INPUT_REFERENCE_TIME},
					{Name: "item", ConceptID: "c1", Property: "item_no", Role: interfaces.REQUIREMENT_INPUT_RECORD},
					{Name: "site", ConceptID: "c2", Property: "site_no", Role: interfaces.REQUIREMENT_INPUT_RECORD}}}
			out := &guidanceOutput{Tests: []agentTest{
				{Text: "Is {{slot 1}} unknown?", Kind: interfaces.TEST_KIND_NOT_EXIST, Expect: interfaces.TEST_EXPECT_NOT_EXISTS,
					Slots: []agentSlot{{ConceptID: "c1", Property: "item_no"}}},
				{Text: "How much {{slot 1}} is there?", Kind: "paraphrase", Slots: []agentSlot{{ConceptID: "c1", Property: "item_no"}}},
			}}

			tests, leftOut := buildTests(out, nil, asked, nil)

			So(leftOut, ShouldEqual, 1)
			So(tests, ShouldHaveLength, 3)
			template, absent := tests[0], tests[1]
			So(template.ID, ShouldEqual, "t1")
			So(template.Kind, ShouldEqual, interfaces.TEST_KIND_TEMPLATE)
			So(template.Expect, ShouldEqual, interfaces.TEST_EXPECT_EXISTS)
			So(template.Text, ShouldEqual, "Stock of {{slot 2}} on {{slot 1}} at {{slot 3}}?")
			So(template.Slots, ShouldResemble, []interfaces.QuestionTestSlot{{ConceptID: "c1", Property: "stock_date"},
				{ConceptID: "c1", Property: "item_no"}, {ConceptID: "c2", Property: "site_no"}})
			So(template.ByServer, ShouldBeTrue)
			So(template.Check.Reason, ShouldEqual, checkPending)
			// The first record input names the record that does not exist.
			So(absent.ID, ShouldEqual, "t2")
			So(absent.Kind, ShouldEqual, interfaces.TEST_KIND_NOT_EXIST)
			So(absent.Expect, ShouldEqual, interfaces.TEST_EXPECT_NOT_EXISTS)
			So(absent.Text, ShouldEqual, "Stock of {{slot 1}} on {{slot 2}} at {{slot 3}}?")
			So(absent.Slots[0].Property, ShouldEqual, "item_no")
			So(absent.ByServer, ShouldBeTrue)
			So(tests[2].ID, ShouldEqual, "t3")
			So(tests[2].ByServer, ShouldBeFalse)

			// A {name} no input stands for, as when drafting rejected it: the server writes none.
			stray := &interfaces.RequirementQuestion{Template: "Stock of {item} by {missing}?", Inputs: &[]interfaces.RequirementInput{(*asked.Inputs)[1]}}
			tests, _ = buildTests(&guidanceOutput{}, nil, stray, nil)
			So(tests, ShouldBeEmpty)

			// A period counted back from a time no input gives: what it asks depends on when, so none.
			unbound := &interfaces.RequirementQuestion{Template: "Stock of {item} in the last week?", RelativeWindow: true,
				Inputs: &[]interfaces.RequirementInput{(*asked.Inputs)[1]}}
			tests, _ = buildTests(&guidanceOutput{}, nil, unbound, nil)
			So(tests, ShouldBeEmpty)

			// Only a reference time: no record to make absent.
			onlyTime := &interfaces.RequirementQuestion{Template: "Stock on {as_of}?", Inputs: &[]interfaces.RequirementInput{(*asked.Inputs)[0]}}
			tests, _ = buildTests(&guidanceOutput{}, nil, onlyTime, nil)
			So(tests, ShouldHaveLength, 1)
			So(tests[0].Kind, ShouldEqual, interfaces.TEST_KIND_TEMPLATE)

			// An input with no property: the server cannot tell its field, and the Agent's tests stand.
			unnamed := &interfaces.RequirementQuestion{Template: "Stock of {item}?", Inputs: &[]interfaces.RequirementInput{
				{Name: "item", ConceptID: "c1", Role: interfaces.REQUIREMENT_INPUT_RECORD}}}
			tests, leftOut = buildTests(out, nil, unnamed, nil)
			So(tests, ShouldHaveLength, 2)
			So(tests[0].Kind, ShouldEqual, interfaces.TEST_KIND_NOT_EXIST)
			So(leftOut, ShouldEqual, 0)
		})

		Convey("A question asked about a concept that maps to no object type has no test on it\n", func() {
			asked := &interfaces.RequirementQuestion{Template: "Stock at {site}?", Inputs: &[]interfaces.RequirementInput{
				{Name: "site", ConceptID: "c2", Property: "site_no", Role: interfaces.REQUIREMENT_INPUT_RECORD}}}
			out := &guidanceOutput{Tests: []agentTest{{Text: "Stock held at {{slot 1}}?", Kind: "paraphrase",
				Slots: []agentSlot{{ConceptID: "c2", Property: "site_no"}}}}}
			unmapped := readySpec().Spec

			tests, leftOut := buildTests(out, nil, asked, &unmapped)
			So(tests, ShouldBeEmpty)
			So(leftOut, ShouldEqual, 1)

			unmapped.Concepts[1].MapsTo = &customerRef
			tests, leftOut = buildTests(out, nil, asked, &unmapped)
			So(tests, ShouldHaveLength, 3)
			So(leftOut, ShouldEqual, 0)
		})

		// Two values of one record, so that a test expecting the same record is kept.
		two := []agentSlot{{ConceptID: "c1", Property: "order_no"}, {ConceptID: "c1", Property: "code"}}

		Convey("A test naming the same record by one value is left out unless the server's template test names it\n", func() {
			out := &guidanceOutput{Tests: []agentTest{
				{Text: "Lines of the order named {{slot 1}}?", Kind: "variant", Slots: []agentSlot{{ConceptID: "c1", Property: "name"}}},
				{Text: "Lines of {{slot 1}}, {{slot 2}}?", Kind: "variant", Slots: []agentSlot{{ConceptID: "c1", Property: "name"},
					{ConceptID: "c1", Property: "code"}}},
			}}

			tests, leftOut := buildTests(out, nil, nil, nil)
			So(leftOut, ShouldEqual, 1)
			So(tests, ShouldHaveLength, 1)
			So(tests[0].Slots, ShouldHaveLength, 2)

			asked := &interfaces.RequirementQuestion{Template: "Lines of {order}?", Inputs: &[]interfaces.RequirementInput{
				{Name: "order", ConceptID: "c1", Property: "order_no", Role: interfaces.REQUIREMENT_INPUT_RECORD}}}
			tests, leftOut = buildTests(out, nil, asked, nil)
			So(leftOut, ShouldEqual, 0)
			So(tests, ShouldHaveLength, 4)
			So(tests[2].Slots, ShouldHaveLength, 1)

			// Beside it, a value the documents give is not checked against its record: left out too.
			docs := []*interfaces.RequirementDocument{doc("prd", interfaces.REQUIREMENT_DOC_PRD, "The order named Acme.")}
			given := &guidanceOutput{Tests: []agentTest{{Text: "Lines of the order named Acme?", Kind: "variant",
				Slots: []agentSlot{{ConceptID: "c1", Property: "name", Value: "Acme"}}}}}
			tests, leftOut = buildTests(given, docs, asked, nil)
			So(leftOut, ShouldEqual, 1)
			So(tests, ShouldHaveLength, 2)
		})

		Convey("A test that goes on after its question is left out, unless the template does too\n", func() {
			out := &guidanceOutput{Tests: []agentTest{
				{Text: "Lines of {{slot 1}}? Give the number as written.", Slots: []agentSlot{{ConceptID: "c1", Property: "order_no"}}},
				{Text: "Lines of {{slot 1}}？给出原样的编号。", Slots: []agentSlot{{ConceptID: "c1", Property: "order_no"}}},
				// Spaces after it are nothing.
				{Text: "Lines of {{slot 1}}? ", Slots: []agentSlot{{ConceptID: "c1", Property: "order_no"}}},
				{Text: "List the lines of {{slot 1}}.", Slots: []agentSlot{{ConceptID: "c1", Property: "order_no"}}},
			}}

			tests, leftOut := buildTests(out, nil, &interfaces.RequirementQuestion{Template: "Lines of {order}? "}, nil)
			So(leftOut, ShouldEqual, 2)
			So(tests, ShouldHaveLength, 2)
			So(tests[0].Text, ShouldEqual, "Lines of {{slot 1}}?")

			tests, leftOut = buildTests(out, nil, &interfaces.RequirementQuestion{Template: "Lines of {order}? List each one."}, nil)
			So(leftOut, ShouldEqual, 0)
			So(tests, ShouldHaveLength, 4)

			// Without a question to compare with, it is left out all the same.
			_, leftOut = buildTests(out, nil, nil, nil)
			So(leftOut, ShouldEqual, 2)
		})

		Convey("A test's kind decides what it expects\n", func() {
			tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{
				{Text: "a", Kind: "paraphrase", Expect: interfaces.TEST_EXPECT_SAME_RECORD},
				{Text: "b", Kind: "boundary", Expect: interfaces.TEST_EXPECT_NOT_EXISTS},
				{Text: "c {{slot 1}} {{slot 2}}", Kind: "variant", Expect: interfaces.TEST_EXPECT_EXISTS, Slots: two},
				{Text: "d", Kind: interfaces.TEST_KIND_NOT_EXIST, Expect: interfaces.TEST_EXPECT_EXISTS},
				{Text: "e {{slot 1}} {{slot 2}}", Kind: "", Expect: interfaces.TEST_EXPECT_SAME_RECORD, Slots: two},
			}}, nil, nil, nil)
			So([]string{tests[0].Expect, tests[1].Expect, tests[2].Expect, tests[3].Expect, tests[4].Expect}, ShouldResemble,
				[]string{interfaces.TEST_EXPECT_EXISTS, interfaces.TEST_EXPECT_EXISTS, interfaces.TEST_EXPECT_SAME_RECORD,
					interfaces.TEST_EXPECT_NOT_EXISTS, interfaces.TEST_EXPECT_SAME_RECORD})
		})

		Convey("An expectation the server does not know reads as exists\n", func() {
			tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{{Text: "x", Expect: "maybe"}, {Text: "y {{slot 1}} {{slot 2}}", Expect: interfaces.TEST_EXPECT_SAME_RECORD, Slots: two}}}, nil, nil, nil)

			So(tests[0].Expect, ShouldEqual, interfaces.TEST_EXPECT_EXISTS)
			So(tests[1].Expect, ShouldEqual, interfaces.TEST_EXPECT_SAME_RECORD)
		})
	})
}

func Test_GenerateGuidance(t *testing.T) {
	Convey("Test GenerateGuidance\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		clock := int64(1000)
		svc := &requirementService{ps: ps, ra: ra, da: da, ba: ba,
			now: func() int64 { return clock }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(d time.Duration) { clock += d.Milliseconds() }}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller,
			NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).AnyTimes()
		var finished *interfaces.QuestionGuidance
		expectFinish := func() {
			ra.EXPECT().FinishGenerating(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, g *interfaces.QuestionGuidance) (bool, error) {
					finished = g
					return true, nil
				})
		}

		Convey("Generating runs the Agent as the caller and records the tests and guidance\n", func() {
			spec := readySpec()
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(guidanceDraft(), true, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{
				doc("prd", interfaces.REQUIREMENT_DOC_PRD, "Orders of customer 10001 are asked about.")}, nil)
			ra.EXPECT().StartGenerating(gomock.Any(), gomock.Any(), int64(1000)-agentDeadline.Milliseconds()-time.Minute.Milliseconds()).DoAndReturn(
				func(_ context.Context, g *interfaces.QuestionGuidance, _ int64) (bool, error) {
					So(g.SpecVersion, ShouldEqual, 1)
					So(g.Question, ShouldEqual, "Which orders?")
					So(g.Status, ShouldEqual, interfaces.GUIDANCE_STATUS_GENERATING)
					So(g.Runner, ShouldResemble, caller)
					// Signed from the draft generating read.
					So(g.OntologySignature, ShouldEqual, ontologySignature(guidanceDraft(), spec.Spec.Questions[0]))
					So(g.OntologySignature, ShouldNotBeEmpty)
					return true, nil
				})
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					So(req.AgentID, ShouldEqual, requirementAgentID)
					So(req.Message, ShouldStartWith, "pass guidance\nlanguage: zh-CN\n")
					So(req.ResponseFormat["type"], ShouldEqual, "object")
					reason := req.ResponseFormat["properties"].(map[string]any)["no_tests_reason"].(map[string]any)
					So(reason["description"], ShouldContainSubstring, "otherwise empty")
					return &interfaces.BknAgentTask{TaskID: "t9", Status: interfaces.BKN_AGENT_TASK_PENDING}, nil
				})
			ba.EXPECT().GetTask(gomock.Any(), caller, "t9").Return(&interfaces.BknAgentTask{TaskID: "t9",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
				Output: `{"tests":[{"text":"Orders of 10001?","kind":"paraphrase","expect":"exists","slots":[{"concept_id":"c2","property":"customer_id","value":"10001"}]}],"no_tests_reason":"None fit.","out_of_scope":["Parent company?"],"guidance":"Use the relation."}`}, nil)
			expectFinish()

			g, err := svc.GenerateGuidance(ctx, token, "p1", "q1", caller)

			So(err, ShouldBeNil)
			So(g.Status, ShouldEqual, interfaces.GUIDANCE_STATUS_GENERATING)
			So(finished.Status, ShouldEqual, interfaces.GUIDANCE_STATUS_READY)
			So(finished.RunID, ShouldEqual, "run1")
			So(finished.AgentTaskID, ShouldEqual, "t9")
			So(finished.Tests[0].Text, ShouldEqual, "Orders of 10001?")
			So(finished.Tests[0].Slots[0].Value, ShouldEqual, "10001")
			// Checked before it is recorded: c2 maps to nothing in this specification.
			So(finished.Tests[0].Check, ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED,
				Reason: checkConceptNotMapped})
			So(finished.OutOfScope, ShouldResemble, []string{"Parent company?"})
			So(finished.Guidance, ShouldEqual, "Use the relation.")
			// A reason for writing none does not apply to tests kept.
			So(finished.NoTestsReason, ShouldEqual, "")
			So(finished.LeftOutTests, ShouldEqual, 0)
		})

		Convey("Without tests kept, the Agent's reason is recorded, cut to length, and the tests left out counted\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(readySpec(), true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(guidanceDraft(), true, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(nil, nil)
			ra.EXPECT().StartGenerating(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t9"}, nil)
			reason := strings.Repeat("金", maxNoTestsReason+5)
			ba.EXPECT().GetTask(gomock.Any(), caller, "t9").Return(&interfaces.BknAgentTask{TaskID: "t9",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
				Output: `{"tests":[{"text":"Orders of a customer?","expect":"exists","slots":[{"concept_id":"c2","property":"customer_id","value":""}]}],` +
					`"no_tests_reason":" ` + reason + ` ","out_of_scope":[],"guidance":"g"}`}, nil)
			expectFinish()

			_, err := svc.GenerateGuidance(ctx, token, "p1", "q1", caller)

			So(err, ShouldBeNil)
			So(finished.Tests, ShouldBeEmpty)
			So(finished.LeftOutTests, ShouldEqual, 1)
			So(finished.NoTestsReason, ShouldEqual, reason[:len("金")*maxNoTestsReason])
		})

		Convey("Generating holds the tests to the question's inputs, and to concepts that map to an object type\n", func() {
			spec := readySpec()
			// c2 maps to nothing in this specification.
			spec.Spec.Questions[0].Template = "Orders of {customer}?"
			spec.Spec.Questions[0].Inputs = &[]interfaces.RequirementInput{
				{Name: "customer", ConceptID: "c2", Property: "customer_id", Role: interfaces.REQUIREMENT_INPUT_RECORD}}
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(guidanceDraft(), true, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(nil, nil)
			ra.EXPECT().StartGenerating(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t9"}, nil)
			ba.EXPECT().GetTask(gomock.Any(), caller, "t9").Return(&interfaces.BknAgentTask{TaskID: "t9",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
				Output: `{"tests":[{"text":"Orders of {{slot 1}}?","kind":"paraphrase","expect":"exists","slots":[{"concept_id":"c2","property":"customer_id","value":""}]}],"no_tests_reason":"","out_of_scope":[],"guidance":"g"}`}, nil)
			expectFinish()

			_, err := svc.GenerateGuidance(ctx, token, "p1", "q1", caller)

			So(err, ShouldBeNil)
			So(finished.Tests, ShouldBeEmpty)
			So(finished.LeftOutTests, ShouldEqual, 1)
		})

		Convey("Checking stops in time to record the outcome within the run's lease\n", func() {
			va := pmock.NewMockVegaAccess(ctrl)
			svc.va = va
			spec := readySpec()
			spec.Spec.Concepts[1].MapsTo = &customerRef
			draft := guidanceDraft()
			draft.Items.ObjectTypes[1].Table = interfaces.DraftTable{ResourceID: "r-cust"}
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{
				doc("prd", interfaces.REQUIREMENT_DOC_PRD, "Customers 10001 and 10002 call.")}, nil)
			ra.EXPECT().StartGenerating(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t9"}, nil)
			// The Agent finishes just inside its own limit.
			ba.EXPECT().GetTask(gomock.Any(), caller, "t9").Return(&interfaces.BknAgentTask{TaskID: "t9",
				Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil).Times(int(agentDeadline/pollInterval) - 1)
			ba.EXPECT().GetTask(gomock.Any(), caller, "t9").Return(&interfaces.BknAgentTask{TaskID: "t9",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
				Output: `{"tests":[{"text":"Orders of 10001?","expect":"exists","slots":[{"concept_id":"c2","property":"customer_id","value":"10001"}]},` +
					`{"text":"Orders of 10002?","expect":"exists","slots":[{"concept_id":"c2","property":"customer_id","value":"10002"}]}],"guidance":"g"}`}, nil)
			// The first check takes long; the second would end past the lease.
			va.EXPECT().Query(gomock.Any(), caller, "SELECT COUNT(*) AS n FROM {{r-cust}} WHERE `cust_id` = '10001'", checkQueryTimeout).
				DoAndReturn(func(queryCtx context.Context, _ interfaces.VegaAccount, _ string, _ int) ([]map[string]any, error) {
					// A slow count is cut at the check's deadline, inside the lease.
					deadline, ok := queryCtx.Deadline()
					So(ok, ShouldBeTrue)
					So(deadline.UnixMilli(), ShouldEqual, 1000+(guidanceLease-finishMargin).Milliseconds())
					clock += (guidanceLease - agentDeadline).Milliseconds()
					return []map[string]any{{"n": float64(1)}}, nil
				})
			expectFinish()

			_, err := svc.GenerateGuidance(ctx, token, "p1", "q1", caller)

			So(err, ShouldBeNil)
			So(finished.Tests[0].Check.Outcome, ShouldEqual, interfaces.TEST_CHECK_PASSED)
			So(finished.Tests[1].Check, ShouldResemble, interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED,
				Reason: checkTimeout})
		})

		Convey("The Agent's failures are recorded as codes\n", func() {
			for _, c := range []struct {
				expect func()
				code   string
			}{
				{func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
						Return(nil, &interfaces.BknAgentError{HTTPCode: http.StatusBadRequest})
				}, failureAgentRejected},
				{func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("connection refused"))
				}, failureAgentUnavailable},
				{func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t9",
						Status: interfaces.BKN_AGENT_TASK_FAILED}, nil)
				}, failureAgentFailed},
				{func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t9",
						Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: "not json"}, nil)
				}, failureOutputInvalid},
			} {
				clock = 1000
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(readySpec(), true, nil)
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(guidanceDraft(), true, nil)
				ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(nil, nil)
				ra.EXPECT().StartGenerating(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
				c.expect()
				expectFinish()

				_, err := svc.GenerateGuidance(ctx, token, "p1", "q1", caller)

				So(err, ShouldBeNil)
				So(finished.Status, ShouldEqual, interfaces.GUIDANCE_STATUS_FAILED)
				So(finished.Error, ShouldEqual, c.code)
				So(finished.Tests, ShouldBeEmpty)
			}
		})

		Convey("Generating needs a ready specification with the question, and one run at a time\n", func() {
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(nil, false, nil)
			_, err := svc.GenerateGuidance(ctx, token, "p1", "q1", caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)

			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(&interfaces.Requirement{Status: interfaces.REQUIREMENT_STATUS_DRAFTING}, true, nil)
			_, err = svc.GenerateGuidance(ctx, token, "p1", "q1", caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)

			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(readySpec(), true, nil)
			_, err = svc.GenerateGuidance(ctx, token, "p1", "q9", caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)

			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(readySpec(), true, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(guidanceDraft(), true, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(nil, nil)
			ra.EXPECT().StartGenerating(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
			_, err = svc.GenerateGuidance(ctx, token, "p1", "q1", caller)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Generating)
		})

		Convey("Only the owner generates\n", func() {
			other := pmock.NewMockProjectService(ctrl)
			other.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{
				ID: "p1", Owner: interfaces.AccountInfo{ID: "u2"}}}, nil)
			svc.ps = other

			_, err := svc.GenerateGuidance(ctx, token, "p1", "q1", caller)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
		})

		Convey("FailInterrupted fails guidance a restart left generating too\n", func() {
			ra.EXPECT().FailDrafting(gomock.Any(), failureInterrupted, int64(1000)).Return(int64(0), nil)
			ra.EXPECT().FailGenerating(gomock.Any(), failureInterrupted, int64(1000)).Return(int64(1), nil)

			So(svc.FailInterrupted(ctx), ShouldBeNil)
		})
	})
}

func Test_TestsNote(t *testing.T) {
	Convey("Test what a question's tests show as a whole\n", t, func() {
		check := func(outcome, reason string) *interfaces.QuestionTest {
			return &interfaces.QuestionTest{Check: interfaces.QuestionTestCheck{Outcome: outcome, Reason: reason}}
		}
		unknownConcept := check(interfaces.TEST_CHECK_NOT_CHECKED, checkUnknownConcept)
		unknownProperty := check(interfaces.TEST_CHECK_NOT_CHECKED, checkUnknownProperty)

		So(testsNote([]*interfaces.QuestionTest{unknownConcept, unknownProperty}, nil), ShouldEqual, interfaces.TESTS_NOTE_NONE_CHECKABLE)
		So(testsNote([]*interfaces.QuestionTest{unknownConcept}, nil), ShouldEqual, interfaces.TESTS_NOTE_NONE_CHECKABLE)
		for _, tests := range [][]*interfaces.QuestionTest{
			nil,
			{unknownConcept, check(interfaces.TEST_CHECK_PASSED, "")},
			{unknownConcept, check(interfaces.TEST_CHECK_NOT_CHECKED, checkQueryFailed)},
			{check(interfaces.TEST_CHECK_FAILED, checkRecordNotFound)},
		} {
			So(testsNote(tests, nil), ShouldEqual, "")
		}

		named := func(concepts ...string) *interfaces.QuestionTest {
			t := check(interfaces.TEST_CHECK_PASSED, "")
			for _, c := range concepts {
				t.Slots = append(t.Slots, interfaces.QuestionTestSlot{ConceptID: c})
			}
			return t
		}
		asked := &interfaces.RequirementQuestion{Inputs: &[]interfaces.RequirementInput{{ConceptID: "c1"}, {ConceptID: "c2"}}}
		So(testsNote(nil, asked), ShouldEqual, interfaces.TESTS_NOTE_INPUTS_UNTESTED)
		So(testsNote([]*interfaces.QuestionTest{named("c1"), named("c2")}, asked), ShouldEqual, interfaces.TESTS_NOTE_INPUTS_UNTESTED)
		So(testsNote([]*interfaces.QuestionTest{named("c1"), named("c2", "c1")}, asked), ShouldEqual, "")
		So(testsNote(nil, &interfaces.RequirementQuestion{Inputs: &[]interfaces.RequirementInput{}}), ShouldEqual, "")
		So(testsNote(nil, &interfaces.RequirementQuestion{}), ShouldEqual, "")
	})
}

func Test_GetGuidance(t *testing.T) {
	Convey("Test GetGuidance\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()

		Convey("The ontology changed when the needed items sign otherwise now; unknown without a signature or a draft\n", func() {
			spec := readySpec()
			spec.CreateTime = 10
			signed := func(signature string) *interfaces.QuestionGuidance {
				return &interfaces.QuestionGuidance{ProjectID: "p1", QuestionID: "q1", SpecVersion: 1, Question: "Which orders?",
					Status: interfaces.GUIDANCE_STATUS_READY, CreateTime: 20, OntologySignature: signature}
			}
			now := ontologySignature(guidanceDraft(), spec.Spec.Questions[0])
			for _, c := range []struct {
				g       *interfaces.QuestionGuidance
				draft   bool
				changed any
			}{
				{signed(now), true, false},
				{signed("0000"), true, true},
				{signed(""), true, nil},
				{signed(now), false, nil},
			} {
				ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(c.g, true, nil)
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)
				if c.g.OntologySignature != "" {
					da.EXPECT().GetDraft(gomock.Any(), "p1").Return(guidanceDraft(), c.draft, nil)
				}

				view, err := svc.GetGuidance(ctx, token, "p1", "q1")

				So(err, ShouldBeNil)
				// Only a hint: it leaves current as it is.
				So(view.Current, ShouldBeTrue)
				if c.changed == nil {
					So(view.OntologyChanged, ShouldBeNil)
				} else {
					So(*view.OntologyChanged, ShouldEqual, c.changed)
				}
			}
		})

		// Guidance generated at 20, for a specification drafted at 10 unless said otherwise.
		stored := func(version int, question string) *interfaces.QuestionGuidance {
			return &interfaces.QuestionGuidance{ProjectID: "p1", QuestionID: "q1", SpecVersion: version, Question: question,
				Status: interfaces.GUIDANCE_STATUS_READY, CreateTime: 20}
		}
		drafted := func(at int64) *interfaces.Requirement {
			spec := readySpec()
			spec.CreateTime = at
			return spec
		}

		Convey("Guidance applies while the specification version and the question are as generated for\n", func() {
			for _, c := range []struct {
				g       *interfaces.QuestionGuidance
				spec    *interfaces.Requirement
				current bool
			}{
				{stored(1, "Which orders?"), drafted(10), true},
				{stored(1, "Which orders of last month?"), drafted(10), false},
				{stored(0, "Which orders?"), drafted(10), false},
				// Drafted again after the guidance, to the same version and question: its ids may name others now.
				{stored(1, "Which orders?"), drafted(30), false},
			} {
				ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(c.g, true, nil)
				ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(c.spec, true, nil)

				view, err := svc.GetGuidance(ctx, token, "p1", "q1")
				So(err, ShouldBeNil)
				So(view.Current, ShouldEqual, c.current)
			}
		})

		Convey("Reading notes when no test could be checked, from the tests as they are\n", func() {
			g := stored(1, "Which orders?")
			g.Tests = []*interfaces.QuestionTest{{ID: "t1", Check: interfaces.QuestionTestCheck{
				Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkUnknownConcept}}}
			spec := readySpec()
			spec.CreateTime = 10
			ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(g, true, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)

			view, err := svc.GetGuidance(ctx, token, "p1", "q1")

			So(err, ShouldBeNil)
			So(view.TestsNote, ShouldEqual, interfaces.TESTS_NOTE_NONE_CHECKABLE)
		})

		Convey("Reading notes a question whose inputs no test names, from the question as it is\n", func() {
			spec := readySpec()
			spec.CreateTime = 10
			spec.Spec.Questions[0].Inputs = &[]interfaces.RequirementInput{{Name: "order", ConceptID: "c1"}}
			ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(stored(1, "Which orders?"), true, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(spec, true, nil)

			view, err := svc.GetGuidance(ctx, token, "p1", "q1")

			So(err, ShouldBeNil)
			So(view.TestsNote, ShouldEqual, interfaces.TESTS_NOTE_INPUTS_UNTESTED)
		})

		Convey("A question with no test questions generated is 404\n", func() {
			ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(nil, false, nil)

			_, err := svc.GetGuidance(ctx, token, "p1", "q1")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)
		})
	})
}
