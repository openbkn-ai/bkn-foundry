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

// extractedFrom is what buildConcepts adds to an empty specification, and the document types it
// suggests.
func extractedFrom(out *conceptsOutput, docs map[string]*interfaces.RequirementDocument,
	draft *interfaces.Draft) (*interfaces.RequirementSpec, map[string]string) {
	spec := emptySpec()
	types := buildConcepts(out, docs, draft, spec)
	return spec, types
}

func Test_BuildConcepts(t *testing.T) {
	Convey("Test buildConcepts\n", t, func() {
		docs := documentsByID(testDocuments())
		draft := testDraft()
		run := interfaces.REQUIREMENT_RUN_CONCEPTS

		Convey("A concept quotes the documents, of a category the run judges, mapped only to an adopted item\n", func() {
			customer := interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "customer"}
			out := &conceptsOutput{Concepts: []agentConcept{
				{ID: "a", Term: "Agreed date", Category: "property", Citations: []agentCitation{cite("prd", "updates the agreed date")}},
				{ID: "b", Term: "Late reply rate", Category: interfaces.REQUIREMENT_CONCEPT_KINETIC,
					Citations: []agentCitation{cite("pol", "within 2 working days")}},
				{ID: "c", Term: "Client", Category: "object", MapsTo: &customer, Citations: []agentCitation{cite("prd", "a customer's request")}},
				{ID: "d", Term: "Carrier", Category: "object", Citations: []agentCitation{cite("prd", "a customer's request")},
					MapsTo: &interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: "carrier"}},
				// From the documents, a reason does not stand in for a quote.
				{Term: "Invented", Category: "object", SupplementReason: "needed"},
				{Term: "Gadget", Category: "gadget", Citations: []agentCitation{cite("prd", "a customer's request")}},
			}}

			spec, _ := extractedFrom(out, docs, draft)

			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c1", "c2", "c3", "c4"})
			for _, c := range spec.Concepts {
				So(c.Source, ShouldEqual, interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS)
			}
			So(spec.Concepts[1].Category, ShouldEqual, interfaces.REQUIREMENT_CONCEPT_KINETIC)
			So(spec.Concepts[0].Citations[0].BlockID, ShouldEqual, "b2")
			So(spec.Concepts[2].MapsTo, ShouldResemble, &customer)
			So(spec.Concepts[3].MapsTo, ShouldBeNil)
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedConcept, Text: "Invented", Reason: rejectedQuoteNotFound, Run: run},
				{Kind: rejectedConcept, Text: "Gadget", Reason: rejectedUnknownKind, Run: run},
			})
		})

		Convey("At most maxSpecConcepts new concepts are kept\n", func() {
			out := &conceptsOutput{}
			for i := range maxSpecConcepts + 1 {
				out.Concepts = append(out.Concepts, agentConcept{Term: fmt.Sprintf("t%d", i), Category: "object",
					Citations: []agentCitation{cite("prd", "a customer's request")}})
			}

			spec, _ := extractedFrom(out, docs, draft)

			So(spec.Concepts, ShouldHaveLength, maxSpecConcepts)
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedConcept, Text: fmt.Sprintf("t%d", maxSpecConcepts), Reason: rejectedTooMany, Run: run}})
		})

		Convey("A rule needs a quote, and its numbers must be in it\n", func() {
			out := &conceptsOutput{Rules: []agentRule{
				{Statement: "Answer within 2 working days", CompilesTo: "metric_definition",
					Citations: []agentCitation{cite("pol", "within 2 working days")}},
				{Statement: "Answer within 3 working days", Citations: []agentCitation{cite("pol", "within 2 working days")}},
				{Statement: "Only signed changes count"},
			}}

			spec, _ := extractedFrom(out, docs, draft)

			So(spec.Rules, ShouldHaveLength, 1)
			So(spec.Rules[0].ID, ShouldEqual, "r1")
			So(spec.Rules[0].CompilesTo, ShouldEqual, "metric_definition")
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedRule, Text: "Answer within 3 working days", Reason: rejectedNumberNotInQuote, Run: run},
				{Kind: rejectedRule, Text: "Only signed changes count", Reason: rejectedQuoteNotFound, Run: run},
			})
		})

		Convey("A conflict keeps the sides found and needs two documents\n", func() {
			out := &conceptsOutput{Conflicts: []agentConflict{
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

			spec, _ := extractedFrom(out, docs, draft)

			So(spec.Conflicts, ShouldHaveLength, 1)
			k := spec.Conflicts[0]
			So(k.ID, ShouldEqual, "k1")
			So(k.Sides, ShouldHaveLength, 2)
			So(k.Sides[1].Position, ShouldEqual, "signed only")
			So(k.Sides[1].BlockID, ShouldEqual, "b1")
			So(k.Resolution, ShouldBeNil)
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedConflict, Text: "One-sided", Reason: rejectedQuoteNotFound, Run: run},
			})
		})

		Convey("A choice needs a quote and two options; a recommendation outside them is dropped\n", func() {
			options := []agentOption{{ID: "a", Label: "As an event"}, {ID: "b", Label: "As a property"}}
			out := &conceptsOutput{Choices: []agentChoice{
				{Question: "Model date changes?", Options: options, RecommendedOptionID: "a",
					Citations: []agentCitation{cite("pol", "only a signed change does")}},
				{Question: "Odd recommendation?", Options: options, RecommendedOptionID: "z",
					Citations: []agentCitation{cite("pol", "only a signed change does")}},
				{Question: "One option?", Options: options[:1], Citations: []agentCitation{cite("pol", "only a signed change does")}},
				{Question: "No quote?", Options: options},
			}}

			spec, _ := extractedFrom(out, docs, draft)

			So(spec.Choices, ShouldHaveLength, 2)
			So(spec.Choices[0].ID, ShouldEqual, "f1")
			So(spec.Choices[0].Options[0].ID, ShouldEqual, "o1")
			So(spec.Choices[0].RecommendedOptionID, ShouldEqual, "o1")
			So(spec.Choices[1].RecommendedOptionID, ShouldEqual, "")
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedChoice, Text: "One option?", Reason: rejectedTooFewOptions, Run: run},
				{Kind: rejectedChoice, Text: "No quote?", Reason: rejectedQuoteNotFound, Run: run},
			})
		})

		Convey("Document types the agent suggests are kept only for known documents and types\n", func() {
			out := &conceptsOutput{DocTypes: []agentDocType{
				{DocumentID: "prd", DocType: interfaces.REQUIREMENT_DOC_PRD},
				{DocumentID: "pol", DocType: "manual"},
				{DocumentID: "gone", DocType: interfaces.REQUIREMENT_DOC_POLICY},
				// Skill documents and API descriptions are a person's to set.
				{DocumentID: "pol", DocType: interfaces.REQUIREMENT_DOC_SKILL},
				{DocumentID: "pol", DocType: interfaces.REQUIREMENT_DOC_API},
			}}

			_, types := extractedFrom(out, docs, draft)

			So(types, ShouldResemble, map[string]string{"prd": interfaces.REQUIREMENT_DOC_PRD})
		})

		Convey("Needs the documents state quote them and name the concepts by the run's ids\n", func() {
			out := &conceptsOutput{
				Concepts: []agentConcept{{ID: "rate", Term: "Late reply rate", Category: interfaces.REQUIREMENT_CONCEPT_KINETIC,
					Citations: []agentCitation{cite("pol", "within 2 working days")}}},
				LogicNeeds: []agentLogicNeed{
					{ID: "x", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late reply rate", Definition: "replies over 2 working days",
						ConceptIDs: []string{"rate", "nope", "rate"}, SuggestedMetricType: "atomic",
						Citations: []agentCitation{cite("pol", "within 2 working days")}},
					// From the documents, a reason does not stand in for a quote.
					{Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Signed check", SupplementReason: "needed"},
				},
			}

			spec, _ := extractedFrom(out, docs, draft)

			So(spec.LogicNeeds, ShouldHaveLength, 1)
			n := spec.LogicNeeds[0]
			So(n.ID, ShouldEqual, "n1")
			So(n.Source, ShouldEqual, interfaces.LOGIC_NEED_FROM_DOCUMENTS)
			So(n.ConceptIDs, ShouldResemble, []string{"c1"})
			So(n.SuggestedMetricType, ShouldEqual, "atomic")
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Kind: rejectedLogicNeed, Text: "Signed check", Reason: rejectedQuoteNotFound, Run: run}})
		})

		Convey("What is kept stays as it is: repeats are merged into it, and the run lists its own rejections anew\n", func() {
			spec := emptySpec()
			spec.Concepts = []*interfaces.RequirementConcept{{ID: "c3", Term: "Agreed date", Category: "property", Edited: true}}
			spec.Rules = []*interfaces.RequirementRule{{ID: "r2", Statement: "Only signed changes count", Edited: true}}
			spec.Conflicts = []*interfaces.RequirementConflict{{ID: "k1", Topic: "Email confirmation", Deferred: true}}
			spec.Choices = []*interfaces.RequirementChoice{{ID: "f1", Question: "Model date changes?",
				Decision: &interfaces.RequirementChoiceDecision{OptionID: "o1"}}}
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{{ID: "n4", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late reply rate",
				ConceptIDs: []string{}, Edited: true}}
			spec.Rejected = []*interfaces.RequirementRejected{{Text: "old", Run: run}, {Text: "drafting's"}}
			spec.LastIDs = map[string]int{"c": 5}
			quote := []agentCitation{cite("pol", "only a signed change does")}
			out := &conceptsOutput{
				Concepts: []agentConcept{{ID: "a", Term: " agreed DATE ", Category: "object", Citations: quote},
					{ID: "b", Term: "Signed change", Category: "event", Citations: quote}},
				Rules:     []agentRule{{Statement: "only signed changes count", Citations: quote}},
				Conflicts: []agentConflict{{Topic: "email confirmation"}},
				Choices:   []agentChoice{{Question: "model date changes?"}},
				LogicNeeds: []agentLogicNeed{{Kind: interfaces.LOGIC_NEED_METRIC, Name: "late reply rate", ConceptIDs: []string{"a", "b", "kept-c3"},
					Citations: quote}},
			}

			buildConcepts(out, docs, draft, spec)

			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c3", "c6"})
			So(spec.Concepts[0].Category, ShouldEqual, "property")
			So(ids(spec.Rules, ruleID), ShouldResemble, []string{"r2"})
			So(ids(spec.Conflicts, conflictID), ShouldResemble, []string{"k1"})
			So(ids(spec.Choices, choiceID), ShouldResemble, []string{"f1"})
			So(ids(spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n4"})
			So(spec.LogicNeeds[0].ConceptIDs, ShouldResemble, []string{"c3", "c6"})
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{{Text: "drafting's"}})
		})

		Convey("Concepts are mapped to the model by name after they are extracted\n", func() {
			out := &conceptsOutput{Concepts: []agentConcept{{Term: "Order", Category: "object",
				Citations: []agentCitation{cite("prd", "a customer's request")}}}}

			spec, _ := extractedFrom(out, docs, draft)
			So(spec.Concepts[0].MapsTo, ShouldResemble, &orderRef)

			spec, _ = extractedFrom(out, docs, nil)
			So(spec.Concepts[0].MapsTo, ShouldBeNil)
		})
	})
}

func Test_ConceptsResponseFormatIsValidJSONSchema(t *testing.T) {
	Convey("The response formats of pass concepts and pass kinetic are objects requiring their own properties", t, func() {
		for _, format := range []map[string]any{conceptsResponseFormat(), kineticResponseFormat()} {
			body, err := json.Marshal(format)
			So(err, ShouldBeNil)
			var schema map[string]any
			So(json.Unmarshal(body, &schema), ShouldBeNil)
			So(schema["type"], ShouldEqual, "object")
			props := schema["properties"].(map[string]any)
			for _, name := range schema["required"].([]any) {
				So(props, ShouldContainKey, name)
			}
			So(string(body), ShouldNotContainSubstring, "null")
		}
	})
}

func Test_ExtractConcepts(t *testing.T) {
	Convey("Test the concepts run\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		da := pmock.NewMockDraftAccess(ctrl)
		va := pmock.NewMockVegaAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, va: va, ba: ba,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", Owner: caller,
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}}
		docs := testDocuments()
		docs[1].DocType = interfaces.REQUIREMENT_DOC_OTHER
		cites := []interfaces.RequirementCitation{{DocumentID: "prd", Quote: "q"}}
		// previous has a scenario side drafted earlier, and concepts, rules, conflicts, choices and
		// needs extracted earlier, some made a person's.
		previous := func() *interfaces.Requirement {
			return &interfaces.Requirement{ProjectID: "p1", Version: 2, Status: interfaces.REQUIREMENT_STATUS_READY, UpdateTime: 7,
				Spec: interfaces.RequirementSpec{
					Scenarios: []*interfaces.RequirementScenario{{ID: "s1", Name: "Answer", Citations: cites}},
					Questions: []*interfaces.RequirementQuestion{{ID: "q1", Text: "When?", ScenarioIDs: []string{"s1"},
						Basis: interfaces.RequirementBasis{ConceptIDs: []string{"c2"}}, LogicNeedIDs: []string{"n2"},
						Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY}},
					Concepts: []*interfaces.RequirementConcept{
						{ID: "c1", Term: "Order", Category: "object", Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS},
						{ID: "c2", Term: "Agreed date", Category: "property", Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS},
						{ID: "c3", Term: "Signed change", Category: "event", Source: interfaces.REQUIREMENT_CONCEPT_FROM_DOCUMENTS, Edited: true},
						{ID: "c4", Term: "Reminder", Category: "event", Source: interfaces.REQUIREMENT_CONCEPT_FROM_SCENARIOS},
					},
					Rules: []*interfaces.RequirementRule{{ID: "r1", Statement: "Only signed changes count", Edited: true},
						{ID: "r2", Statement: "Old rule"}},
					Conflicts: []*interfaces.RequirementConflict{{ID: "k1", Topic: "Grade", Deferred: true}, {ID: "k2", Topic: "Leave"}},
					Choices: []*interfaces.RequirementChoice{{ID: "f1", Question: "Team?", Decision: &interfaces.RequirementChoiceDecision{OptionID: "o1"}},
						{ID: "f2", Question: "Site?"}},
					LogicNeeds: []*interfaces.RequirementLogicNeed{
						{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Old metric", Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS},
						{ID: "n2", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Agreed date age", Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS,
							ConceptIDs: []string{"c2"}},
						{ID: "n3", Kind: interfaces.LOGIC_NEED_API, Name: "ERP lookup", Source: interfaces.LOGIC_NEED_FROM_ANALYSIS},
					},
					Rejected: []*interfaces.RequirementRejected{{Text: "old", Run: interfaces.REQUIREMENT_RUN_CONCEPTS}, {Text: "drafting's"}},
					LastIDs:  map[string]int{"s": 1, "c": 4, "q": 1, "r": 2, "k": 2, "f": 2, "n": 3},
				}}
		}
		output := `{"concepts":[
				{"id":"a","term":"Order","category":"object","citations":[{"document_id":"prd","quote":"the current date comes from"}]},
				{"id":"b","term":"agreed date","category":"property","citations":[{"document_id":"prd","quote":"updates the agreed date"}]},
				{"id":"k","term":"Late reply rate","category":"kinetic","citations":[{"document_id":"pol","quote":"within 2 working days"}]}],
			"rules":[{"statement":"Answer within 2 working days","compiles_to":"metric_definition","citations":[{"document_id":"pol","quote":"within 2 working days"}]},
				{"statement":"only signed changes count","compiles_to":"constraint","citations":[{"document_id":"pol","quote":"only a signed change does"}]}],
			"conflicts":[{"topic":"Email confirmation","kind":"rule","sides":[
				{"document_id":"prd","quote":"confirmed by email updates the agreed date","position":"email"},
				{"document_id":"pol","quote":"Requests by email do not change the agreed date","position":"signed"}]}],
			"choices":[{"question":"Model date changes?","options":[{"id":"a","label":"Event","consequence":""},{"id":"b","label":"Property","consequence":""}],
				"recommended_option_id":"a","reason":"r","citations":[{"document_id":"pol","quote":"only a signed change does"}]}],
			"doc_types":[{"document_id":"pol","doc_type":"policy"},{"document_id":"prd","doc_type":"standard"}],
			"logic_needs":[{"id":"x","kind":"metric","name":"Late reply rate","definition":"replies later than 2 working days","concept_ids":["k","b"],
				"citations":[{"document_id":"pol","quote":"within 2 working days"}]},
				{"id":"y","kind":"metric","name":"agreed date AGE","definition":"","concept_ids":["a"],"citations":[]}]}`
		expectRun := func(st *runStore) {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(testDraft(), true, nil)
			st.expect(ra)
			va.EXPECT().ListTables(gomock.Any(), caller, "c1").Return([]*interfaces.VegaTable{{Name: "orders"}}, nil)
		}

		Convey("It extracts the documents' items around what is kept, and leaves the scenario side as it was\n", func() {
			st := &runStore{req: previous()}
			expectRun(st)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					So(req.AgentID, ShouldEqual, requirementAgentID)
					So(req.Message, ShouldStartWith, "pass concepts\nlanguage: en-US\n")
					var msg conceptsMessage
					So(json.Unmarshal([]byte(req.Message[strings.Index(req.Message, "{"):]), &msg), ShouldBeNil)
					So(msg.Documents, ShouldHaveLength, 2)
					So(msg.Tables, ShouldResemble, []messageTable{{Name: "orders"}})
					// The model, when there is one, only gives concepts items to map to: no properties.
					So(msg.ObjectTypes, ShouldHaveLength, 2)
					// Kept items are named by prefixed ids, not to be extracted again.
					kept := []string{}
					for _, c := range msg.Kept.Concepts {
						kept = append(kept, c.ID)
					}
					So(kept, ShouldResemble, []string{"kept-c2", "kept-c3", "kept-c4"})
					So(msg.Kept.Rules, ShouldResemble, []messageRule{{Statement: "Only signed changes count"}})
					So(msg.Kept.LogicNeeds, ShouldHaveLength, 2)
					So(msg.Kept.LogicNeeds[0].ID, ShouldEqual, "kept-n2")
					So(req.ResponseFormat["required"], ShouldContain, "logic_needs")
					return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil
				})
			// Only a document still typed other takes the suggested type.
			ra.EXPECT().SuggestDocumentType(gomock.Any(), "p1", "pol", interfaces.REQUIREMENT_DOC_POLICY, int64(1000)).Return(true, nil)

			req, err := svc.ExtractConcepts(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(st.readUpdateTime, ShouldEqual, 7)
			So(st.started.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_RUNNING)
			So(st.started.By, ShouldResemble, caller)
			So(req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
			spec := st.req.Spec
			run := spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
			So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
			So(run.AgentTaskID, ShouldEqual, "t1")
			So(run.Inputs.Documents, ShouldResemble, []interfaces.RequirementInputDocument{
				{ID: "pol", DocType: interfaces.REQUIREMENT_DOC_POLICY}, {ID: "prd", DocType: interfaces.REQUIREMENT_DOC_PRD}})
			So(st.req.UpdateTime, ShouldEqual, 1000)
			So(st.req.Version, ShouldEqual, 2)
			// The scenario side is drafting's: untouched.
			So(ids(spec.Scenarios, scenarioID), ShouldResemble, []string{"s1"})
			So(ids(spec.Questions, questionID), ShouldResemble, []string{"q1"})
			// c1 no one made theirs is extracted anew as c5, now mapped to the model by name; c2 a
			// question rests on, c3 changed by a person and c4 drafting's stay.
			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c2", "c3", "c4", "c5", "c6"})
			So(spec.Concepts[3].Term, ShouldEqual, "Order")
			So(spec.Concepts[3].MapsTo, ShouldResemble, &orderRef)
			So(ids(spec.Rules, ruleID), ShouldResemble, []string{"r1", "r3"})
			So(ids(spec.Conflicts, conflictID), ShouldResemble, []string{"k1", "k3"})
			// Conflicts are suggested by the types the documents have now.
			So(spec.Conflicts[1].SuggestedDocumentID, ShouldEqual, "pol")
			So(ids(spec.Choices, choiceID), ShouldResemble, []string{"f1", "f3"})
			// n1 goes, n2 a question rests on stays and takes the concepts the repeat names, n3 is the
			// kinetic run's.
			So(ids(spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2", "n3", "n4"})
			So(spec.LogicNeeds[0].ConceptIDs, ShouldResemble, []string{"c2", "c5"})
			So(spec.LogicNeeds[2].ConceptIDs, ShouldResemble, []string{"c6", "c2"})
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{{Text: "drafting's"}})
		})

		Convey("Starting over keeps only what questions rest on of the extracted items\n", func() {
			st := &runStore{req: previous()}
			expectRun(st)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{}`}, nil)

			_, err := svc.ExtractConcepts(ctx, token, "p1", caller, true)

			So(err, ShouldBeNil)
			spec := st.req.Spec
			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c2", "c4"})
			So(spec.Rules, ShouldBeEmpty)
			So(spec.Conflicts, ShouldBeEmpty)
			So(spec.Choices, ShouldBeEmpty)
			So(ids(spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2", "n3"})
			So(ids(spec.Questions, questionID), ShouldResemble, []string{"q1"})
		})

		Convey("A run that fails leaves the specification as it was, the run failed with why\n", func() {
			st := &runStore{req: previous()}
			expectRun(st)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("connection refused"))

			_, err := svc.ExtractConcepts(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			run := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS]
			So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_FAILED)
			So(run.Error, ShouldEqual, failureAgentUnavailable)
			So(run.Inputs, ShouldBeNil)
			want := previous().Spec
			So(ids(st.req.Spec.Concepts, conceptID), ShouldResemble, ids(want.Concepts, conceptID))
			So(ids(st.req.Spec.Rules, ruleID), ShouldResemble, ids(want.Rules, ruleID))
			So(ids(st.req.Spec.LogicNeeds, logicNeedID), ShouldResemble, ids(want.LogicNeeds, logicNeedID))
			So(st.req.Spec.Rejected, ShouldHaveLength, 2)
		})

		Convey("A project without a specification gets one with the concepts extracted, at version 1\n", func() {
			st := &runStore{}
			expectRun(st)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil)
			ra.EXPECT().SuggestDocumentType(gomock.Any(), "p1", "pol", interfaces.REQUIREMENT_DOC_POLICY, int64(1000)).Return(false, nil)

			_, err := svc.ExtractConcepts(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(st.readUpdateTime, ShouldEqual, 0)
			So(st.req.Version, ShouldEqual, 1)
			So(ids(st.req.Spec.Concepts, conceptID), ShouldResemble, []string{"c1", "c2", "c3"})
			// The owner typed pol meanwhile: the run read it as other.
			So(st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_CONCEPTS].Inputs.Documents[0].DocType, ShouldEqual, interfaces.REQUIREMENT_DOC_OTHER)
		})

		Convey("It needs documents, and does not start while another run goes or over a changed specification\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil).Times(3)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return([]*interfaces.RequirementDocument{}, nil)
			_, err := svc.ExtractConcepts(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Requirement_NoDocuments)

			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil).Times(2)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(nil, false, nil).Times(2)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(previous(), true, nil).Times(2)
			ra.EXPECT().StartRun(gomock.Any(), "p1", interfaces.REQUIREMENT_RUN_CONCEPTS, gomock.Any(),
				int64(1000)-2*agentDeadline.Milliseconds()-time.Minute.Milliseconds(), int64(7)).Return(false, nil)
			ra.EXPECT().StartRun(gomock.Any(), "p1", interfaces.REQUIREMENT_RUN_CONCEPTS, gomock.Any(), gomock.Any(), int64(7)).
				Return(false, interfaces.ErrRequirementChanged)

			_, err = svc.ExtractConcepts(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
			_, err = svc.ExtractConcepts(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Stale)
		})

		Convey("Only the owner extracts concepts\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{
				ID: "p1", Owner: interfaces.AccountInfo{ID: "u2"}}}, nil)

			_, err := svc.ExtractConcepts(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusForbidden, perrors.BknProject_Requirement_NotOwner)
		})
	})
}
