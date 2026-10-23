// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"strings"

	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func ValidateRiskTypes(ctx context.Context, knID string, riskTypes []*interfaces.RiskType) error {
	tmpNameMap := make(map[string]any)
	idMap := make(map[string]any)
	for i := range riskTypes {
		riskType := riskTypes[i]
		if riskType.ModuleType != "" && riskType.ModuleType != interfaces.MODULE_TYPE_RISK_TYPE {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.RiskType.InvalidParameter.Detail.ModuleType", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
				WithErrorDetails(localizedText)
		}

		rtID := riskType.RTID
		if _, ok := idMap[rtID]; !ok || rtID == "" {
			idMap[rtID] = nil
		} else {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.RiskType.InvalidParameter.Detail.DuplicatedIDInFile", map[string]any{"riskTypeID": rtID})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RiskType_Duplicated_IDInFile).
				WithMetadata(map[string]any{"riskTypeID": rtID}).
				WithErrorDetails(localizedText)
		}

		err := ValidateRiskType(ctx, riskType)
		if err != nil {
			return err
		}

		if _, ok := tmpNameMap[riskType.RTName]; !ok {
			tmpNameMap[riskType.RTName] = nil
		} else {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.RiskType.InvalidParameter.Detail.DuplicatedNameInFile", map[string]any{"riskTypeName": riskType.RTName})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RiskType_Duplicated_Name).
				WithMetadata(map[string]any{"riskTypeName": riskType.RTName}).
				WithErrorDetails(localizedText)
		}

		riskType.KNID = knID
	}
	return nil
}

// ValidateRiskType validates a single risk type.
func ValidateRiskType(ctx context.Context, riskType *interfaces.RiskType) error {
	err := validateID(ctx, riskType.RTID)
	if err != nil {
		return err
	}

	riskType.RTName = strings.TrimSpace(riskType.RTName)
	err = validateObjectName(ctx, riskType.RTName, interfaces.MODULE_TYPE_RISK_TYPE)
	if err != nil {
		return err
	}

	if err = ValidateTags(ctx, riskType.Tags); err != nil {
		return err
	}
	riskType.Tags = libCommon.TagSliceTransform(riskType.Tags)

	return nil
}
