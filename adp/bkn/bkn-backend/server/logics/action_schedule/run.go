// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_schedule

import (
	"context"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
)

var (
	executorMu sync.RWMutex
	executor   interfaces.ScheduleExecutor
)

// SetScheduleExecutor sets what runs a schedule now: the schedule worker.
func SetScheduleExecutor(e interfaces.ScheduleExecutor) {
	executorMu.Lock()
	defer executorMu.Unlock()
	executor = e
}

func scheduleExecutor() interfaces.ScheduleExecutor {
	executorMu.RLock()
	defer executorMu.RUnlock()
	return executor
}

// RunSchedule runs a schedule once now, as its execution subject, and leaves its next run as it
// was. Whoever may change the network may run its schedules: a user's schedule as they could
// change it, the platform's schedule of a record-event action type as they could change the check
// schedule on the action type. A record-event schedule needs its license, as when it runs on time.
func (s *actionScheduleService) RunSchedule(ctx context.Context, knID, branch, scheduleID string) (string, error) {
	schedule, err := s.asa.GetSchedule(ctx, scheduleID)
	if err != nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
	}
	if schedule == nil || schedule.KNID != knID || schedule.Branch != branch {
		return "", httperrors.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_NotFound)
	}
	if s.ps == nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_InternalError_CheckPermissionFailed)
	}
	if err := s.ps.CheckPermission(ctx, interfaces.PermissionResource{
		Type: interfaces.RESOURCE_TYPE_KN, ID: knID,
	}, []string{interfaces.OPERATION_TYPE_MODIFY}); err != nil {
		return "", err
	}
	if schedule.PlatformKept() {
		operation := logics.ActionTypeOperationNamed(interfaces.ActionOperationEvent)
		if operation == nil || !operation.Available() {
			return "", httperrors.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_NotFound)
		}
	}
	run := scheduleExecutor()
	if run == nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusServiceUnavailable, berrors.BknBackend_ActionSchedule_InternalError).
			WithErrorDetails("the schedule worker is not running")
	}
	executionID, err := run.ExecuteSchedule(ctx, schedule)
	if err != nil {
		return "", httperrors.NewHTTPError(ctx, http.StatusBadGateway, berrors.BknBackend_ActionSchedule_InternalError).
			WithErrorDetails(err.Error())
	}
	return executionID, nil
}
