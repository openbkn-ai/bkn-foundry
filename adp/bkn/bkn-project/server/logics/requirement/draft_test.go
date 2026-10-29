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

		Convey("A question's layer is knowledge only when said so\n", func() {
			out := &draftOutput{Questions: []agentQuestion{
				{Text: "a", Layer: "knowledge", SupplementReason: "r"},
				{Text: "b", Layer: "capability", SupplementReason: "r"},
				{Text: "c", Layer: "whatever", SupplementReason: "r"},
			}}

			spec, _ := buildSpec(out, docs, draft)

			So(spec.Questions[0].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_KNOWLEDGE)
			So(spec.Questions[1].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
			So(spec.Questions[2].Layer, ShouldEqual, interfaces.REQUIREMENT_LAYER_CAPABILITY)
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
					So(req.Message, ShouldContainSubstring, `{"id":"sales_order","name":"Sales order","aliases":["Order"]}`)
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
