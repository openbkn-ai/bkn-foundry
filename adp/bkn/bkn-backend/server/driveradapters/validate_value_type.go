// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// ValidateValueTypes checks the value types of one request or file: each on its own, and no ID or
// name twice. The value rules are checked by the service, with the checks a 3.0 import runs.
func ValidateValueTypes(ctx context.Context, valueTypes []*interfaces.ValueType) error {
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, vt := range valueTypes {
		if vt == nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_InvalidParameter)
		}
		if err := ValidateValueType(ctx, vt); err != nil {
			return err
		}
		if vt.VTID != "" {
			if ids[vt.VTID] {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ValueType.InvalidParameter.Detail.DuplicatedIDInFile", map[string]any{"valueTypeID": vt.VTID})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_Duplicated_IDInFile).
					WithErrorDetails(localizedText)
			}
			ids[vt.VTID] = true
		}
		if names[vt.VTName] {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ValueType.InvalidParameter.Detail.DuplicatedNameInFile", map[string]any{"valueTypeName": vt.VTName})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_Duplicated_Name).
				WithErrorDetails(localizedText)
		}
		names[vt.VTName] = true
	}
	return nil
}

// ValidateValueType checks the fields of one value type every definition shares: module type, ID,
// name and tags.
func ValidateValueType(ctx context.Context, vt *interfaces.ValueType) error {
	if vt.ModuleType != "" && vt.ModuleType != interfaces.MODULE_TYPE_VALUE_TYPE {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ValueType.InvalidParameter.Detail.ModuleType", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ModuleType).
			WithErrorDetails(localizedText)
	}
	if err := validateID(ctx, vt.VTID); err != nil {
		return err
	}
	vt.VTName = strings.TrimSpace(vt.VTName)
	if err := validateObjectName(ctx, vt.VTName, interfaces.MODULE_TYPE_VALUE_TYPE); err != nil {
		return err
	}
	if err := ValidateTags(ctx, vt.Tags); err != nil {
		return err
	}
	vt.Tags = libCommon.TagSliceTransform(vt.Tags)
	vt.BaseType = strings.ToLower(strings.TrimSpace(vt.BaseType))
	for n, item := range vt.Enum {
		if item == nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ValueType_InvalidParameter).
				WithErrorDetails(fmt.Sprintf("value type %q enum item %d is null", vt.VTID, n+1))
		}
	}
	return nil
}
