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

		Convey("Tests get the server's ids, at most maxTests, and are not checked yet\n", func() {
			out := &guidanceOutput{Guidance: "Use the relation.", OutOfScope: []string{"Parent company?"}}
			for i := range maxTests + 2 {
				out.Tests = append(out.Tests, agentTest{Text: fmt.Sprintf("Orders %d of 10001?", i), Kind: "paraphrase",
					Expect: interfaces.TEST_EXPECT_EXISTS, Slots: []agentSlot{{ConceptID: "c2", Property: "customer_id", Value: "10001"}}})
			}
			out.Tests[1].Text = "  "

			tests, leftOut := buildTests(out, nil)

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
			}}}}, docs)

			So(tests[0].Slots[0].Value, ShouldEqual, "10001")
			So(tests[0].Slots[1].Value, ShouldEqual, "")
			So(tests[0].Slots[2].Value, ShouldEqual, "")
		})

		Convey("A value made up is replaced in the text by its slot, for the server's value to take its place\n", func() {
			tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{{Text: "Orders of 20001 and of Acme?", Slots: []agentSlot{
				{ConceptID: "c2", Property: "customer_id", Value: "20001"},
				{ConceptID: "c2", Property: "full_name", Value: "Acme"},
			}}}}, nil)

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

				tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{at}}, nil)

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
			}}, nil)

			So(tests, ShouldHaveLength, 1)
			So(leftOut, ShouldEqual, 4)
			So(tests[0].ID, ShouldEqual, "t1")
			So(tests[0].Text, ShouldEqual, "Orders of {{slot 1}}?")
		})

		Convey("An expectation the server does not know reads as exists\n", func() {
			tests, _ := buildTests(&guidanceOutput{Tests: []agentTest{{Text: "x", Expect: "maybe"}, {Text: "y", Expect: interfaces.TEST_EXPECT_SAME_RECORD}}}, nil)

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

func Test_GetGuidance(t *testing.T) {
	Convey("Test GetGuidance\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		ps := pmock.NewMockProjectService(ctrl)
		ra := pmock.NewMockRequirementAccess(ctrl)
		svc := &requirementService{ps: ps, ra: ra}
		ctx := context.Background()
		ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(&interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}, nil).AnyTimes()
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

		Convey("A question with no test questions generated is 404\n", func() {
			ra.EXPECT().GetGuidance(gomock.Any(), "p1", "q1").Return(nil, false, nil)

			_, err := svc.GetGuidance(ctx, token, "p1", "q1")
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)
		})
	})
}
