// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func actionScheduleDetail(ctx context.Context, name string) string {
	return i18n.Translate(
		rest.GetLanguageByCtx(ctx),
		"BknBackend.ActionSchedule.Detail."+name,
		nil,
	)
}

// ValidateActionScheduleCreate validates the create schedule request
func ValidateActionScheduleCreate(ctx context.Context, req *interfaces.ActionScheduleCreateRequest) error {
	// Validate name
	if req.Name == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(actionScheduleDetail(ctx, "NameRequired"))
	}
	if len(req.Name) > 100 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(actionScheduleDetail(ctx, "NameLength"))
	}

	// Validate action_type_id
	if req.ActionTypeID == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(actionScheduleDetail(ctx, "ActionTypeIDRequired"))
	}

	// Validate cron_expression
	if req.CronExpression == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(actionScheduleDetail(ctx, "CronExpressionRequired"))
	}

	// Validate _instance_identities
	if len(req.InstanceIdentities) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(actionScheduleDetail(ctx, "InstanceIdentitiesRequired"))
	}

	// Validate status if provided
	if req.Status != "" && req.Status != interfaces.ScheduleStatusActive && req.Status != interfaces.ScheduleStatusInactive {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidStatus).
			WithErrorDetails(actionScheduleDetail(ctx, "StatusInvalid"))
	}

	return nil
}

// ValidateActionScheduleUpdate validates the update schedule request
func ValidateActionScheduleUpdate(ctx context.Context, req *interfaces.ActionScheduleUpdateRequest) error {
	// Validate name if provided
	if req.Name != "" && len(req.Name) > 100 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(actionScheduleDetail(ctx, "NameLength"))
	}

	// At least one field should be provided
	if req.Name == "" && req.CronExpression == "" && req.InstanceIdentities == nil && req.DynamicParams == nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(actionScheduleDetail(ctx, "UpdateFieldRequired"))
	}

	return nil
}
