// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package release

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func Test_RunCredential(t *testing.T) {
	Convey("Test which credential a run asks a question with\n", t, func() {
		at := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
		c := &runCredential{token: "Bearer t", delegated: "bex_run_s", now: func() time.Time { return at }}

		Convey("The sign-in while it holds for a whole answer, then the issued credential\n", func() {
			c.expires = at.Add(tokenMargin + time.Second)
			So(c.current(), ShouldEqual, "Bearer t")
			c.expires = at.Add(tokenMargin)
			So(c.current(), ShouldEqual, "Bearer bex_run_s")
		})
		Convey("When it is not known how long the sign-in holds, the issued credential\n", func() {
			So(c.current(), ShouldEqual, "Bearer bex_run_s")
		})
		Convey("Without an issued credential, the sign-in always\n", func() {
			c.delegated = ""
			So(c.current(), ShouldEqual, "Bearer t")
		})
	})
}

func Test_RegressionService_Credential(t *testing.T) {
	Convey("Test a regression going on as its runner after the sign-in lapses\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ra := pmock.NewMockRegressionAccess(mockCtrl)
		rs := pmock.NewMockRequirementService(mockCtrl)
		ba := pmock.NewMockBknAgentAccess(mockCtrl)
		gs := pmock.NewMockGitStore(mockCtrl)
		da := pmock.NewMockDraftAccess(mockCtrl)
		pa := pmock.NewMockProjectAccess(mockCtrl)
		va := pmock.NewMockVegaAccess(mockCtrl)
		aa := pmock.NewMockAuthAccess(mockCtrl)
		ga := pmock.NewMockExecutionGrantAccess(mockCtrl)
		clock := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
		svc := &regressionService{ra: ra, rs: rs, ba: ba, gs: gs, da: da, pa: pa, va: va, aa: aa, ga: ga,
			run: func(fn func()) { fn() }, sleep: func(time.Duration) {}, now: func() time.Time { return clock }}
		ctx := context.Background()
		const token = "Bearer t"
		runner := interfaces.AccountInfo{ID: "u1", Type: "user"}
		release := &interfaces.Release{ID: "r2", LineageID: "supply", NetworkID: "kn-supply", Version: "1.1.0", ProjectID: "p1", BranchCommit: "b1"}
		ot := interfaces.RELEASE_ITEM_OBJECT_TYPE
		pa.EXPECT().GetProject(gomock.Any(), "p1").Return(&interfaces.Project{ID: "p1",
			NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}, true, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{}, true, nil).AnyTimes()
		va.EXPECT().MissingTables(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(&interfaces.ScenarioQuestions{SpecVersion: 1, Questions: []interfaces.ScenarioQuestion{
			regQuestion("q1", interfaces.REQUIREMENT_LAYER_KNOWLEDGE, regNeed(ot, "supplier")),
		}}, nil)
		ra.EXPECT().StartRegression(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "supply", "b1").Return(map[string][]byte{
			"network.bkn":               []byte("---\ntype: knowledge_network\nid: supply\nname: Supply\n---\n"),
			"object_types/supplier.bkn": objectTypeFile("supplier", "Supplier"),
		}, nil)
		var finished *interfaces.Regression
		ba.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).DoAndReturn(
			func(context.Context, interfaces.BknAgentAccount, *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				data, _ := json.Marshal(judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_ANSWERED, Reason: "R"})
				return &interfaces.BknAgentTask{TaskID: "j", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: string(data)}, nil
			}).AnyTimes()
		var askedWith []string
		ba.EXPECT().AnswerOnNetwork(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, credential string, _ *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
				askedWith = append(askedWith, credential)
				return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "Supplier S1.", ToolCalls: 1}, nil
			}).AnyTimes()
		var issued interfaces.ExecutionGrantRequest
		expectIssue := func(err error) {
			ga.EXPECT().Issue(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, req interfaces.ExecutionGrantRequest) (*interfaces.ExecutionGrant, error) {
					issued = req
					if err != nil {
						return nil, err
					}
					return &interfaces.ExecutionGrant{Credential: "bex_" + req.ID + "_s"}, nil
				})
		}
		ra.EXPECT().FinishRegression(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, r *interfaces.Regression) (bool, error) {
				finished = r
				return true, nil
			})

		Convey("A sign-in about to lapse: asked with the credential issued at the start, revoked once recorded\n", func() {
			aa.EXPECT().TokenExpiry(gomock.Any(), token).Return(clock.Add(5*time.Minute), nil)
			expectIssue(nil)
			revoked := false
			ga.EXPECT().Revoke(gomock.Any(), gomock.Any(), "p1", gomock.Any()).DoAndReturn(
				func(_ context.Context, id, _, run string) error {
					So(finished, ShouldNotBeNil)
					So(id, ShouldEqual, run)
					revoked = true
					return nil
				})

			r, ok, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(issued, ShouldResemble, interfaces.ExecutionGrantRequest{ID: r.RunID, ProjectID: "p1", RunID: r.RunID, DelegatorID: "u1",
				KnID: "kn-supply", AgentID: answerAgentID, TTL: regressionStaleAfter})
			So(askedWith, ShouldResemble, []string{"Bearer bex_" + r.RunID + "_s"})
			So(revoked, ShouldBeTrue)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_PASSED)
		})

		Convey("A sign-in that holds: asked with it\n", func() {
			aa.EXPECT().TokenExpiry(gomock.Any(), token).Return(clock.Add(time.Hour), nil)
			expectIssue(nil)
			ga.EXPECT().Revoke(gomock.Any(), gomock.Any(), "p1", gomock.Any()).Return(nil)

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(askedWith, ShouldResemble, []string{token})
		})

		Convey("No credential issued: the run asks with the sign-in, and nothing is revoked\n", func() {
			aa.EXPECT().TokenExpiry(gomock.Any(), token).Return(clock.Add(5*time.Minute), nil)
			expectIssue(errors.New("bkn-safe down"))

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(askedWith, ShouldResemble, []string{token})
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_PASSED)
		})

		Convey("An application runs on its own sign-in: none is issued for it\n", func() {
			app := interfaces.AccountInfo{ID: "a1", Type: "app"}
			ba.EXPECT().RunTask(gomock.Any(), app, gomock.Any()).DoAndReturn(
				func(context.Context, interfaces.BknAgentAccount, *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					data, _ := json.Marshal(judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_ANSWERED, Reason: "R"})
					return &interfaces.BknAgentTask{TaskID: "j", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: string(data)}, nil
				}).AnyTimes()

			_, _, err := svc.StartRegression(ctx, token, release, app)

			So(err, ShouldBeNil)
			So(askedWith, ShouldResemble, []string{token})
		})
	})
}
