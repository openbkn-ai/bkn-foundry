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
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

// ValidateRiskTypes validates risk type creation requests.

func riskTypeInvalidDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(
		rest.GetLanguageByCtx(ctx),
		"BknBackend.RiskType.InvalidParameter.Detail."+name,
		templateData,
	)
}

func ValidateRiskTypes(ctx context.Context, knID string, riskTypes []*interfaces.RiskType) error {
	tmpNameMap := make(map[string]any)
	idMap := make(map[string]any)
	for i := range riskTypes {
		riskType := riskTypes[i]
		if riskType.ModuleType != "" && riskType.ModuleType != interfaces.MODULE_TYPE_RISK_TYPE {
			return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
				WithErrorDetails(riskTypeInvalidDetail(ctx, "ModuleType", nil))
		}

		rtID := riskType.RTID
		if _, ok := idMap[rtID]; !ok || rtID == "" {
			idMap[rtID] = nil
		} else {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RiskType_Duplicated_IDInFile).
				WithDescription(map[string]any{"riskTypeID": rtID}).
				WithErrorDetails(riskTypeInvalidDetail(ctx, "DuplicatedIDInFile", map[string]any{"riskTypeID": rtID}))
		}

		err := ValidateRiskType(ctx, riskType)
		if err != nil {
			return err
		}

		if _, ok := tmpNameMap[riskType.RTName]; !ok {
			tmpNameMap[riskType.RTName] = nil
		} else {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RiskType_Duplicated_Name).
				WithDescription(map[string]any{"riskTypeName": riskType.RTName}).
				WithErrorDetails(riskTypeInvalidDetail(ctx, "DuplicatedNameInFile", map[string]any{"riskTypeName": riskType.RTName}))
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
