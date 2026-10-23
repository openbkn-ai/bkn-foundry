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
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// ValidateActionScheduleCreate validates the create schedule request
func ValidateActionScheduleCreate(ctx context.Context, req *interfaces.ActionScheduleCreateRequest) error {
	// Validate name
	if req.Name == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.NameRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if len(req.Name) > 100 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.NameLength", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Validate action_type_id
	if req.ActionTypeID == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.ActionTypeIDRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Validate cron_expression
	if req.CronExpression == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.CronExpressionRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Validate _instance_identities
	if len(req.InstanceIdentities) == 0 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.InstanceIdentitiesRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Validate status if provided
	if req.Status != "" && req.Status != interfaces.ScheduleStatusActive && req.Status != interfaces.ScheduleStatusInactive {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.StatusInvalid", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidStatus).
			WithErrorDetails(localizedText)
	}

	return nil
}

// ValidateActionScheduleUpdate validates the update schedule request
func ValidateActionScheduleUpdate(ctx context.Context, req *interfaces.ActionScheduleUpdateRequest) error {
	// Validate name if provided
	if req.Name != "" && len(req.Name) > 100 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.NameLength", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// At least one field should be provided
	if req.Name == "" && req.CronExpression == "" && req.InstanceIdentities == nil && req.DynamicParams == nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionSchedule.Detail.UpdateFieldRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionSchedule_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	return nil
}
