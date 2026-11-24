// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_schedule

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	bmock "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

type executorStub struct {
	ran *interfaces.ActionSchedule
	err error
}

func (s *executorStub) ExecuteSchedule(_ context.Context, schedule *interfaces.ActionSchedule) (string, error) {
	s.ran = schedule
	return "exec-1", s.err
}

type operationStub struct{ available bool }

func (s operationStub) Available() bool { return s.available }
func (operationStub) Prepare(context.Context, *sql.Tx, *interfaces.ActionType, *interfaces.BatchIDIndex) error {
	return nil
}
func (operationStub) MainChanged(context.Context, string) {}

func runTestService(t *testing.T, schedule *interfaces.ActionSchedule, permissionErr error) (*actionScheduleService, *executorStub) {
	t.Helper()
	ctrl := gomock.NewController(t)
	access := bmock.NewMockActionScheduleAccess(ctrl)
	permissions := bmock.NewMockPermissionService(ctrl)
	access.EXPECT().GetSchedule(gomock.Any(), "s1").Return(schedule, nil)
	if schedule != nil && schedule.KNID == "kn1" && schedule.Branch == interfaces.MAIN_BRANCH {
		permissions.EXPECT().CheckPermission(gomock.Any(), interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: "kn1"},
			[]string{interfaces.OPERATION_TYPE_MODIFY}).Return(permissionErr)
	}
	executor := &executorStub{}
	SetScheduleExecutor(executor)
	t.Cleanup(func() { SetScheduleExecutor(nil) })
	return &actionScheduleService{asa: access, ps: permissions}, executor
}

func runStatus(t *testing.T, err error) int {
	t.Helper()
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("%v is no HTTP error", err)
	}
	return httpErr.HTTPCode
}

func userSchedule() *interfaces.ActionSchedule {
	return &interfaces.ActionSchedule{ID: "s1", KNID: "kn1", Branch: interfaces.MAIN_BRANCH, ActionTypeID: "at1"}
}

func TestRunScheduleRunsItNowAsItsSubject(t *testing.T) {
	service, executor := runTestService(t, userSchedule(), nil)
	id, err := service.RunSchedule(context.Background(), "kn1", interfaces.MAIN_BRANCH, "s1")
	if err != nil || id != "exec-1" || executor.ran == nil || executor.ran.ID != "s1" {
		t.Fatalf("id %q err %v ran %+v", id, err, executor.ran)
	}
}

func TestRunScheduleOfAnotherNetworkIsNotFound(t *testing.T) {
	other := userSchedule()
	other.KNID = "kn2"
	service, executor := runTestService(t, other, nil)
	_, err := service.RunSchedule(context.Background(), "kn1", interfaces.MAIN_BRANCH, "s1")
	if runStatus(t, err) != http.StatusNotFound || executor.ran != nil {
		t.Fatalf("%v, want 404 without a run", err)
	}
}

func TestRunScheduleNeedsTheNetworksModifyPermission(t *testing.T) {
	denied := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)
	service, executor := runTestService(t, userSchedule(), denied)
	_, err := service.RunSchedule(context.Background(), "kn1", interfaces.MAIN_BRANCH, "s1")
	if runStatus(t, err) != http.StatusForbidden || executor.ran != nil {
		t.Fatalf("%v, want 403 without a run", err)
	}
}

// The schedule of a record-event action type runs only where its module is licensed.
func TestRunPlatformScheduleNeedsTheLicense(t *testing.T) {
	platform := userSchedule()
	platform.Source = interfaces.ActionScheduleSourceActionType
	for _, available := range []bool{false, true} {
		logics.RegisterActionTypeOperation(interfaces.ActionOperationEvent, operationStub{available: available})
		service, executor := runTestService(t, platform, nil)
		_, err := service.RunSchedule(context.Background(), "kn1", interfaces.MAIN_BRANCH, "s1")
		if available && (err != nil || executor.ran == nil) {
			t.Fatalf("licensed: %v", err)
		}
		if !available && (runStatus(t, err) != http.StatusNotFound || executor.ran != nil) {
			t.Fatalf("unlicensed: %v, want 404 without a run", err)
		}
	}
	logics.RegisterActionTypeOperation(interfaces.ActionOperationEvent, nil)
}

func TestRunScheduleWithoutTheWorkerIsUnavailable(t *testing.T) {
	service, _ := runTestService(t, userSchedule(), nil)
	SetScheduleExecutor(nil)
	_, err := service.RunSchedule(context.Background(), "kn1", interfaces.MAIN_BRANCH, "s1")
	if runStatus(t, err) != http.StatusServiceUnavailable {
		t.Fatalf("%v, want 503", err)
	}
}
