// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"net/http"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	pmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces/mock"
)

func schedule(id, name, status string) *interfaces.ActionSchedule {
	return &interfaces.ActionSchedule{ID: id, Name: name, ActionTypeID: "restock", CronExpression: "0 2 * * *", Status: status}
}

func Test_LineageService_Switch(t *testing.T) {
	Convey("Test lineageService PlanSwitch and SwitchConsumers on a real git store\n", t, func() {
		mockCtrl := gomock.NewController(t)
		defer mockCtrl.Finish()

		bba := pmock.NewMockBknBackendAccess(mockCtrl)
		ra := pmock.NewMockReleaseAccess(mockCtrl)
		svc := &lineageService{gs: realGS, bba: bba, ra: ra}
		ctx := context.Background()
		operator := interfaces.AccountInfo{ID: "u1", Type: "user"}

		id := newLineageID()
		_, err := realGS.InitLineage(ctx, id, definitions("A part.", "A supplier."), "init", author)
		So(err, ShouldBeNil)
		v1 := id + "_v1_0_0"
		ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), v1).
			Return(&interfaces.Release{LineageID: id, NetworkID: v1}, true, nil).AnyTimes()
		active, inactive := interfaces.SCHEDULE_STATUS_ACTIVE, interfaces.SCHEDULE_STATUS_INACTIVE

		Convey("Before any switch the base network is in use, and the plan lists its active schedules\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(nil, false, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, id).
				Return([]*interfaces.ActionSchedule{schedule("s1", "nightly", active), schedule("s2", "paused", inactive)}, nil)

			plan, err := svc.PlanSwitch(ctx, token, id, v1)

			So(err, ShouldBeNil)
			So(plan.FromNetworkID, ShouldEqual, id)
			So(plan.ToNetworkID, ShouldEqual, v1)
			So(plan.Schedules, ShouldResemble, []*interfaces.ActionSchedule{schedule("s1", "nightly", active)})
		})

		Convey("A switch copies active schedules off, turns them on on the target and records it\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(nil, false, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, id).
				Return([]*interfaces.ActionSchedule{schedule("s1", "nightly", active), schedule("s2", "paused", inactive)}, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, v1).Return([]*interfaces.ActionSchedule{}, nil)
			copied := schedule("s1", "nightly", inactive)
			gomock.InOrder(
				bba.EXPECT().CreateSchedule(gomock.Any(), token, v1, copied).Return("t1", nil),
				bba.EXPECT().SetScheduleStatus(gomock.Any(), token, id, "s1", inactive).Return(nil),
				bba.EXPECT().SetScheduleStatus(gomock.Any(), token, v1, "t1", active).Return(nil),
			)
			var recorded *interfaces.Switch
			ra.EXPECT().RecordSwitch(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, sw *interfaces.Switch) error {
					recorded = sw
					return nil
				})

			sw, err := svc.SwitchConsumers(ctx, token, id, interfaces.SwitchRequest{ToNetworkID: v1}, operator)

			So(err, ShouldBeNil)
			So(sw, ShouldEqual, recorded)
			So(sw.LineageID, ShouldEqual, id)
			So(sw.FromNetworkID, ShouldEqual, id)
			So(sw.ToNetworkID, ShouldEqual, v1)
			So(sw.Operator, ShouldResemble, operator)
			So(sw.Schedules, ShouldResemble, []interfaces.SwitchedSchedule{{FromID: "s1", ToID: "t1", Name: "nightly"}})
			So(sw.ID, ShouldNotBeEmpty)
			So(sw.CreateTime, ShouldBeGreaterThan, 0)
		})

		Convey("A rollback turns the earlier counterparts back on instead of copying again\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(&interfaces.Switch{FromNetworkID: id, ToNetworkID: v1}, true, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, v1).Return([]*interfaces.ActionSchedule{schedule("t1", "nightly", active)}, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, id).Return([]*interfaces.ActionSchedule{schedule("s1", "nightly", inactive)}, nil)
			gomock.InOrder(
				bba.EXPECT().SetScheduleStatus(gomock.Any(), token, v1, "t1", inactive).Return(nil),
				bba.EXPECT().SetScheduleStatus(gomock.Any(), token, id, "s1", active).Return(nil),
			)
			ra.EXPECT().RecordSwitch(gomock.Any(), gomock.Any()).Return(nil)

			sw, err := svc.SwitchConsumers(ctx, token, id, interfaces.SwitchRequest{ToNetworkID: id}, operator)

			So(err, ShouldBeNil)
			So(sw.FromNetworkID, ShouldEqual, v1)
			So(sw.ToNetworkID, ShouldEqual, id)
			So(sw.Schedules, ShouldResemble, []interfaces.SwitchedSchedule{{FromID: "t1", ToID: "s1", Name: "nightly"}})
		})

		Convey("A counterpart already running on the target is left on, never run twice\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(nil, false, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, id).Return([]*interfaces.ActionSchedule{schedule("s1", "nightly", active)}, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, v1).Return([]*interfaces.ActionSchedule{schedule("t1", "nightly", active)}, nil)
			bba.EXPECT().SetScheduleStatus(gomock.Any(), token, id, "s1", inactive).Return(nil)
			ra.EXPECT().RecordSwitch(gomock.Any(), gomock.Any()).Return(nil)

			sw, err := svc.SwitchConsumers(ctx, token, id, interfaces.SwitchRequest{ToNetworkID: v1}, operator)

			So(err, ShouldBeNil)
			So(sw.Schedules, ShouldResemble, []interfaces.SwitchedSchedule{{FromID: "s1", ToID: "t1", Name: "nightly"}})
		})

		Convey("A failed copy turns nothing off and records nothing\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(nil, false, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, id).
				Return([]*interfaces.ActionSchedule{schedule("s1", "nightly", active), schedule("s3", "weekly", active)}, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, v1).Return([]*interfaces.ActionSchedule{}, nil)
			bba.EXPECT().CreateSchedule(gomock.Any(), token, v1, gomock.Any()).Return("t1", nil)
			bba.EXPECT().CreateSchedule(gomock.Any(), token, v1, gomock.Any()).
				Return("", &interfaces.BknBackendError{HTTPCode: http.StatusForbidden})

			_, err := svc.SwitchConsumers(ctx, token, id, interfaces.SwitchRequest{ToNetworkID: v1}, operator)

			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusForbidden)
			So(code, ShouldEqual, perrors.BknProject_Lineage_BknBackendRequestFailed)
		})

		Convey("A counterpart that does not turn on puts the schedule back on where it ran\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(nil, false, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, id).Return([]*interfaces.ActionSchedule{schedule("s1", "nightly", active)}, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, v1).Return([]*interfaces.ActionSchedule{schedule("t1", "nightly", inactive)}, nil)
			gomock.InOrder(
				bba.EXPECT().SetScheduleStatus(gomock.Any(), token, id, "s1", inactive).Return(nil),
				bba.EXPECT().SetScheduleStatus(gomock.Any(), token, v1, "t1", active).
					Return(&interfaces.BknBackendError{HTTPCode: http.StatusInternalServerError}),
				bba.EXPECT().SetScheduleStatus(gomock.Any(), token, id, "s1", active).Return(nil),
			)

			_, err := svc.SwitchConsumers(ctx, token, id, interfaces.SwitchRequest{ToNetworkID: v1}, operator)

			status, _ := httpCode(err)
			So(status, ShouldEqual, http.StatusBadGateway)
		})

		Convey("A retry records the schedules an earlier attempt moved without touching them\n", func() {
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(nil, false, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, id).Return([]*interfaces.ActionSchedule{schedule("s1", "nightly", inactive)}, nil)
			bba.EXPECT().ListSchedules(gomock.Any(), token, v1).Return([]*interfaces.ActionSchedule{schedule("t1", "nightly", active)}, nil)
			var recorded *interfaces.Switch
			ra.EXPECT().RecordSwitch(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, sw *interfaces.Switch) error {
					recorded = sw
					return nil
				})

			_, err := svc.SwitchConsumers(ctx, token, id, interfaces.SwitchRequest{ToNetworkID: v1}, operator)

			So(err, ShouldBeNil)
			So(recorded.Schedules, ShouldResemble, []interfaces.SwitchedSchedule{{FromID: "s1", ToID: "t1", Name: "nightly"}})
		})

		Convey("Targets outside the lineage, the network in use and unknown lineages are refused before any schedule is read\n", func() {
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "other_v1_0_0").
				Return(&interfaces.Release{LineageID: "other", NetworkID: "other_v1_0_0"}, true, nil)
			ra.EXPECT().GetReleaseByNetworkID(gomock.Any(), "x").Return(nil, false, nil)
			ra.EXPECT().GetLatestSwitch(gomock.Any(), id).Return(&interfaces.Switch{ToNetworkID: v1}, true, nil)

			for _, to := range []string{"", "other_v1_0_0", "x"} {
				_, err := svc.SwitchConsumers(ctx, token, id, interfaces.SwitchRequest{ToNetworkID: to}, operator)
				status, code := httpCode(err)
				So(status, ShouldEqual, http.StatusBadRequest)
				So(code, ShouldEqual, perrors.BknProject_Lineage_InvalidSwitchTarget)
			}

			_, err := svc.PlanSwitch(ctx, token, id, v1)
			status, code := httpCode(err)
			So(status, ShouldEqual, http.StatusConflict)
			So(code, ShouldEqual, perrors.BknProject_Lineage_AlreadyInUse)

			_, err = svc.PlanSwitch(ctx, token, newLineageID(), v1)
			_, code = httpCode(err)
			So(code, ShouldEqual, perrors.BknProject_Lineage_NotFound)
		})
	})
}
