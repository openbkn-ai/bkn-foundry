// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func regNeed(kind string, id string) interfaces.RequirementItemRef {
	return interfaces.RequirementItemRef{Kind: kind, ID: id}
}

func regQuestion(id string, layer string, needs ...interfaces.RequirementItemRef) interfaces.ScenarioQuestion {
	return interfaces.ScenarioQuestion{ID: id, Text: "Text of " + id, Layer: layer, Needs: needs}
}

func objectTypeFile(id string, name string) []byte {
	return []byte(fmt.Sprintf("---\ntype: object_type\nid: %s\nname: %s\n---\n\n## ObjectType: %s\n", id, name, name))
}

func Test_RegressionService(t *testing.T) {
	Convey("Test regressionService\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ra := pmock.NewMockRegressionAccess(mockCtrl)
		rs := pmock.NewMockRequirementService(mockCtrl)
		ba := pmock.NewMockBknAgentAccess(mockCtrl)
		gs := pmock.NewMockGitStore(mockCtrl)
		da := pmock.NewMockDraftAccess(mockCtrl)
		pa := pmock.NewMockProjectAccess(mockCtrl)
		clock := time.UnixMilli(1000)
		svc := &regressionService{ra: ra, rs: rs, ba: ba, gs: gs, da: da, pa: pa, run: func(fn func()) { fn() },
			sleep: func(d time.Duration) { clock = clock.Add(d) }, now: func() time.Time { return clock }}
		ctx := context.Background()
		const token = "Bearer t"
		runner := interfaces.AccountInfo{ID: "u1", Type: "user"}
		release := &interfaces.Release{LineageID: "supply", NetworkID: "supply_v1_1_0", Version: "1.1.0", ProjectID: "p1"}
		knowledge := interfaces.REQUIREMENT_LAYER_KNOWLEDGE
		ot := interfaces.RELEASE_ITEM_OBJECT_TYPE
		questions := func(qs ...interfaces.ScenarioQuestion) *interfaces.ScenarioQuestions {
			return &interfaces.ScenarioQuestions{SpecVersion: 1, UpdateTime: 5, Questions: qs}
		}
		files := map[string][]byte{
			"network.bkn":               []byte("---\ntype: knowledge_network\nid: supply\nname: Supply\n---\n"),
			"object_types/supplier.bkn": objectTypeFile("supplier", "Supplier"),
			"object_types/part.bkn":     objectTypeFile("part", "Part"),
		}
		// finished captures what the run records.
		var finished *interfaces.Regression
		var started *interfaces.Regression
		expectStart := func() {
			ra.EXPECT().StartRegression(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, r *interfaces.Regression, _ int64) (bool, error) {
					copied := *r
					started = &copied
					return true, nil
				})
		}
		expectFinish := func() {
			ra.EXPECT().FinishRegression(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, r *interfaces.Regression) (bool, error) {
					finished = r
					return true, nil
				})
		}
		answered := func(answer string) *interfaces.BknAgentAnswer {
			return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: answer, ToolCalls: 2}
		}
		// The project names its items in English; its draft knows names the release does not have.
		pa.EXPECT().GetProject(gomock.Any(), "p1").Return(&interfaces.Project{ID: "p1",
			NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}, true, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{Items: interfaces.DraftItems{
			ObjectTypes: []*interfaces.DraftObjectType{{ID: "customer", Name: "Customer account"}},
		}}, true, nil).AnyTimes()
		// The judge answers by the question it is given: answered unless verdicts says otherwise.
		verdicts := map[string]judgeOutput{}
		var judged []judgeInput
		ba.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				var in judgeInput
				So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
				judged = append(judged, in)
				out, ok := verdicts[in.Question]
				if !ok {
					out = judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_ANSWERED, Reason: "R"}
				}
				data, _ := json.Marshal(out)
				return &interfaces.BknAgentTask{TaskID: "j", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: string(data)}, nil
			}).AnyTimes()
		passed := func(id string, answer string) interfaces.RegressionResult {
			return interfaces.RegressionResult{QuestionID: id, Outcome: interfaces.REGRESSION_OUTCOME_PASSED, Answer: answer,
				Verdict: interfaces.REGRESSION_VERDICT_ANSWERED, Reason: "R"}
		}

		Convey("Questions of the knowledge layer are answered on the release's network and judged\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "supplier")),
				regQuestion("q2", knowledge, regNeed(ot, "customer"), regNeed(ot, "part")),
				regQuestion("q3", knowledge),
				regQuestion("q4", interfaces.REQUIREMENT_LAYER_CAPABILITY, regNeed(ot, "part")),
				// What joins its object types in the draft counts as needed too.
				withPath(regQuestion("q5", knowledge, regNeed(ot, "supplier")), regNeed(interfaces.RELEASE_ITEM_RELATION_TYPE, "supplier_part")),
			), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(files, nil)
			var asked []*interfaces.BknAgentAnswerRequest
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, req *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
					asked = append(asked, req)
					if req.Message == "Text of q1" {
						return answered("Supplier S1."), nil
					}
					return answered("  "), nil
				}).Times(2)
			expectFinish()

			r, ok, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(r.Status, ShouldEqual, interfaces.REGRESSION_STATUS_RUNNING)
			So(started.NetworkID, ShouldEqual, "supply_v1_1_0")
			So(started.ProjectID, ShouldEqual, "p1")
			So(started.Version, ShouldEqual, "1.1.0")
			So(started.Runner, ShouldResemble, runner)
			So(started.Attempt, ShouldEqual, 1)
			So(started.Questions, ShouldResemble, []interfaces.RegressionQuestion{
				{ID: "q1", Text: "Text of q1", Needs: []interfaces.RequirementItemRef{regNeed(ot, "supplier")}},
				{ID: "q2", Text: "Text of q2", Needs: []interfaces.RequirementItemRef{regNeed(ot, "customer"), regNeed(ot, "part")}},
				{ID: "q3", Text: "Text of q3", Needs: []interfaces.RequirementItemRef{}},
				// The path kept apart from what the specification names.
				{ID: "q5", Text: "Text of q5", Needs: []interfaces.RequirementItemRef{regNeed(ot, "supplier")},
					Path: []interfaces.RequirementItemRef{regNeed(interfaces.RELEASE_ITEM_RELATION_TYPE, "supplier_part")}},
			})
			// The question that lacks what it needs is not asked.
			So(asked, ShouldHaveLength, 2)
			for _, req := range asked {
				So(req.AgentID, ShouldEqual, answerAgentID)
				So(req.KNID, ShouldEqual, "supply_v1_1_0")
				So(regexp.MustCompile(`^[0-9A-Za-z_.-]{1,100}$`).MatchString(req.ClientRequestID), ShouldBeTrue)
			}
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_NOT_PASSED)
			So(finished.FinishTime, ShouldBeGreaterThan, 0)
			So(finished.Results, ShouldResemble, []interfaces.RegressionResult{
				passed("q1", "Supplier S1."),
				// Not in the release: named as the draft names it.
				{QuestionID: "q2", Outcome: interfaces.REGRESSION_OUTCOME_MISSING,
					Missing: []interfaces.ReleaseItem{{Kind: ot, ID: "customer", Name: "Customer account"}}},
				// The answer is kept as it came, blank too.
				{QuestionID: "q3", Outcome: interfaces.REGRESSION_OUTCOME_NO_ANSWER, Answer: "  "},
				{QuestionID: "q5", Outcome: interfaces.REGRESSION_OUTCOME_MISSING,
					Missing: []interfaces.ReleaseItem{{Kind: interfaces.RELEASE_ITEM_RELATION_TYPE, ID: "supplier_part", Name: "supplier_part"}}},
			})
		})

		Convey("Each asked question keeps the answer and the tool calls behind it, for a later judge\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "part")), regQuestion("q2", knowledge, regNeed(ot, "part")),
			), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			uses := []interfaces.BknAgentToolUse{{Name: "query_object_instance",
				Input: []byte(`{"ot_id":"part"}`), Status: "succeeded"}}
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, req *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
					if req.Message == "Text of q1" {
						return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "Part P1.",
							ToolCalls: 1, ToolUses: uses}, nil
					}
					// Every call failed: the answer says nothing of the ontology, but is kept.
					return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "Sorry.",
						ToolCalls: 1, ToolsUnavailable: 1, ToolUses: []interfaces.BknAgentToolUse{{Name: "search_schema",
							Status: "failed", ErrorKind: "unavailable"}}}, nil
				}).Times(3)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			want := passed("q1", "Part P1.")
			want.ToolUses = uses
			So(finished.Results[0], ShouldResemble, want)
			So(finished.Results[1].Answer, ShouldEqual, "Sorry.")
			So(finished.Results[1].ToolUses, ShouldHaveLength, 1)
		})

		Convey("A regression whose questions were all answered passes\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				withPath(regQuestion("q1", knowledge, regNeed(ot, "part")), regNeed(ot, "supplier"))), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(files, nil)
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("Part P1."), nil)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_PASSED)
			So(finished.Attempt, ShouldEqual, 1)
			// The judge is told the path among what the question needs.
			So(judged[len(judged)-1].Needs, ShouldResemble, []interfaces.ReleaseItem{
				{Kind: ot, ID: "part", Name: "Part"}, {Kind: ot, ID: "supplier", Name: "Supplier"}})
		})

		Convey("Questions that could not be asked are asked once more, alone, and fail the run if they fail again\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "part")),
				regQuestion("q2", knowledge, regNeed(ot, "part")),
				regQuestion("q3", knowledge, regNeed(ot, "part")),
				regQuestion("q4", knowledge, regNeed(ot, "part")),
			), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(files, nil)
			calls := map[string][]string{}
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, req *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
					calls[req.Message] = append(calls[req.Message], req.ClientRequestID)
					first := len(calls[req.Message]) == 1
					switch req.Message {
					case "Text of q1":
						// Refused before answering, then answered.
						if first {
							return nil, &interfaces.BknAgentError{HTTPCode: http.StatusNotFound, Code: "BknAgent.NotFound"}
						}
						return answered("P1."), nil
					case "Text of q2":
						return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_FAILED,
							ErrorCode: "BknAgent.Model.Unavailable"}, nil
					case "Text of q3":
						// Every call failed to reach the network.
						return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "Sorry.",
							ToolCalls: 2, ToolsUnavailable: 2}, nil
					}
					return answered("P4."), nil
				}).Times(7)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(calls["Text of q4"], ShouldHaveLength, 1)
			So(calls["Text of q1"], ShouldHaveLength, 2)
			// A retry asks anew rather than replaying the failed answer.
			So(calls["Text of q1"][0], ShouldNotEqual, calls["Text of q1"][1])
			So(finished.Attempt, ShouldEqual, 2)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
			So(finished.Results, ShouldResemble, []interfaces.RegressionResult{
				passed("q1", "P1."),
				{QuestionID: "q2", Outcome: interfaces.REGRESSION_OUTCOME_ERROR, ErrorCode: "BknAgent.Model.Unavailable"},
				{QuestionID: "q3", Outcome: interfaces.REGRESSION_OUTCOME_ERROR, ErrorCode: interfaces.REGRESSION_CODE_TOOLS_UNAVAILABLE,
					Answer: "Sorry."},
				passed("q4", "P4."),
			})
		})

		Convey("An answer refused, not reached, cut short or cancelled could not be asked\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "part")), regQuestion("q2", knowledge, regNeed(ot, "part")),
				regQuestion("q3", knowledge, regNeed(ot, "part")), regQuestion("q4", knowledge, regNeed(ot, "part")),
			), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, req *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
					switch req.Message {
					case "Text of q1":
						return nil, errors.New("connection refused")
					case "Text of q4":
						// Reached, but the stream stopped before its end.
						return nil, fmt.Errorf("%w: no terminal event", interfaces.ErrBknAgentAnswerCut)
					case "Text of q2":
						return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_CANCELLED}, nil
					}
					// No permission on the new network, or it does not exist: bkn-agent says 403 for both.
					return nil, &interfaces.BknAgentError{HTTPCode: http.StatusForbidden, Code: "BknAgent.Execution.Forbidden"}
				}).Times(8)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Results[0].ErrorCode, ShouldEqual, interfaces.REGRESSION_CODE_UNREACHABLE)
			So(finished.Results[3].ErrorCode, ShouldEqual, interfaces.REGRESSION_CODE_ANSWER_INTERRUPTED)
			So(finished.Results[1].ErrorCode, ShouldEqual, interfaces.REGRESSION_CODE_CANCELLED)
			So(finished.Results[2].ErrorCode, ShouldEqual, "BknAgent.Execution.Forbidden")
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
		})

		Convey("A question answered with nothing does not pass\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(regQuestion("q1", knowledge, regNeed(ot, "part"))), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("\n "), nil)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Results[0].Outcome, ShouldEqual, interfaces.REGRESSION_OUTCOME_NO_ANSWER)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_NOT_PASSED)
		})

		Convey("A release whose definitions cannot be read fails, asking nothing\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(regQuestion("q1", knowledge, regNeed(ot, "part"))), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("tag not found"))
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
			So(finished.Error, ShouldEqual, interfaces.REGRESSION_ERROR_DEFINITIONS_UNREADABLE)
		})

		Convey("Without questions of the knowledge layer the regression is recorded as having none, at once\n", func() {
			for _, sq := range []*interfaces.ScenarioQuestions{
				{}, questions(regQuestion("q1", interfaces.REQUIREMENT_LAYER_CAPABILITY, regNeed(ot, "part"))),
			} {
				rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(sq, nil)
				expectStart()

				r, ok, err := svc.StartRegression(ctx, token, release, runner)

				So(err, ShouldBeNil)
				So(ok, ShouldBeTrue)
				So(r.Status, ShouldEqual, interfaces.REGRESSION_STATUS_NO_QUESTIONS)
				So(started.Status, ShouldEqual, interfaces.REGRESSION_STATUS_NO_QUESTIONS)
				So(started.FinishTime, ShouldBeGreaterThan, 0)
			}
		})

		Convey("Without a requirement service there are no questions\n", func() {
			svc.rs = nil
			expectStart()

			r, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(r.Status, ShouldEqual, interfaces.REGRESSION_STATUS_NO_QUESTIONS)
		})

		Convey("A regression of the release running already is left alone\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(regQuestion("q1", knowledge, regNeed(ot, "part"))), nil)
			ra.EXPECT().StartRegression(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)

			_, ok, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(ok, ShouldBeFalse)
		})

		Convey("Failures to read the questions or to start are errors\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(nil, errors.New("db down"))
			_, _, err := svc.StartRegression(ctx, token, release, runner)
			So(err, ShouldNotBeNil)

			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(regQuestion("q1", knowledge, regNeed(ot, "part"))), nil)
			ra.EXPECT().StartRegression(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, errors.New("db down"))
			_, _, err = svc.StartRegression(ctx, token, release, runner)
			So(err, ShouldNotBeNil)
		})

		Convey("The judge's verdict decides an answer, with its reason and the needs it used\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "part"), regNeed(ot, "supplier")),
				regQuestion("q2", knowledge, regNeed(ot, "part")),
				regQuestion("q3", knowledge, regNeed(ot, "part")),
				regQuestion("q4", knowledge, regNeed(ot, "part")),
			), nil)
			verdicts["Text of q1"] = judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_ANSWERED, Reason: "Listed them.",
				UsedNeeds: []interfaces.RequirementItemRef{regNeed(ot, "part")}}
			verdicts["Text of q2"] = judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_CLARIFICATION_NEEDED, Reason: "Asks which one."}
			verdicts["Text of q3"] = judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_CANNOT_ANSWER, Reason: "No relation between them."}
			verdicts["Text of q4"] = judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_OFF_TOPIC, Reason: "About something else."}
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			uses := []interfaces.BknAgentToolUse{
				{Name: "query_object_instance", Input: json.RawMessage(`{"ot_id":"part"}`), Status: "succeeded"},
				{Name: "search_schema", InputOmitted: true, Status: "succeeded"},
			}
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(&interfaces.BknAgentAnswer{
				Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "An answer.", ToolCalls: 2, ToolUses: uses}, nil).Times(4)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_NOT_PASSED)
			outcome := func(i int) (string, string, string) {
				return finished.Results[i].Outcome, finished.Results[i].Verdict, finished.Results[i].Reason
			}
			o, v, r := outcome(0)
			So([]string{o, v, r}, ShouldResemble, []string{"passed", "answered", "Listed them."})
			So(finished.Results[0].UsedNeeds, ShouldResemble, []interfaces.RequirementItemRef{regNeed(ot, "part")})
			o, v, _ = outcome(1)
			So([]string{o, v}, ShouldResemble, []string{"needs_values", "clarification_needed"})
			o, v, r = outcome(2)
			So([]string{o, v, r}, ShouldResemble, []string{"not_answered", "cannot_answer", "No relation between them."})
			o, v, _ = outcome(3)
			So([]string{o, v}, ShouldResemble, []string{"not_answered", "off_topic"})
			// The judge was given the question, the answer, the calls without their output, the needs by
			// name and the project's naming language.
			So(judged[0], ShouldResemble, judgeInput{
				Question: "Text of q1", Answer: "An answer.",
				ToolCalls: []judgeToolCall{{Name: "query_object_instance", Input: json.RawMessage(`{"ot_id":"part"}`)}, {Name: "search_schema"}},
				Needs:     []interfaces.ReleaseItem{{Kind: ot, ID: "part", Name: "Part"}, {Kind: ot, ID: "supplier", Name: "Supplier"}},
				Language:  interfaces.NAMING_LANGUAGE_EN_US,
			})
		})

		Convey("A run passes with one passed question; with only questions lacking values it shows nothing\n", func() {
			for _, c := range []struct {
				q1, q2 string
				want   string
			}{
				{interfaces.REGRESSION_VERDICT_ANSWERED, interfaces.REGRESSION_VERDICT_CLARIFICATION_NEEDED, interfaces.REGRESSION_STATUS_PASSED},
				{interfaces.REGRESSION_VERDICT_CLARIFICATION_NEEDED, interfaces.REGRESSION_VERDICT_CLARIFICATION_NEEDED, interfaces.REGRESSION_STATUS_NEEDS_VALUES},
			} {
				rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
					regQuestion("q1", knowledge, regNeed(ot, "part")), regQuestion("q2", knowledge, regNeed(ot, "part")),
				), nil)
				verdicts["Text of q1"], verdicts["Text of q2"] = judgeOutput{Verdict: c.q1}, judgeOutput{Verdict: c.q2}
				expectStart()
				gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
				ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("An answer."), nil).Times(2)
				expectFinish()

				_, _, err := svc.StartRegression(ctx, token, release, runner)

				So(err, ShouldBeNil)
				So(finished.Status, ShouldEqual, c.want)
			}
		})

		Convey("A question lacking values does not hide one that lacks what it needs\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "part")), regQuestion("q2", knowledge, regNeed(ot, "customer")),
			), nil)
			verdicts["Text of q1"] = judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_CLARIFICATION_NEEDED}
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("An answer."), nil)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_NOT_PASSED)
		})

		Convey("A judge that fails or answers outside its schema is an error, judged once more without asking again\n", func() {
			for _, c := range []struct {
				name  string
				first func() (*interfaces.BknAgentTask, error)
				code  string
			}{
				{"not run", func() (*interfaces.BknAgentTask, error) {
					return nil, &interfaces.BknAgentError{HTTPCode: http.StatusNotFound, Code: "BknAgent.NotFound"}
				}, interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE},
				{"failed", func() (*interfaces.BknAgentTask, error) {
					return &interfaces.BknAgentTask{Status: interfaces.BKN_AGENT_TASK_FAILED, FailureDetail: "model"}, nil
				}, interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE},
				{"not json", func() (*interfaces.BknAgentTask, error) {
					return &interfaces.BknAgentTask{Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: "yes"}, nil
				}, interfaces.REGRESSION_CODE_JUDGE_INVALID},
				{"unknown verdict", func() (*interfaces.BknAgentTask, error) {
					return &interfaces.BknAgentTask{Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{"verdict":"maybe","reason":"x","used_needs":[]}`}, nil
				}, interfaces.REGRESSION_CODE_JUDGE_INVALID},
				{"need not given", func() (*interfaces.BknAgentTask, error) {
					return &interfaces.BknAgentTask{Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
						Output: `{"verdict":"answered","reason":"x","used_needs":[{"kind":"object_type","id":"invented"}]}`}, nil
				}, interfaces.REGRESSION_CODE_JUDGE_INVALID},
			} {
				Convey(c.name+"\n", func() {
					judge := pmock.NewMockBknAgentAccess(mockCtrl)
					svc.ba = judge
					rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(regQuestion("q1", knowledge, regNeed(ot, "part"))), nil)
					expectStart()
					gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
					// Asked once: the retry judges the answer kept.
					judge.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("An answer."), nil).Times(1)
					gomock.InOrder(
						judge.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).DoAndReturn(
							func(context.Context, interfaces.BknAgentAccount, *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
								return c.first()
							}),
						judge.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).DoAndReturn(
							func(context.Context, interfaces.BknAgentAccount, *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
								return c.first()
							}),
					)
					expectFinish()

					_, _, err := svc.StartRegression(ctx, token, release, runner)

					So(err, ShouldBeNil)
					So(finished.Attempt, ShouldEqual, 2)
					So(finished.Results[0].Outcome, ShouldEqual, interfaces.REGRESSION_OUTCOME_ERROR)
					So(finished.Results[0].ErrorCode, ShouldEqual, c.code)
					// Never falls back to passing on an answer alone.
					So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
					So(finished.Results[0].Answer, ShouldEqual, "An answer.")
				})
			}
		})

		Convey("A judge that fails once and then judges decides the question\n", func() {
			judge := pmock.NewMockBknAgentAccess(mockCtrl)
			svc.ba = judge
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(regQuestion("q1", knowledge, regNeed(ot, "part"))), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			judge.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("An answer."), nil).Times(1)
			gomock.InOrder(
				judge.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).Return(nil, errors.New("down")),
				judge.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).Return(&interfaces.BknAgentTask{
					Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: `{"verdict":"answered","reason":"R","used_needs":[]}`}, nil),
			)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Results[0].Outcome, ShouldEqual, interfaces.REGRESSION_OUTCOME_PASSED)
			// Nothing of the failed judgement stays.
			So(finished.Results[0].ErrorCode, ShouldBeEmpty)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_PASSED)
		})

		Convey("The judge is asked as the runner with its schema, and waited for until its deadline\n", func() {
			judge := pmock.NewMockBknAgentAccess(mockCtrl)
			svc.ba = judge
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "part")), regQuestion("q2", knowledge, regNeed(ot, "part")),
			), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			judge.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("An answer."), nil).Times(2)
			var schema map[string]any
			judge.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					So(req.AgentID, ShouldEqual, judgeAgentID)
					schema = req.ResponseFormat
					return &interfaces.BknAgentTask{TaskID: "t-" + req.Message[len(req.Message)-30:], Status: interfaces.BKN_AGENT_TASK_PENDING}, nil
				}).Times(3)
			// q1's task finishes after a poll; q2's never does, nor its retry.
			first := true
			judge.EXPECT().GetTask(gomock.Any(), runner, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ interfaces.BknAgentAccount, taskID string) (*interfaces.BknAgentTask, error) {
					if first {
						first = false
						return &interfaces.BknAgentTask{TaskID: taskID, Status: interfaces.BKN_AGENT_TASK_SUCCEEDED,
							Output: `{"verdict":"answered","reason":"R","used_needs":[]}`}, nil
					}
					return &interfaces.BknAgentTask{TaskID: taskID, Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil
				}).MinTimes(2)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Results[0].Outcome, ShouldEqual, interfaces.REGRESSION_OUTCOME_PASSED)
			So(finished.Results[1].ErrorCode, ShouldEqual, interfaces.REGRESSION_CODE_JUDGE_UNAVAILABLE)
			verdict := schema["properties"].(map[string]any)["verdict"].(map[string]any)
			So(verdict["enum"], ShouldResemble, []string{"answered", "clarification_needed", "cannot_answer", "off_topic"})
		})

		Convey("A question with tests is asked through them, one result each, and itself not at all\n", func() {
			withTests := regQuestion("q1", knowledge, regNeed(ot, "part"))
			withTests.Tests = []interfaces.ScenarioTest{
				{ID: "t1", Text: "Which part is P1?", Expect: interfaces.TEST_EXPECT_EXISTS},
				{ID: "t2", Text: "Which part is P2?", Expect: interfaces.TEST_EXPECT_NOT_EXISTS},
			}
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(withTests, regQuestion("q2", knowledge, regNeed(ot, "part"))), nil)
			verdicts["Which part is P2?"] = judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_CANNOT_ANSWER, Reason: "No P2."}
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
			var asked []string
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, req *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
					asked = append(asked, req.Message)
					return answered("An answer."), nil
				}).Times(3)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(started.Questions[0].Tests, ShouldResemble, []interfaces.RegressionTest{
				{ID: "t1", Text: "Which part is P1?", Expect: interfaces.TEST_EXPECT_EXISTS},
				{ID: "t2", Text: "Which part is P2?", Expect: interfaces.TEST_EXPECT_NOT_EXISTS},
			})
			// The judge reads what each test expects of the data; a template question expects nothing.
			So([]string{judged[0].Expect, judged[1].Expect, judged[2].Expect}, ShouldResemble,
				[]string{interfaces.TEST_EXPECT_EXISTS, interfaces.TEST_EXPECT_NOT_EXISTS, ""})
			So(asked, ShouldResemble, []string{"Which part is P1?", "Which part is P2?", "Text of q2"})
			So(finished.Results, ShouldHaveLength, 3)
			So([]string{finished.Results[0].QuestionID, finished.Results[0].TestID}, ShouldResemble, []string{"q1", "t1"})
			So([]string{finished.Results[1].TestID, finished.Results[1].Outcome}, ShouldResemble, []string{"t2", "not_answered"})
			So([]string{finished.Results[2].QuestionID, finished.Results[2].TestID}, ShouldResemble, []string{"q2", ""})
		})

		Convey("A not_exists test passes when the judge finds it answered, and fails when it does not\n", func() {
			for verdict, want := range map[string]string{
				interfaces.REGRESSION_VERDICT_ANSWERED:      interfaces.REGRESSION_OUTCOME_PASSED,
				interfaces.REGRESSION_VERDICT_CANNOT_ANSWER: interfaces.REGRESSION_OUTCOME_NOT_ANSWERED,
			} {
				q := regQuestion("q1", knowledge, regNeed(ot, "part"))
				q.Tests = []interfaces.ScenarioTest{{ID: "t1", Text: "Which suppliers supply part X-0?", Expect: interfaces.TEST_EXPECT_NOT_EXISTS}}
				rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(q), nil)
				verdicts["Which suppliers supply part X-0?"] = judgeOutput{Verdict: verdict, Reason: "R"}
				expectStart()
				gs.EXPECT().ReadTree(gomock.Any(), gomock.Any(), gomock.Any()).Return(files, nil)
				ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("No such part exists."), nil)
				expectFinish()
				judged = nil

				_, _, err := svc.StartRegression(ctx, token, release, runner)

				So(err, ShouldBeNil)
				So(judged[0].Expect, ShouldEqual, interfaces.TEST_EXPECT_NOT_EXISTS)
				So(finished.Results[0].Outcome, ShouldEqual, want)
			}
		})

		Convey("Request ids keep to bkn-agent's form and differ by question and attempt\n", func() {
			id := clientRequestID("0190f5e2-7c1a-7d3e-8f00-123456789abc", 12, 2)

			So(regexp.MustCompile(`^[0-9A-Za-z_.-]{1,100}$`).MatchString(id), ShouldBeTrue)
			So(clientRequestID("r", 1, 1), ShouldNotEqual, clientRequestID("r", 1, 2))
			So(clientRequestID("r", 1, 1), ShouldNotEqual, clientRequestID("r", 2, 1))
		})

		Convey("Regressions a restart left running are failed as interrupted\n", func() {
			ra.EXPECT().FailRunning(gomock.Any(), interfaces.REGRESSION_ERROR_INTERRUPTED, gomock.Any()).Return(int64(2), nil)

			So(svc.FailInterrupted(ctx), ShouldBeNil)
		})
	})
}

// withPath gives a scenario question the path that joins what it needs in the draft.
func withPath(q interfaces.ScenarioQuestion, path ...interfaces.RequirementItemRef) interfaces.ScenarioQuestion {
	q.Path = path
	return q
}
