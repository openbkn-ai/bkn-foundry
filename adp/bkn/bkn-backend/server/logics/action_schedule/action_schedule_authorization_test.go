// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_schedule

import (
	"context"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
)

type actionExecutionAccessStub struct {
	calls    int
	request  interfaces.ActionExecutionCheckRequest
	checkErr error
}

func (s *actionExecutionAccessStub) CheckActionExecution(_ context.Context,
	request interfaces.ActionExecutionCheckRequest) error {
	s.calls++
	s.request = request
	return s.checkErr
}

func actionScheduleAuthorizationTestService(t *testing.T) (*actionScheduleService,
	*bmock.MockActionScheduleAccess, *bmock.MockActionTypeAccess, *actionExecutionAccessStub) {
	t.Helper()
	ctrl := gomock.NewController(t)
	access := bmock.NewMockActionScheduleAccess(ctrl)
	actionTypes := bmock.NewMockActionTypeAccess(ctrl)
	checks := &actionExecutionAccessStub{}
	return &actionScheduleService{
		asa: access,
		ata: actionTypes,
		aea: checks,
		cronParser: cron.NewParser(
			cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
	}, access, actionTypes, checks
}

func actionScheduleSubjectContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
		ID: "user-current", Type: "user", Name: "Current User",
	})
}

func schedulableActionType(id string) *interfaces.ActionType {
	return &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID: id, ActionType: interfaces.ACTION_TYPE_MODIFY,
		OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationObject},
	}}
}

func TestCreateScheduleChecksAndStoresCurrentExecutionSubject(t *testing.T) {
	service, schedules, actionTypes, checks := actionScheduleAuthorizationTestService(t)
	actionTypes.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"}).
		Return([]*interfaces.ActionType{schedulableActionType("at-1")}, nil)
	schedules.EXPECT().CreateSchedule(gomock.Any(), nil, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ any, schedule *interfaces.ActionSchedule) error {
			if schedule.ExecutionSubject.ID != "user-current" || schedule.ExecutionSubject.Type != "user" {
				t.Fatalf("execution subject = %#v", schedule.ExecutionSubject)
			}
			return nil
		})

	_, err := service.CreateSchedule(actionScheduleSubjectContext(), &interfaces.ActionSchedule{
		KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ActionTypeID: "at-1",
		CronExpression: "* * * * *", DynamicParams: map[string]any{"threshold": 3},
		InstanceIdentities: []map[string]any{{"id": "target-1"}},
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if checks.calls != 1 || checks.request.KNID != "kn-1" || checks.request.ActionTypeID != "at-1" {
		t.Fatalf("permission checks = %d, request = %#v", checks.calls, checks.request)
	}
}

func TestUpdateScheduleRotatesSubjectOnlyForExecutableChanges(t *testing.T) {

	t.Run("name only keeps the current subject", func(t *testing.T) {
		service, schedules, _, checks := actionScheduleAuthorizationTestService(t)
		schedules.EXPECT().GetSchedule(gomock.Any(), "schedule-1").Return(&interfaces.ActionSchedule{
			ID: "schedule-1", KNID: "kn-1", ActionTypeID: "at-1", CronExpression: "* * * * *",
			ExecutionSubject: interfaces.AccountInfo{ID: "user-original", Type: "user"},
		}, nil)
		schedules.EXPECT().UpdateSchedule(gomock.Any(), nil, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ any, update *interfaces.ActionSchedule) error {
				if update.ExecutionSubject.ID != "" {
					t.Fatalf("name-only update rotated execution subject: %#v", update.ExecutionSubject)
				}
				return nil
			})

		if err := service.UpdateSchedule(actionScheduleSubjectContext(), "schedule-1",
			&interfaces.ActionScheduleUpdateRequest{Name: "renamed"}); err != nil {
			t.Fatalf("UpdateSchedule() error = %v", err)
		}
		if checks.calls != 0 {
			t.Fatalf("permission checks = %d, want 0", checks.calls)
		}
	})

	t.Run("dynamic parameters recheck and rotate the subject", func(t *testing.T) {
		service, schedules, actionTypes, checks := actionScheduleAuthorizationTestService(t)
		schedules.EXPECT().GetSchedule(gomock.Any(), "schedule-1").Return(&interfaces.ActionSchedule{
			ID: "schedule-1", KNID: "kn-1", ActionTypeID: "at-1", CronExpression: "* * * * *",
			DynamicParams:      map[string]any{"threshold": 1},
			InstanceIdentities: []map[string]any{{"id": "target-1"}},
		}, nil)
		actionTypes.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", "", []string{"at-1"}).
			Return([]*interfaces.ActionType{schedulableActionType("at-1")}, nil)
		schedules.EXPECT().UpdateSchedule(gomock.Any(), nil, gomock.Any()).DoAndReturn(
			func(_ context.Context, _ any, update *interfaces.ActionSchedule) error {
				if update.ExecutionSubject.ID != "user-current" || update.ExecutionSubject.Type != "user" {
					t.Fatalf("execution subject = %#v", update.ExecutionSubject)
				}
				return nil
			})

		params := map[string]any{"threshold": 5}
		if err := service.UpdateSchedule(actionScheduleSubjectContext(), "schedule-1",
			&interfaces.ActionScheduleUpdateRequest{DynamicParams: params}); err != nil {
			t.Fatalf("UpdateSchedule() error = %v", err)
		}
		if checks.calls != 1 || checks.request.DynamicParams["threshold"] != 5 {
			t.Fatalf("permission checks = %d, request = %#v", checks.calls, checks.request)
		}
	})
}

func TestActivateScheduleRechecksAndRotatesExecutionSubject(t *testing.T) {
	service, schedules, actionTypes, checks := actionScheduleAuthorizationTestService(t)
	schedules.EXPECT().GetSchedule(gomock.Any(), "schedule-1").Return(&interfaces.ActionSchedule{
		ID: "schedule-1", KNID: "kn-1", ActionTypeID: "at-1", CronExpression: "* * * * *",
		Status:             interfaces.ScheduleStatusInactive,
		InstanceIdentities: []map[string]any{{"id": "target-1"}},
	}, nil)
	actionTypes.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", "", []string{"at-1"}).
		Return([]*interfaces.ActionType{schedulableActionType("at-1")}, nil)
	schedules.EXPECT().UpdateSchedule(gomock.Any(), nil, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ any, update *interfaces.ActionSchedule) error {
			if update.Status != interfaces.ScheduleStatusActive || update.ExecutionSubject.ID != "user-current" {
				t.Fatalf("activation update = %#v", update)
			}
			return nil
		})

	if err := service.UpdateScheduleStatus(actionScheduleSubjectContext(), "schedule-1",
		interfaces.ScheduleStatusActive); err != nil {
		t.Fatalf("UpdateScheduleStatus() error = %v", err)
	}
	if checks.calls != 1 {
		t.Fatalf("permission checks = %d, want 1", checks.calls)
	}
}

func TestScheduleRejectsTargetsTheLegacyTransportCannotRepresent(t *testing.T) {
	for _, test := range []struct {
		name   string
		action *interfaces.ActionType
	}{
		{"new object", &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "at-1", ActionType: interfaces.ACTION_TYPE_ADD,
			OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationObject},
		}}},
		{"relation", &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "at-1", OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationRelation},
		}}},
		{"tool", &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: "at-1", OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationTool},
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, actionTypes, checks := actionScheduleAuthorizationTestService(t)
			actionTypes.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"}).
				Return([]*interfaces.ActionType{test.action}, nil)
			_, err := service.CreateSchedule(actionScheduleSubjectContext(), &interfaces.ActionSchedule{
				KNID: "kn-1", Branch: interfaces.MAIN_BRANCH, ActionTypeID: "at-1",
				CronExpression: "* * * * *", InstanceIdentities: []map[string]any{{"id": "target-1"}},
			})
			if err == nil || !strings.Contains(err.Error(), "supports only modify or delete") || checks.calls != 0 {
				t.Fatalf("CreateSchedule() = %v, checks=%d; want unsupported operation before persistence", err, checks.calls)
			}
		})
	}
}

func TestScheduleUpdateAndActivationRejectUnsupportedOperation(t *testing.T) {
	for _, update := range []bool{true, false} {
		t.Run(map[bool]string{true: "update", false: "activate"}[update], func(t *testing.T) {
			service, schedules, actionTypes, checks := actionScheduleAuthorizationTestService(t)
			schedules.EXPECT().GetSchedule(gomock.Any(), "schedule-1").Return(&interfaces.ActionSchedule{
				ID: "schedule-1", KNID: "kn-1", Branch: interfaces.MAIN_BRANCH,
				ActionTypeID: "at-1", CronExpression: "* * * * *", Status: interfaces.ScheduleStatusInactive,
				InstanceIdentities: []map[string]any{{"id": "target-1"}},
			}, nil)
			actionTypes.EXPECT().GetActionTypesByIDs(gomock.Any(), "kn-1", interfaces.MAIN_BRANCH, []string{"at-1"}).
				Return([]*interfaces.ActionType{{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
					ATID: "at-1", OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationTool},
				}}}, nil)
			var err error
			if update {
				err = service.UpdateSchedule(actionScheduleSubjectContext(), "schedule-1", &interfaces.ActionScheduleUpdateRequest{DynamicParams: map[string]any{"x": 1}})
			} else {
				err = service.UpdateScheduleStatus(actionScheduleSubjectContext(), "schedule-1", interfaces.ScheduleStatusActive)
			}
			if err == nil || !strings.Contains(err.Error(), "supports only modify or delete") || checks.calls != 0 {
				t.Fatalf("schedule change = %v, checks=%d; want unsupported operation", err, checks.calls)
			}
		})
	}
}
