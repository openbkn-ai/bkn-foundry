// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_schedule

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
	"bkn-backend/logics"
)

var (
	assOnce    sync.Once
	assService interfaces.ActionScheduleService
)

type actionScheduleService struct {
	appSetting *common.AppSetting
	asa        interfaces.ActionScheduleAccess
	ata        interfaces.ActionTypeAccess
	aea        interfaces.ActionExecutionAccess
	//db         interface{ Begin() (interface{}, error) }

	cronParser cron.Parser
}

func actionScheduleDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail."+name, templateData)
}

// NewActionScheduleService creates a singleton instance of ActionScheduleService
func NewActionScheduleService(appSetting *common.AppSetting) interfaces.ActionScheduleService {
	assOnce.Do(func() {
		assService = &actionScheduleService{
			appSetting: appSetting,
			asa:        logics.ASA,
			ata:        logics.ATA,
			aea:        logics.AEA,
			// Standard 5-field cron parser (minute, hour, day of month, month, day of week)
			cronParser: cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow),
		}
	})
	return assService
}

// CreateSchedule creates a new action schedule
func (s *actionScheduleService) CreateSchedule(ctx context.Context, schedule *interfaces.ActionSchedule) (string, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "CreateSchedule")
	defer span.End()

	// Validate cron expression
	if err := s.ValidateCronExpression(schedule.CronExpression); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidCronExpression).
			WithErrorDetails(actionScheduleDetail(ctx, "CronExpressionInvalid", nil))
		otellog.LogError(ctx, "Validate cron expression failed", err)
		return "", httpErr
	}

	// Validate action type exists
	actionTypes, err := s.ata.GetActionTypesByIDs(ctx, schedule.KNID, schedule.Branch, []string{schedule.ActionTypeID})
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetActionTypeFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to get action type", httpErr)
		return "", httpErr
	}
	if len(actionTypes) == 0 {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_ActionTypeNotFound).
			WithErrorDetails(actionScheduleDetail(ctx, "ActionTypeNotFound", map[string]any{"actionTypeID": schedule.ActionTypeID}))
		otellog.LogError(ctx, "Action type not found", httpErr)
		return "", httpErr
	}
	if common.GetAuthEnabled() {
		if err := s.checkActionExecution(ctx, "schedule_create", schedule.KNID, schedule.ActionTypeID, schedule.DynamicParams); err != nil {
			return "", err
		}
	}
	// Persist the current subject for future scheduled executions.
	schedule.ExecutionSubject = accountFromContext(ctx)

	// Generate ID and set defaults
	scheduleID, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate action schedule UUIDv7: %w", err)
	}
	schedule.ID = scheduleID.String()
	now := time.Now().UnixMilli()
	schedule.CreateTime = now
	schedule.UpdateTime = now

	if schedule.Status == "" {
		schedule.Status = interfaces.ScheduleStatusInactive
	}

	// Calculate next run time if status is active
	if schedule.Status == interfaces.ScheduleStatusActive {
		nextRunTime, err := s.CalculateNextRunTime(schedule.CronExpression, now)
		if err != nil {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidCronExpression).
				WithErrorDetails(actionScheduleDetail(ctx, "CronExpressionInvalid", nil))
			otellog.LogError(ctx, "Calculate next run time failed", err)
			return "", httpErr
		}
		schedule.NextRunTime = nextRunTime
	}

	// Create in database
	if err := s.asa.CreateSchedule(ctx, nil, schedule); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_CreateFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to create schedule", httpErr)
		return "", httpErr
	}

	logger.Infof("Created schedule: %s", schedule.ID)
	span.SetStatus(codes.Ok, "")
	return schedule.ID, nil
}

// UpdateSchedule updates an existing action schedule
func (s *actionScheduleService) UpdateSchedule(ctx context.Context, scheduleID string, req *interfaces.ActionScheduleUpdateRequest) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateSchedule")
	defer span.End()

	// Check if schedule exists
	existing, err := s.asa.GetSchedule(ctx, scheduleID)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to get schedule", httpErr)
		return httpErr
	}
	if existing == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_NotFound)
		otellog.LogError(ctx, "Schedule not found", httpErr)
		return httpErr
	}

	// Validate cron expression if provided
	cronExpr := existing.CronExpression
	if req.CronExpression != "" {
		if err := s.ValidateCronExpression(req.CronExpression); err != nil {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidCronExpression).
				WithErrorDetails(actionScheduleDetail(ctx, "CronExpressionInvalid", nil))
			otellog.LogError(ctx, "Validate cron expression failed", err)
			return httpErr
		}
		cronExpr = req.CronExpression
	}

	// Build update object
	now := time.Now().UnixMilli()
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}

	update := &interfaces.ActionSchedule{
		ID:             scheduleID,
		Name:           req.Name,
		CronExpression: req.CronExpression,
		Updater:        accountInfo,
		UpdateTime:     now,
	}

	if req.InstanceIdentities != nil {
		update.InstanceIdentities = req.InstanceIdentities
	}
	if req.DynamicParams != nil {
		update.DynamicParams = req.DynamicParams
	}

	executableConfigChanged := req.CronExpression != "" || req.InstanceIdentities != nil || req.DynamicParams != nil
	if common.GetAuthEnabled() && executableConfigChanged {
		dynamicParams := existing.DynamicParams
		if req.DynamicParams != nil {
			dynamicParams = req.DynamicParams
		}
		if err := s.checkActionExecution(ctx, "schedule_update", existing.KNID, existing.ActionTypeID, dynamicParams); err != nil {
			return err
		}
		update.ExecutionSubject = accountInfo
	}

	// Recalculate next run time if cron changed and schedule is active
	if req.CronExpression != "" && existing.Status == interfaces.ScheduleStatusActive {
		nextRunTime, err := s.CalculateNextRunTime(cronExpr, now)
		if err != nil {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidCronExpression).
				WithErrorDetails(actionScheduleDetail(ctx, "CronExpressionInvalid", nil))
			otellog.LogError(ctx, fmt.Sprintf("Failed to calculate next run time for schedule %s", scheduleID), err)
			return httpErr
		}
		update.NextRunTime = nextRunTime
	}

	if err := s.asa.UpdateSchedule(ctx, nil, update); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_UpdateFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to update schedule", httpErr)
		return httpErr
	}

	logger.Infof("Updated schedule: %s", scheduleID)
	span.SetStatus(codes.Ok, "")
	return nil
}

// UpdateScheduleStatus updates the status of a schedule
func (s *actionScheduleService) UpdateScheduleStatus(ctx context.Context, scheduleID string, status string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "UpdateScheduleStatus")
	defer span.End()

	// Validate status
	if status != interfaces.ScheduleStatusActive && status != interfaces.ScheduleStatusInactive {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidStatus).
			WithErrorDetails(actionScheduleDetail(ctx, "StatusValueInvalid", map[string]any{"status": status}))
		otellog.LogError(ctx, "Invalid schedule status", httpErr)
		return httpErr
	}

	// Check if schedule exists
	existing, err := s.asa.GetSchedule(ctx, scheduleID)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to get schedule", httpErr)
		return httpErr
	}
	if existing == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_NotFound)
		otellog.LogError(ctx, "Schedule not found", httpErr)
		return httpErr
	}

	// Calculate next run time when activating
	var nextRunTime int64
	if status == interfaces.ScheduleStatusActive {
		now := time.Now().UnixMilli()
		nextRunTime, err = s.CalculateNextRunTime(existing.CronExpression, now)
		if err != nil {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidCronExpression).
				WithErrorDetails(actionScheduleDetail(ctx, "CronExpressionInvalid", nil))
			otellog.LogError(ctx, "Calculate next run time failed", err)
			return httpErr
		}
	}

	if common.GetAuthEnabled() && status == interfaces.ScheduleStatusActive &&
		existing.Status != interfaces.ScheduleStatusActive {
		if err := s.checkActionExecution(ctx, "schedule_activate", existing.KNID, existing.ActionTypeID, existing.DynamicParams); err != nil {
			return err
		}
		accountInfo := accountFromContext(ctx)
		if err := s.asa.UpdateSchedule(ctx, nil, &interfaces.ActionSchedule{
			ID:               scheduleID,
			Status:           status,
			NextRunTime:      nextRunTime,
			ExecutionSubject: accountInfo,
			Updater:          accountInfo,
			UpdateTime:       time.Now().UnixMilli(),
		}); err != nil {
			httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_UpdateFailed).
				WithErrorDetails(err.Error())
			otellog.LogError(ctx, "Failed to update schedule status", httpErr)
			return httpErr
		}
		logger.Infof("Updated schedule %s status to %s", scheduleID, status)
		span.SetStatus(codes.Ok, "")
		return nil
	}

	if err := s.asa.UpdateScheduleStatus(ctx, scheduleID, status, nextRunTime); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_UpdateFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to update schedule status", httpErr)
		return httpErr
	}

	logger.Infof("Updated schedule %s status to %s", scheduleID, status)
	span.SetStatus(codes.Ok, "")
	return nil
}

// DeleteSchedules deletes schedules by IDs
func (s *actionScheduleService) DeleteSchedules(ctx context.Context, knID, branch string, scheduleIDs []string) error {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "DeleteSchedules")
	defer span.End()

	if len(scheduleIDs) == 0 {
		span.SetStatus(codes.Ok, "")
		return nil
	}

	// Verify all schedules exist and belong to the kn/branch
	schedules, err := s.asa.GetSchedules(ctx, scheduleIDs)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to get schedules", httpErr)
		return httpErr
	}

	for _, id := range scheduleIDs {
		schedule, exists := schedules[id]
		if !exists {
			httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_NotFound).
				WithErrorDetails(actionScheduleDetail(ctx, "ScheduleNotFound", map[string]any{"scheduleID": id}))
			otellog.LogError(ctx, "Schedule not found", httpErr)
			return httpErr
		}
		if schedule.KNID != knID || schedule.Branch != branch {
			httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
				WithErrorDetails(actionScheduleDetail(ctx, "ScheduleScopeMismatch", map[string]any{
					"scheduleID": id,
					"knID":       knID,
					"branch":     branch,
				}))
			otellog.LogError(ctx, "Schedule does not belong to request scope", httpErr)
			return httpErr
		}
	}

	if err := s.asa.DeleteSchedules(ctx, nil, scheduleIDs); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_DeleteFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to delete schedules", httpErr)
		return httpErr
	}

	logger.Infof("Deleted schedules: %v", scheduleIDs)
	span.SetStatus(codes.Ok, "")
	return nil
}

// GetSchedule gets a single schedule by ID
func (s *actionScheduleService) GetSchedule(ctx context.Context, scheduleID string) (*interfaces.ActionSchedule, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetSchedule")
	defer span.End()

	schedule, err := s.asa.GetSchedule(ctx, scheduleID)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to get schedule", httpErr)
		return nil, httpErr
	}
	if schedule == nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_ActionSchedule_NotFound)
		otellog.LogError(ctx, "Schedule not found", httpErr)
		return nil, httpErr
	}

	span.SetStatus(codes.Ok, "")
	return schedule, nil
}

// GetSchedules gets schedules by IDs
func (s *actionScheduleService) GetSchedules(ctx context.Context, scheduleIDs []string) (map[string]*interfaces.ActionSchedule, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetSchedules")
	defer span.End()

	schedules, err := s.asa.GetSchedules(ctx, scheduleIDs)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to get schedules", httpErr)
		return nil, httpErr
	}

	span.SetStatus(codes.Ok, "")
	return schedules, nil
}

// ListSchedules lists schedules with pagination
func (s *actionScheduleService) ListSchedules(ctx context.Context, queryParams interfaces.ActionScheduleQueryParams) ([]*interfaces.ActionSchedule, int64, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "ListSchedules")
	defer span.End()

	schedules, err := s.asa.ListSchedules(ctx, queryParams)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to list schedules", httpErr)
		return nil, 0, httpErr
	}

	total, err := s.asa.GetSchedulesTotal(ctx, queryParams)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_ActionSchedule_GetFailed).
			WithErrorDetails(err.Error())
		otellog.LogError(ctx, "Failed to get schedules total", httpErr)
		return nil, 0, httpErr
	}

	span.SetStatus(codes.Ok, "")
	return schedules, total, nil
}

// ValidateCronExpression validates a cron expression
func (s *actionScheduleService) ValidateCronExpression(cronExpr string) error {
	_, err := s.cronParser.Parse(cronExpr)
	if err != nil {
		return fmt.Errorf("invalid cron expression '%s': %v", cronExpr, err)
	}
	return nil
}

// CalculateNextRunTime calculates the next run time based on cron expression
func (s *actionScheduleService) CalculateNextRunTime(cronExpr string, from int64) (int64, error) {
	schedule, err := s.cronParser.Parse(cronExpr)
	if err != nil {
		return 0, fmt.Errorf("invalid cron expression: %v", err)
	}

	fromTime := time.UnixMilli(from)
	nextTime := schedule.Next(fromTime)
	return nextTime.UnixMilli(), nil
}

func (s *actionScheduleService) checkActionExecution(ctx context.Context, phase, knID, actionTypeID string,
	dynamicParams map[string]any) error {
	if s == nil || s.aea == nil {
		return rest.NewHTTPError(ctx, http.StatusServiceUnavailable,
			berrors.BknBackend_ActionSchedule_InternalError).WithErrorDetails("action execution authorization is not configured")
	}
	logger.Infof("Action execution permission check: phase=%s kn_id=%s action_type_id=%s", phase, knID, actionTypeID)
	return s.aea.CheckActionExecution(ctx, interfaces.ActionExecutionCheckRequest{
		KNID:          knID,
		ActionTypeID:  actionTypeID,
		DynamicParams: dynamicParams,
	})
}

func accountFromContext(ctx context.Context) interfaces.AccountInfo {
	if account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo); ok {
		return account
	}
	return interfaces.AccountInfo{}
}
