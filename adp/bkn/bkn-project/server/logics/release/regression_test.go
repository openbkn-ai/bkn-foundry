// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"testing"

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
		svc := &regressionService{ra: ra, rs: rs, ba: ba, gs: gs, run: func(fn func()) { fn() }}
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

		Convey("Questions of the knowledge layer are answered on the release's network and judged\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(
				regQuestion("q1", knowledge, regNeed(ot, "supplier")),
				regQuestion("q2", knowledge, regNeed(ot, "customer"), regNeed(ot, "part")),
				regQuestion("q3", knowledge),
				regQuestion("q4", interfaces.REQUIREMENT_LAYER_CAPABILITY, regNeed(ot, "part")),
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
				{QuestionID: "q1", Outcome: interfaces.REGRESSION_OUTCOME_PASSED, Answer: "Supplier S1."},
				{QuestionID: "q2", Outcome: interfaces.REGRESSION_OUTCOME_MISSING,
					Missing: []interfaces.ReleaseItem{{Kind: ot, ID: "customer", Name: "customer"}}},
				// The answer is kept as it came, blank too.
				{QuestionID: "q3", Outcome: interfaces.REGRESSION_OUTCOME_NO_ANSWER, Answer: "  "},
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
			So(finished.Results[0], ShouldResemble, interfaces.RegressionResult{QuestionID: "q1",
				Outcome: interfaces.REGRESSION_OUTCOME_PASSED, Answer: "Part P1.", ToolUses: uses})
			So(finished.Results[1].Answer, ShouldEqual, "Sorry.")
			So(finished.Results[1].ToolUses, ShouldHaveLength, 1)
		})

		Convey("A regression whose questions were all answered passes\n", func() {
			rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(questions(regQuestion("q1", knowledge, regNeed(ot, "part"))), nil)
			expectStart()
			gs.EXPECT().ReadTree(gomock.Any(), "supply", "refs/tags/v1.1.0").Return(files, nil)
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).Return(answered("Part P1."), nil)
			expectFinish()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_PASSED)
			So(finished.Attempt, ShouldEqual, 1)
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
				{QuestionID: "q1", Outcome: interfaces.REGRESSION_OUTCOME_PASSED, Answer: "P1."},
				{QuestionID: "q2", Outcome: interfaces.REGRESSION_OUTCOME_ERROR, ErrorCode: "BknAgent.Model.Unavailable"},
				{QuestionID: "q3", Outcome: interfaces.REGRESSION_OUTCOME_ERROR, ErrorCode: interfaces.REGRESSION_CODE_TOOLS_UNAVAILABLE,
					Answer: "Sorry."},
				{QuestionID: "q4", Outcome: interfaces.REGRESSION_OUTCOME_PASSED, Answer: "P4."},
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
