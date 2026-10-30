// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package draft

import (
	"context"
	"encoding/json"
	"errors"
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

// askDraft is identityDraft with a relation from order lines to customers.
func askDraft() *interfaces.Draft {
	d := identityDraft()
	d.Items.RelationTypes = []*interfaces.DraftRelationType{{
		ID: "order_lines_to_customers", Name: "Customer of order line", Source: "order_lines", Target: "customers",
		Cardinality: interfaces.DRAFT_CARDINALITY_MANY_TO_ONE,
		Mapping:     []interfaces.DraftFieldMapping{{SourceProperty: "order_no", TargetProperty: "id"}},
		Evidence:    interfaces.DraftJoinEvidence{Computed: true, Rows: 10, Hits: 9, HitRate: 0.9},
	}, {
		ID: "regions_to_customers", Source: "regions", Target: "elsewhere",
	}}
	return d
}

func answered(output string) *interfaces.BknAgentTask {
	return &interfaces.BknAgentTask{TaskID: "k1", Status: interfaces.BKN_AGENT_TASK_SUCCEEDED, Output: output}
}

func Test_AskObjectType(t *testing.T) {
	Convey("Test asking AI about an object type\n", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		// The draft is only read: any write to it, a decision or an id registered fails the test,
		// as these mocks expect no such call and the id registry is left out.
		da := pmock.NewMockDraftAccess(ctrl)
		ps := pmock.NewMockProjectService(ctrl)
		ba := pmock.NewMockBknAgentAccess(ctrl)
		clock := time.Unix(0, 0)
		svc := &draftService{setting: testSetting, da: da, ps: ps, ba: ba,
			now: func() time.Time { return clock }, sleep: func(d time.Duration) { clock = clock.Add(d) }}
		ctx := context.Background()
		project := &interfaces.ProjectEntry{Project: &interfaces.Project{ID: "p1"}}
		ask := func(q string) (*interfaces.DraftAskAnswer, error) {
			return svc.AskObjectType(ctx, token, "p1", "order_lines", interfaces.DraftAskRequest{Question: q}, caller)
		}
		readDraft := func(d *interfaces.Draft) {
			ps.EXPECT().GetProject(gomock.Any(), token, "p1").Return(project, nil)
			da.EXPECT().GetDraft(gomock.Any(), "p1").Return(d, true, nil)
		}

		Convey("It answers from the object type's state as the caller, and may suggest a name\n", func() {
			readDraft(askDraft())
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ interfaces.AccountInfo, req *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					So(req.AgentID, ShouldEqual, askAgentID)
					So(req.ResponseFormat, ShouldNotBeNil)
					var in askInput
					So(json.Unmarshal([]byte(req.Message), &in), ShouldBeNil)
					So(in.Question, ShouldEqual, "这个对象类叫什么更好？")
					So(in.ObjectType.ID, ShouldEqual, "order_lines")
					So(in.ObjectType.NeedsIdentity, ShouldBeTrue)
					So(len(in.ObjectType.Properties), ShouldEqual, 3)
					So(in.ObjectType.Properties[0].Field, ShouldEqual, "order_no")
					// Only the relation types it is an end of, with their counts, never rows.
					So(len(in.Relations), ShouldEqual, 1)
					So(*in.Relations[0].HitRate, ShouldEqual, 0.9)
					return answered(`{"answer":"  它记录订单的明细行。 ","suggestion":{"field":"name","value":"订单明细","reason":"更贴近业务"}}`), nil
				})

			got, err := ask("  这个对象类叫什么更好？ ")

			So(err, ShouldBeNil)
			So(got.Answer, ShouldEqual, "它记录订单的明细行。")
			So(got.Suggestion, ShouldResemble, &interfaces.DraftAskSuggestion{
				Field: interfaces.DRAFT_ASK_FIELD_NAME, Value: "订单明细", Reason: "更贴近业务"})
		})

		Convey("An identity it suggests is kept when ChooseIdentity would take it\n", func() {
			readDraft(askDraft())
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
				Return(answered(`{"answer":"用订单号和行号。","suggestion":{"field":"identity","properties":["order_no","line_no"]}}`), nil)

			got, err := ask("用什么做唯一标识？")

			So(err, ShouldBeNil)
			So(got.Suggestion.Properties, ShouldResemble, []string{"order_no", "line_no"})
		})

		Convey("A suggestion the edits would refuse, or one that changes nothing, is left out; the answer stays\n", func() {
			for _, suggestion := range []string{
				`{"field":"none"}`,
				`{"field":"adopted","value":"true"}`,
				`{"field":"name","value":"Order lines"}`,
				`{"field":"name","value":"` + strings.Repeat("名", interfaces.DRAFT_NAME_MAX_LENGTH+1) + `"}`,
				`{"field":"description","value":"` + strings.Repeat("长", askValueMaxLength+1) + `"}`,
				`{"field":"identity","properties":["order_no","order_no"]}`,
				`{"field":"identity","properties":["amount"]}`,
				`{"field":"identity","properties":["ghost"]}`,
				`{"field":"identity","properties":[]}`,
			} {
				readDraft(askDraft())
				ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
					Return(answered(`{"answer":"答复","suggestion":`+suggestion+`}`), nil)

				got, err := ask("怎么改？")

				So(err, ShouldBeNil)
				So(got.Answer, ShouldEqual, "答复")
				So(got.Suggestion, ShouldBeNil)
			}
		})

		Convey("It waits for the task, reading it again until it finishes\n", func() {
			readDraft(askDraft())
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
				Return(&interfaces.BknAgentTask{TaskID: "k1", Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil)
			gomock.InOrder(
				ba.EXPECT().GetTask(gomock.Any(), caller, "k1").Return(nil, errors.New("blip")),
				ba.EXPECT().GetTask(gomock.Any(), caller, "k1").Return(answered(`{"answer":"好","suggestion":{"field":"none"}}`), nil),
			)

			got, err := ask("问")

			So(err, ShouldBeNil)
			So(got.Answer, ShouldEqual, "好")
		})

		Convey("A task that does not finish in time is a timeout\n", func() {
			readDraft(askDraft())
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
				Return(&interfaces.BknAgentTask{TaskID: "k1", Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil)
			ba.EXPECT().GetTask(gomock.Any(), caller, "k1").
				Return(&interfaces.BknAgentTask{TaskID: "k1", Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil).AnyTimes()

			_, err := ask("问")

			shouldBeHTTPError(err, http.StatusGatewayTimeout, perrors.BknProject_Draft_AskTimeout)
			So(clock.Sub(time.Unix(0, 0)), ShouldBeLessThanOrEqualTo, askDeadline+askPoll)
		})

		Convey("A request cut while waiting stops waiting at once\n", func() {
			readDraft(askDraft())
			cut, cancel := context.WithCancel(ctx)
			ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
				DoAndReturn(func(context.Context, interfaces.AccountInfo, *interfaces.BknAgentRunRequest) (*interfaces.BknAgentTask, error) {
					cancel()
					return &interfaces.BknAgentTask{TaskID: "k1", Status: interfaces.BKN_AGENT_TASK_RUNNING}, nil
				})

			_, err := svc.AskObjectType(cut, token, "p1", "order_lines", interfaces.DraftAskRequest{Question: "问"}, caller)

			shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Draft_AskUnavailable)
			// No poll was made: the task is never read, and no time passed.
			So(clock, ShouldEqual, time.Unix(0, 0))
		})

		Convey("An Agent that cannot run, fails or answers outside its schema is unavailable\n", func() {
			for _, run := range []func(){
				func() { ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(nil, errors.New("down")) },
				func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).
						Return(&interfaces.BknAgentTask{TaskID: "k1", Status: interfaces.BKN_AGENT_TASK_FAILED}, nil)
				},
				func() { ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(answered("not json"), nil) },
				func() {
					ba.EXPECT().RunTask(gomock.Any(), caller, gomock.Any()).Return(answered(`{"answer":" "}`), nil)
				},
			} {
				readDraft(askDraft())
				run()

				_, err := ask("问")

				shouldBeHTTPError(err, http.StatusBadGateway, perrors.BknProject_Draft_AskUnavailable)
			}
		})

		Convey("A blank or long question is refused before anything is read\n", func() {
			for _, q := range []string{"  ", strings.Repeat("问", interfaces.DRAFT_ASK_QUESTION_MAX_LENGTH+1)} {
				_, err := ask(q)

				shouldBeHTTPError(err, http.StatusBadRequest, perrors.BknProject_Draft_InvalidQuestion)
			}
		})

		Convey("Only a ready draft's object types are asked about, and the Agent is not run otherwise\n", func() {
			d := askDraft()
			d.Status = interfaces.DRAFT_STATUS_DRAFTING
			readDraft(d)
			_, err := ask("问")
			shouldBeHTTPError(err, http.StatusConflict, perrors.BknProject_Draft_NotReady)

			readDraft(askDraft())
			_, err = svc.AskObjectType(ctx, token, "p1", "ghost", interfaces.DraftAskRequest{Question: "问"}, caller)
			shouldBeHTTPError(err, http.StatusNotFound, perrors.BknProject_Draft_ItemNotFound)
		})
	})
}
