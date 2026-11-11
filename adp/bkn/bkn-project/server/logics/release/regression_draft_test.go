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

func Test_RegressionService_Draft(t *testing.T) {
	Convey("Test a regression answering on the network's draft at the revision the release prepared\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		ra := pmock.NewMockRegressionAccess(mockCtrl)
		rs := pmock.NewMockRequirementService(mockCtrl)
		ba := pmock.NewMockBknAgentAccess(mockCtrl)
		gs := pmock.NewMockGitStore(mockCtrl)
		da := pmock.NewMockDraftAccess(mockCtrl)
		pa := pmock.NewMockProjectAccess(mockCtrl)
		va := pmock.NewMockVegaAccess(mockCtrl)
		kva := pmock.NewMockKNVersionAccess(mockCtrl)
		clock := time.UnixMilli(1000)
		var slept time.Duration
		svc := &regressionService{ra: ra, rs: rs, ba: ba, gs: gs, da: da, pa: pa, va: va, kva: kva,
			run:   func(fn func()) { fn() },
			sleep: func(d time.Duration) { slept += d; clock = clock.Add(d) }, now: func() time.Time { return clock }}
		ctx := context.Background()
		const token = "Bearer t"
		runner := interfaces.AccountInfo{ID: "u1", Type: "user"}
		release := &interfaces.Release{ID: "r2", LineageID: "supply", NetworkID: "supply", Version: "1.1.0", ProjectID: "p1",
			BranchCommit: "b1", DraftRevision: 7}
		ot := interfaces.RELEASE_ITEM_OBJECT_TYPE
		files := map[string][]byte{
			"network.bkn":               []byte("---\ntype: knowledge_network\nid: supply\nname: Supply\n---\n"),
			"object_types/supplier.bkn": objectTypeFile("supplier", "Supplier"),
		}
		pa.EXPECT().GetProject(gomock.Any(), "p1").Return(&interfaces.Project{ID: "p1",
			NamingLanguage: interfaces.NAMING_LANGUAGE_EN_US}, true, nil).AnyTimes()
		da.EXPECT().GetDraft(gomock.Any(), "p1").Return(&interfaces.Draft{}, true, nil).AnyTimes()
		va.EXPECT().MissingTables(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
		rs.EXPECT().ScenarioQuestions(gomock.Any(), "p1").Return(&interfaces.ScenarioQuestions{SpecVersion: 1, Questions: []interfaces.ScenarioQuestion{
			regQuestion("q1", interfaces.REQUIREMENT_LAYER_KNOWLEDGE, regNeed(ot, "supplier")),
		}}, nil)
		ra.EXPECT().StartRegression(gomock.Any(), gomock.Any(), gomock.Any()).Return(true, nil)
		gs.EXPECT().ReadTree(gomock.Any(), "supply", "b1").Return(files, nil)
		var finished *interfaces.Regression
		ra.EXPECT().FinishRegression(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, r *interfaces.Regression) (bool, error) {
				finished = r
				return true, nil
			})
		ba.EXPECT().RunTask(gomock.Any(), runner, gomock.Any()).DoAndReturn(
			func(context.Context, interfaces.BknAgentAccount, *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
				data, _ := json.Marshal(judgeOutput{Verdict: interfaces.REGRESSION_VERDICT_ANSWERED, Reason: "R"})
				return &interfaces.BknAgentTask{TaskID: "j", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: string(data)}, nil
			}).AnyTimes()
		var asked []*interfaces.BknAgentAnswerRequest
		expectAnswer := func() {
			ba.EXPECT().AnswerOnNetwork(gomock.Any(), token, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ string, req *interfaces.BknAgentAnswerRequest) (*interfaces.BknAgentAnswer, error) {
					asked = append(asked, req)
					return &interfaces.BknAgentAnswer{Status: interfaces.BKN_AGENT_ANSWER_SUCCEEDED, Answer: "Supplier S1.", ToolCalls: 1}, nil
				})
		}
		draftAt := func(revision int64, ready bool, status string) *interfaces.KNVersioning {
			return &interfaces.KNVersioning{Enabled: true, RunningVersion: 2,
				Draft: interfaces.KNDraftState{Revision: revision, IndexReady: ready,
					Index: interfaces.KNDraftIndexState{Revision: revision, Status: status}}}
		}
		ready := draftAt(7, true, interfaces.KN_DRAFT_INDEX_READY)
		building := draftAt(7, false, interfaces.KN_DRAFT_INDEX_BUILDING)

		Convey("A draft ready at once is answered on, pinned to the revision, and checked again after\n", func() {
			gomock.InOrder(
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(ready, nil),
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(ready, nil),
			)
			expectAnswer()

			_, ok, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(ok, ShouldBeTrue)
			So(slept, ShouldEqual, 0)
			So(asked, ShouldHaveLength, 1)
			So(asked[0].KNID, ShouldEqual, "supply")
			So(asked[0].KNVersion, ShouldEqual, interfaces.KN_VERSION_DRAFT)
			So(asked[0].DraftRevision, ShouldEqual, int64(7))
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_PASSED)
			So(finished.Error, ShouldBeEmpty)
		})

		Convey("A draft whose index is building is waited for, a failed read too\n", func() {
			gomock.InOrder(
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(building, nil),
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(nil, errors.New("timeout")),
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(ready, nil),
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(ready, nil),
			)
			expectAnswer()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(slept, ShouldEqual, 2*draftIndexPoll)
			So(asked, ShouldHaveLength, 1)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_PASSED)
		})

		Convey("A draft that moved on before the start is not answered on\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(draftAt(8, true, interfaces.KN_DRAFT_INDEX_READY), nil)

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(asked, ShouldBeEmpty)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
			So(finished.Error, ShouldEqual, interfaces.REGRESSION_ERROR_DRAFT_CHANGED)
		})

		Convey("A draft written while answering fails the run: its answers cannot be vouched for\n", func() {
			gomock.InOrder(
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(ready, nil),
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(draftAt(8, false, interfaces.KN_DRAFT_INDEX_BUILDING), nil),
			)
			expectAnswer()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(asked, ShouldHaveLength, 1)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
			So(finished.Error, ShouldEqual, interfaces.REGRESSION_ERROR_DRAFT_CHANGED)
		})

		Convey("A draft that cannot be read again after answering counts as moved\n", func() {
			gomock.InOrder(
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(ready, nil),
				kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(nil, errors.New("timeout")),
			)
			expectAnswer()

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
			So(finished.Error, ShouldEqual, interfaces.REGRESSION_ERROR_DRAFT_CHANGED)
		})

		Convey("An index that failed to build is not waited for\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(draftAt(7, false, interfaces.KN_DRAFT_INDEX_FAILED), nil)

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(asked, ShouldBeEmpty)
			So(slept, ShouldEqual, 0)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
			So(finished.Error, ShouldEqual, interfaces.REGRESSION_ERROR_INDEX_NOT_READY)
		})

		Convey("An index not built within the wait fails the run\n", func() {
			kva.EXPECT().GetVersioning(gomock.Any(), token, "supply").Return(building, nil).
				Times(int(draftIndexWait/draftIndexPoll) + 1)

			_, _, err := svc.StartRegression(ctx, token, release, runner)

			So(err, ShouldBeNil)
			So(asked, ShouldBeEmpty)
			So(slept, ShouldEqual, draftIndexWait)
			So(finished.Status, ShouldEqual, interfaces.REGRESSION_STATUS_FAILED)
			So(finished.Error, ShouldEqual, interfaces.REGRESSION_ERROR_INDEX_NOT_READY)
		})
	})
}
