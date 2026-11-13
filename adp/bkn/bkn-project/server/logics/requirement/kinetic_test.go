// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
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

// kineticDraft is the confirmed test model with properties, and a candidate no one adopted.
func kineticDraft() *interfaces.Draft {
	draft := confirmedDraft()
	draft.Items.ObjectTypes[0].Properties = []*interfaces.DraftProperty{{Name: "agreed_on", Type: "date"}, {Name: "amount", Type: "decimal"}}
	draft.Items.ObjectTypes = append(draft.Items.ObjectTypes, &interfaces.DraftObjectType{ID: "carrier", Name: "Carrier",
		Properties: []*interfaces.DraftProperty{{Name: "code", Type: "string"}}})
	return draft
}

func Test_AddLogicNeeds(t *testing.T) {
	Convey("Test addLogicNeeds\n", t, func() {
		docs := documentsByID(testDocuments())
		draft := kineticDraft()
		concepts := map[string]string{"c1": "c1", "kept-c1": "c1", "c2": "c2"}
		var rejected []string
		reject := func(kind, text, reason string) { rejected = append(rejected, kind+" "+text+" "+reason) }
		quote := []agentCitation{cite("pol", "within 2 working days")}

		Convey("An analysed need quotes the documents or says why it is needed, and its numbers are its quotes'\n", func() {
			rejected = nil
			spec := emptySpec()
			proposed := []agentLogicNeed{
				{Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late reply rate", Definition: "replies after 2 working days", Citations: quote,
					ConceptIDs: []string{"kept-c1", "c1", "c9"}, SuggestedMetricType: "derived"},
				{Kind: interfaces.LOGIC_NEED_API, Name: "ERP lookup", SupplementReason: "dates live in the ERP"},
				{Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Escalate"},
				{Kind: interfaces.LOGIC_NEED_METRIC, Name: "Very late", Definition: "replies after 3 working days", Citations: quote},
				{Kind: "dashboard", Name: "Board", SupplementReason: "r"},
				// A Skill must be one the documents name; a function need not be.
				{Kind: interfaces.LOGIC_NEED_SKILL, Name: "Date lookup Skill", SupplementReason: "r"},
				{Kind: interfaces.LOGIC_NEED_SKILL, Name: "signed_change", SupplementReason: "r"},
				{Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "window check", SupplementReason: "r", SuggestedMetricType: "atomic"},
				// A blank name is no need, nor listed.
				{Kind: interfaces.LOGIC_NEED_METRIC, Name: "  ", SupplementReason: "r"},
			}

			addLogicNeeds(spec, proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, concepts, reject)

			got := []string{}
			for _, n := range spec.LogicNeeds {
				got = append(got, n.ID+" "+n.Name)
				So(n.Source, ShouldEqual, interfaces.LOGIC_NEED_FROM_ANALYSIS)
			}
			So(got, ShouldResemble, []string{"n1 Late reply rate", "n2 ERP lookup", "n3 signed_change", "n4 window check"})
			So(spec.LogicNeeds[0].ConceptIDs, ShouldResemble, []string{"c1"})
			So(spec.LogicNeeds[0].SuggestedMetricType, ShouldEqual, "derived")
			So(spec.LogicNeeds[0].Citations[0].DocumentID, ShouldEqual, "pol")
			So(spec.LogicNeeds[1].SupplementReason, ShouldEqual, "dates live in the ERP")
			// Only a metric suggests a metric type.
			So(spec.LogicNeeds[3].SuggestedMetricType, ShouldEqual, "")
			So(rejected, ShouldResemble, []string{
				"logic_need Escalate " + rejectedQuoteNotFound,
				"logic_need Very late " + rejectedNumberNotInQuote,
				"logic_need Board " + rejectedUnknownKind,
				"logic_need Date lookup Skill " + rejectedBasisNotInDocuments,
			})
		})

		Convey("An action type is executed by the api or mcp_tool need the Agent names, of its output or kept\n", func() {
			rejected = nil
			spec := emptySpec()
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				{ID: "n1", Kind: interfaces.LOGIC_NEED_MCP_TOOL, Name: "Mail tool", Source: interfaces.LOGIC_NEED_FROM_PERSON},
				{ID: "n2", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order", Source: interfaces.LOGIC_NEED_FROM_PERSON,
					Edited: true},
			}
			spec.LastIDs = map[string]int{"n": 2}
			proposed := []agentLogicNeed{
				{ID: "a1", Kind: interfaces.LOGIC_NEED_API, Name: "Reminder service", SupplementReason: "r"},
				{ID: "a2", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Send reminder", SupplementReason: "r", ExecutedBy: "a1"},
				{ID: "a3", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Mail customer", SupplementReason: "r", ExecutedBy: "kept-n1"},
				// Only an api or mcp_tool need executes, and only an action type is executed.
				{ID: "a4", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Close order", SupplementReason: "r", ExecutedBy: "a2"},
				{ID: "a5", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check", SupplementReason: "r", ExecutedBy: "a1"},
				{ID: "a6", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Cancel order", SupplementReason: "r", ExecutedBy: "a9"},
				// A need a person changed keeps what they said.
				{ID: "kept-n2", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order", ExecutedBy: "a1"},
			}

			addLogicNeeds(spec, proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, concepts, reject)

			So(rejected, ShouldBeEmpty)
			executedBy := map[string]string{}
			for _, n := range spec.LogicNeeds {
				executedBy[n.Name] = n.ExecutedBy
			}
			So(executedBy, ShouldResemble, map[string]string{"Mail tool": "", "Hold order": "", "Reminder service": "",
				"Send reminder": "n3", "Mail customer": "n1", "Close order": "", "Window check": "", "Cancel order": ""})
		})

		Convey("An action type whose executor a run dropped takes the one the Agent names again\n", func() {
			rejected = nil
			spec := emptySpec()
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				{ID: "n2", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order", Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS,
					ExecutedBy: "n6"},
				{ID: "n6", Kind: interfaces.LOGIC_NEED_API, Name: "Hold service", Source: interfaces.LOGIC_NEED_FROM_ANALYSIS},
			}
			spec.LastIDs = map[string]int{"n": 6}
			// The run analyses n6 anew: no one accepted it.
			keepForKinetic(spec, false)
			proposed := []agentLogicNeed{
				{ID: "a1", Kind: interfaces.LOGIC_NEED_API, Name: "Hold service", SupplementReason: "r"},
				{ID: "kept-n2", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order", ExecutedBy: "a1"},
			}

			addLogicNeeds(spec, proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, concepts, reject)

			So(rejected, ShouldBeEmpty)
			So(spec.LogicNeeds[0].ID, ShouldEqual, "n2")
			So(spec.LogicNeeds[1].ID, ShouldEqual, "n7")
			So(spec.LogicNeeds[0].ExecutedBy, ShouldEqual, "n7")
		})

		Convey("Only adopted object types and their properties stand; a property names its object type\n", func() {
			rejected = nil
			spec := emptySpec()
			proposed := []agentLogicNeed{{Kind: interfaces.LOGIC_NEED_METRIC, Name: "Order amount", SupplementReason: "r",
				ObjectTypeIDs: []string{"customer", "carrier", "nope", "customer"},
				Properties: []agentPropertyRef{
					{ObjectTypeID: "sales_order", Property: "amount"},
					{ObjectTypeID: "sales_order", Property: "amount"},
					{ObjectTypeID: "sales_order", Property: "missing"},
					{ObjectTypeID: "carrier", Property: "code"},
				}}}

			addLogicNeeds(spec, proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, concepts, reject)

			n := spec.LogicNeeds[0]
			So(n.ObjectTypeIDs, ShouldResemble, []string{"customer", "sales_order"})
			So(n.Properties, ShouldResemble, []interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "amount"}})
			So(rejected, ShouldBeEmpty)

			// Without a model none stands.
			spec = emptySpec()
			addLogicNeeds(spec, proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, nil, concepts, reject)
			So(spec.LogicNeeds[0].ObjectTypeIDs, ShouldResemble, []string{})
			So(spec.LogicNeeds[0].Properties, ShouldResemble, []interfaces.RequirementPropertyRef{})
		})

		Convey("A need repeating one of the same kind and name is merged into it; at most maxRunLogicNeeds are added\n", func() {
			rejected = nil
			spec := emptySpec()
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{{ID: "n4", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate",
				ConceptIDs: []string{"c2"}, Source: interfaces.LOGIC_NEED_FROM_PERSON}}
			proposed := []agentLogicNeed{
				{Kind: interfaces.LOGIC_NEED_METRIC, Name: " late RATE ", ConceptIDs: []string{"c1", "c2"}},
				// The same name of another kind is another need.
				{Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Late rate", SupplementReason: "r"},
			}
			for i := range maxRunLogicNeeds {
				proposed = append(proposed, agentLogicNeed{Kind: interfaces.LOGIC_NEED_API, Name: fmt.Sprintf("api %d", i), SupplementReason: "r"})
			}

			addLogicNeeds(spec, proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, concepts, reject)

			So(spec.LogicNeeds, ShouldHaveLength, 1+maxRunLogicNeeds)
			So(spec.LogicNeeds[0].ConceptIDs, ShouldResemble, []string{"c2", "c1"})
			So(spec.LogicNeeds[0].Source, ShouldEqual, interfaces.LOGIC_NEED_FROM_PERSON)
			So(spec.LogicNeeds[1].ID, ShouldEqual, "n5")
			So(spec.LogicNeeds[1].Kind, ShouldEqual, interfaces.LOGIC_NEED_FUNCTION)
			So(rejected, ShouldResemble, []string{fmt.Sprintf("logic_need api %d %s", maxRunLogicNeeds-1, rejectedTooMany)})
		})

		Convey("Given again, a stated need without object types is placed on the model; one a person changed stays\n", func() {
			spec := emptySpec()
			spec.LogicNeeds = []*interfaces.RequirementLogicNeed{
				{ID: "n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate", ObjectTypeIDs: []string{},
					Properties: []interfaces.RequirementPropertyRef{}, Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS},
				{ID: "n2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Overrun", ObjectTypeIDs: []string{},
					Properties: []interfaces.RequirementPropertyRef{}, Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS, Edited: true},
				{ID: "n3", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Placed", ObjectTypeIDs: []string{"customer"},
					Properties: []interfaces.RequirementPropertyRef{}, Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS},
			}
			proposed := []agentLogicNeed{
				// By its kept- id, even renamed.
				{ID: "kept-n1", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Lateness rate", ObjectTypeIDs: []string{"sales_order", "carrier"},
					Properties: []agentPropertyRef{{ObjectTypeID: "sales_order", Property: "agreed_on"}}, SuggestedMetricType: "derived"},
				{Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Overrun", ObjectTypeIDs: []string{"sales_order"}},
				{Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Placed", ObjectTypeIDs: []string{"sales_order"}},
			}

			addLogicNeeds(spec, proposed, interfaces.LOGIC_NEED_FROM_ANALYSIS, docs, draft, concepts, reject)

			So(spec.LogicNeeds, ShouldHaveLength, 3)
			n1 := spec.LogicNeeds[0]
			So(n1.Name, ShouldEqual, "Late rate")
			So(n1.ObjectTypeIDs, ShouldResemble, []string{"sales_order"})
			So(n1.Properties, ShouldResemble, []interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "agreed_on"}})
			So(n1.SuggestedMetricType, ShouldEqual, "derived")
			So(n1.Source, ShouldEqual, interfaces.LOGIC_NEED_FROM_DOCUMENTS)
			So(spec.LogicNeeds[1].ObjectTypeIDs, ShouldBeEmpty)
			So(spec.LogicNeeds[2].ObjectTypeIDs, ShouldResemble, []string{"customer"})
		})
	})
}

func Test_AnalyseKinetic(t *testing.T) {
	Convey("Test the kinetic run\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		// A network the project created and never released: nothing of it is read, no bba nor kva
		// is there to read it, and the analysis goes as it always did.
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		svc := &requirementService{ps: ps, ra: ra, da: da, ba: ba, rla: rla,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller,
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN}}
		docs := testDocuments()
		need := func(id, name, source string, accepted bool) *interfaces.RequirementLogicNeed {
			return &interfaces.RequirementLogicNeed{ID: id, Kind: interfaces.LOGIC_NEED_METRIC, Name: name, Source: source,
				Accepted: accepted, ConceptIDs: []string{}, ScenarioIDs: []string{}}
		}
		previous := func() *interfaces.Requirement {
			return &interfaces.Requirement{ProjectID: "p1", Version: 2, Status: interfaces.REQUIREMENT_STATUS_READY, UpdateTime: 7,
				Spec: interfaces.RequirementSpec{
					Concepts: []*interfaces.RequirementConcept{{ID: "c1", Term: "Agreed date", Category: "property"}},
					Rules:    []*interfaces.RequirementRule{{ID: "r1", Statement: "Only signed changes count"}},
					Questions: []*interfaces.RequirementQuestion{{ID: "q1", Text: "How late?", ScenarioIDs: []string{"s1"},
						LogicNeedIDs: []string{"n5"}, Layer: interfaces.REQUIREMENT_LAYER_CAPABILITY}},
					Scenarios: []*interfaces.RequirementScenario{{ID: "s1", Name: "Chase"}},
					LogicNeeds: []*interfaces.RequirementLogicNeed{
						need("n1", "Old analysis", interfaces.LOGIC_NEED_FROM_ANALYSIS, false),
						need("n2", "Accepted analysis", interfaces.LOGIC_NEED_FROM_ANALYSIS, true),
						need("n3", "Stated", interfaces.LOGIC_NEED_FROM_DOCUMENTS, false),
						need("n4", "Added", interfaces.LOGIC_NEED_FROM_PERSON, false),
						need("n5", "Rested on", interfaces.LOGIC_NEED_FROM_ANALYSIS, false),
					},
					Rejected: []*interfaces.RequirementRejected{{Text: "old", Run: interfaces.REQUIREMENT_RUN_KINETIC},
						{Text: "concepts'", Run: interfaces.REQUIREMENT_RUN_CONCEPTS}},
					LastIDs: map[string]int{"n": 5},
				}}
		}
		expectRun := func(st *runStore, draft *interfaces.Draft) {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(draft, true, nil)
			st.expect(ra)
		}
		output := `{"logic_needs":[
			{"id":"x","kind":"metric","name":"Late reply rate","definition":"replies after 2 working days","object_type_ids":["sales_order"],
				"properties":[{"object_type_id":"sales_order","property":"agreed_on"}],"suggested_metric_type":"atomic",
				"concept_ids":["kept-c1"],"citations":[{"document_id":"pol","quote":"within 2 working days"}]},
			{"id":"y","kind":"metric","name":"stated","definition":"","concept_ids":["c1"],"citations":[]},
			{"id":"z","kind":"skill","name":"Made up Skill","definition":"","citations":[],"supplement_reason":"r"}]}`

		Convey("On a network the project created, it replaces the analysed needs no one made theirs, and keeps the rest\n", func() {
			st := &runStore{req: previous()}
			expectRun(st, kineticDraft())
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					So(req.Message, ShouldStartWith, "pass kinetic\nlanguage: zh-CN\n")
					var msg kineticMessage
					So(json.Unmarshal([]byte(req.Message[strings.Index(req.Message, "{"):]), &msg), ShouldBeNil)
					So(msg.Documents, ShouldHaveLength, 2)
					// The adopted items go with their properties, the candidate does not.
					So(msg.ObjectTypes, ShouldHaveLength, 2)
					So(msg.ObjectTypes[0].Properties, ShouldHaveLength, 2)
					So(msg.Concepts, ShouldResemble, []messageConcept{{ID: "c1", Term: "Agreed date", Category: "property"}})
					So(msg.Rules, ShouldHaveLength, 1)
					kept := []string{}
					for _, n := range msg.Kept.LogicNeeds {
						kept = append(kept, n.ID)
					}
					So(kept, ShouldResemble, []string{"kept-n2", "kept-n3", "kept-n4", "kept-n5"})
					So(req.ResponseFormat["required"], ShouldResemble, []string{"logic_needs"})
					return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil
				})

			req, err := svc.AnalyseKinetic(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			So(st.readUpdateTime, ShouldEqual, 7)
			So(req.Spec.Runs[interfaces.REQUIREMENT_RUN_KINETIC].Status, ShouldEqual, interfaces.REQUIREMENT_RUN_READY)
			spec := st.req.Spec
			So(ids(spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n2", "n3", "n4", "n5", "n6"})
			n6 := spec.LogicNeeds[4]
			So(n6.Source, ShouldEqual, interfaces.LOGIC_NEED_FROM_ANALYSIS)
			So(n6.ConceptIDs, ShouldResemble, []string{"c1"})
			So(n6.ObjectTypeIDs, ShouldResemble, []string{"sales_order"})
			So(n6.Properties, ShouldResemble, []interfaces.RequirementPropertyRef{{ObjectTypeID: "sales_order", Property: "agreed_on"}})
			// The repeat of a stated need is merged into it.
			So(spec.LogicNeeds[1].ConceptIDs, ShouldResemble, []string{"c1"})
			// Needs serve the scenarios of the questions resting on them.
			So(spec.LogicNeeds[3].ScenarioIDs, ShouldResemble, []string{"s1"})
			So(spec.Rejected, ShouldResemble, []*interfaces.RequirementRejected{
				{Text: "concepts'", Run: interfaces.REQUIREMENT_RUN_CONCEPTS},
				{Kind: rejectedLogicNeed, Text: "Made up Skill", Reason: rejectedBasisNotInDocuments, Run: interfaces.REQUIREMENT_RUN_KINETIC}})
			// The rest of the specification is as it was.
			So(ids(spec.Concepts, conceptID), ShouldResemble, []string{"c1"})
			So(ids(spec.Questions, questionID), ShouldResemble, []string{"q1"})
			So(st.req.Version, ShouldEqual, 2)
		})

		Convey("Starting over keeps of the analysed needs only those questions rest on\n", func() {
			st := &runStore{req: previous()}
			draft := kineticDraft()
			// Confirmed against rq-2: the run writes rq-3.
			draft.Items.Confirmation.RequirementVersion = 2
			expectRun(st, draft)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{"logic_needs":[]}`}, nil)

			_, err := svc.AnalyseKinetic(ctx, token, "p1", caller, true)

			So(err, ShouldBeNil)
			So(ids(st.req.Spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n3", "n4", "n5"})
			So(st.req.Version, ShouldEqual, 3)
		})

		Convey("A run that fails leaves the needs as they were\n", func() {
			st := &runStore{req: previous()}
			expectRun(st, kineticDraft())
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(&interfaces.BknAgentTask{TaskID: "t1",
				Status: interfaces.BKN_AGENT_TASK_FAILED, FailureDetail: "RateLimitError"}, nil)

			_, err := svc.AnalyseKinetic(ctx, token, "p1", caller, false)

			So(err, ShouldBeNil)
			run := st.req.Spec.Runs[interfaces.REQUIREMENT_RUN_KINETIC]
			So(run.Status, ShouldEqual, interfaces.REQUIREMENT_RUN_FAILED)
			So(run.Error, ShouldEqual, failureAgentFailed)
			So(run.AgentTaskID, ShouldEqual, "t1")
			So(ids(st.req.Spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n1", "n2", "n3", "n4", "n5"})
			So(st.req.Version, ShouldEqual, 2)
		})

		Convey("It needs a confirmed model adopting an object type\n", func() {
			noneAdopted := confirmedDraft()
			for _, ot := range noneAdopted.Items.ObjectTypes {
				ot.Adopted = false
			}
			for _, d := range []*interfaces.Draft{nil, testDraft(), noneAdopted} {
				ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
				ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
				da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, d != nil, nil)

				_, err := svc.AnalyseKinetic(ctx, token, "p1", caller, false)

				shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_ModelNotConfirmed)
			}
		})

		Convey("It does not start while another run goes\n", func() {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
			ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(docs, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(kineticDraft(), true, nil)
			ra.EXPECT().GetRequirement(gomock.Any(), "p1").Return(previous(), true, nil)
			ra.EXPECT().StartRun(gomock.Any(), "p1", interfaces.REQUIREMENT_RUN_KINETIC, gomock.Any(), gomock.Any(), int64(7)).Return(false, nil)

			_, err := svc.AnalyseKinetic(ctx, token, "p1", caller, false)
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
		})
	})
}

func Test_AnalyseKineticOnNetwork(t *testing.T) {
	Convey("On a network the project started on, the analysis builds on what the network has\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		ra.EXPECT().GetSupplement(gomock.Any(), gomock.Any()).Return(&interfaces.RequirementSupplement{}, nil).AnyTimes()
		da := pmock.NewMockDraftAccess(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		rla := pmock.NewMockReleaseAccess(ctrl)
		kva := pmock.NewMockKNVersionAccess(ctrl)
		bba := pmock.NewMockBknBackendAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra, da: da, ba: ba, rla: rla, kva: kva, bba: bba,
			now: func() int64 { return 1000 }, newID: func() string { return "run1" },
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}}
		ctx := context.Background()
		owned := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1", KNID: "kn1", Owner: caller,
			CatalogIDs: []string{"c1"}, NamingLanguage: interfaces.NAMING_LANGUAGE_ZH_CN}}
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(owned, nil)
		ra.EXPECT().GetDocuments(gomock.Any(), "p1").Return(testDocuments(), nil)
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(kineticDraft(), true, nil)
		rla.EXPECT().ListReleases(gomock.Any(), gomock.Any()).Return(nil, nil)
		kva.EXPECT().GetVersioning(gomock.Any(), token, "kn1").Return(&interfaces.KNVersioning{Enabled: true, RunningVersion: 3}, nil)
		bba.EXPECT().ListActionTypes(gomock.Any(), token, "kn1", "").Return(
			[]*interfaces.KNActionType{{ID: "a1", Name: "Hold order", Comment: "Holds an order", ObjectTypeID: "sales_order"}}, nil)
		bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_FUNCTION, "").Return(
			[]*interfaces.KNCapability{{ID: "b1", CapabilityID: "f1", BoxID: "box1", Name: "Window check", Description: "d"}}, nil)
		bba.EXPECT().ListCapabilities(gomock.Any(), token, "kn1", interfaces.LOGIC_LINK_SKILL, "").Return(nil, nil)
		bba.EXPECT().ListObjectMetrics(gomock.Any(), token, "kn1", "").Return(
			[]*interfaces.KNObjectMetric{{ID: "m1", Name: "Late rate", OwnerObjectTypeID: "sales_order"}}, nil)
		// The network's description is read beside the documents.
		bba.EXPECT().GetKN(gomock.Any(), token, "kn1").Return(&interfaces.KnowledgeNetwork{ID: "kn1", Name: "Orders",
			Comment: "Plans orders by kit availability."}, nil)
		st := &runStore{req: &interfaces.Requirement{ProjectID: "p1", Version: 2, Status: interfaces.REQUIREMENT_STATUS_READY,
			UpdateTime: 7, Spec: interfaces.RequirementSpec{
				LogicNeeds: []*interfaces.RequirementLogicNeed{
					// The documents state a need the network has; a person changed one it has too.
					{ID: "n1", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "hold-order", Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS,
						ConceptIDs: []string{}, ScenarioIDs: []string{}},
					{ID: "n2", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate", Source: interfaces.LOGIC_NEED_FROM_PERSON,
						Edited: true, ConceptIDs: []string{}, ScenarioIDs: []string{}},
					// A step of what a function of the network computes, by another name.
					{ID: "n3", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window days", Source: interfaces.LOGIC_NEED_FROM_DOCUMENTS,
						ConceptIDs: []string{}, ScenarioIDs: []string{}},
				},
				LastIDs: map[string]int{"n": 3}}}}
		st.expect(ra)
		output := `{"logic_needs":[
			{"id":"kept-network-2","kind":"function","name":"Window check","definition":"d","object_type_ids":["sales_order"],"citations":[]},
			{"id":"x","kind":"function","name":"window_check","definition":"","citations":[],"supplement_reason":"r"},
			{"id":"kept-n3","kind":"function","name":"Window days","definition":"","citations":[],"covered_by":"kept-network-2"},
			{"id":"z","kind":"function","name":"Working days in window","definition":"","citations":[],"supplement_reason":"r","covered_by":"kept-network-2"},
			{"id":"y","kind":"metric","name":"On-time rate","definition":"","citations":[],"supplement_reason":"r"}]}`
		ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				var msg kineticMessage
				So(json.Unmarshal([]byte(req.Message[strings.Index(req.Message, "{"):]), &msg), ShouldBeNil)
				// The network's items go as kept needs after the specification's: the Agent never repeats them.
				So(msg.Kept.LogicNeeds, ShouldResemble, []messageLogicNeed{
					{ID: "kept-n1", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "hold-order"},
					{ID: "kept-n2", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate"},
					{ID: "kept-n3", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window days"},
					// Those on the model's object types first, each with its description and object type.
					{ID: "kept-network-1", Kind: interfaces.LOGIC_NEED_ACTION_TYPE, Name: "Hold order", Definition: "Holds an order",
						ObjectTypeIDs: []string{"sales_order"}, OwnerObjectType: "Sales order"},
					{ID: "kept-network-3", Kind: interfaces.LOGIC_NEED_METRIC, Name: "Late rate", ObjectTypeIDs: []string{"sales_order"},
						OwnerObjectType: "Sales order"},
					{ID: "kept-network-2", Kind: interfaces.LOGIC_NEED_FUNCTION, Name: "Window check", Definition: "d"},
				})
				So(msg.NetworkItemsOmitted, ShouldEqual, 0)
				So(msg.Documents[len(msg.Documents)-1].ID, ShouldEqual, interfaces.REQUIREMENT_NETWORK_DOCUMENT_ID)
				So(msg.Documents[len(msg.Documents)-1].Blocks[0].Text, ShouldContainSubstring, "kit availability")
				return &interfaces.BknAgentTask{TaskID: "t1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}, nil
			})

		_, err := svc.AnalyseKinetic(ctx, token, "p1", caller, false)

		So(err, ShouldBeNil)
		spec := st.req.Spec
		// Only the gap is a new need: what the network has, or covers, is not given again.
		So(ids(spec.LogicNeeds, logicNeedID), ShouldResemble, []string{"n1", "n2", "n3", "n4"})
		So(spec.LogicNeeds[3].Name, ShouldEqual, "On-time rate")
		So(spec.LogicNeeds[3].Link, ShouldBeNil)
		// The stated need the Agent says a function covers is linked to it.
		So(spec.LogicNeeds[2].Link, ShouldResemble, &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_FUNCTION,
			ID: "f1", BoxID: "box1", By: caller, At: 1000})
		// The stated need the network covers is linked to it; the one a person changed is left to them.
		So(spec.LogicNeeds[0].Link, ShouldResemble, &interfaces.RequirementLogicLink{Type: interfaces.LOGIC_LINK_ACTION_TYPE,
			ID: "a1", By: caller, At: 1000})
		So(spec.LogicNeeds[1].Link, ShouldBeNil)
	})
}
