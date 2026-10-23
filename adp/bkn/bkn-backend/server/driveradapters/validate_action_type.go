// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
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

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func ValidateActionTypes(ctx context.Context, knID string, actionTypes []*interfaces.ActionType, strictMode bool) error {
	tmpNameMap := make(map[string]any)
	idMap := make(map[string]any)
	for i := 0; i < len(actionTypes); i++ {
		// Verify that imported models are action types.
		if actionTypes[i].ModuleType != "" && actionTypes[i].ModuleType != interfaces.MODULE_TYPE_ACTION_TYPE {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ModuleType", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
				WithErrorDetails(localizedText)
		}

		// Verify that model IDs in the request are unique.
		atID := actionTypes[i].ATID
		if _, ok := idMap[atID]; !ok || atID == "" {
			idMap[atID] = nil
		} else {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.DuplicatedIDInFile", map[string]any{"actionTypeID": atID})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			errDetails := localizedText
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_Duplicated_IDInFile).
				WithMetadata(map[string]any{"actionTypeID": atID}).
				WithErrorDetails(errDetails)
		}

		// Validate required action type fields, lengths, and enum values.
		err := ValidateActionType(ctx, actionTypes[i], strictMode)
		if err != nil {
			return err
		}

		// Verify that action type names in the request are unique.
		if _, ok := tmpNameMap[actionTypes[i].ATName]; !ok {
			tmpNameMap[actionTypes[i].ATName] = nil
		} else {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_Duplicated_Name)
		}

		actionTypes[i].KNID = knID
	}
	return nil
}

// validateActionType validates required action type fields.
func ValidateActionType(ctx context.Context, actionType *interfaces.ActionType, strictMode bool) error {
	// Validate the ID.
	err := validateID(ctx, actionType.ATID)
	if err != nil {
		return err
	}

	// Trim and validate the name.
	actionType.ATName = strings.TrimSpace(actionType.ATName)
	err = validateObjectName(ctx, actionType.ATName, interfaces.MODULE_TYPE_ACTION_TYPE)
	if err != nil {
		return err
	}

	// Validate tags when provided.
	err = ValidateTags(ctx, actionType.Tags)
	if err != nil {
		return err
	}

	// Trim tags and remove duplicates.
	actionType.Tags = libCommon.TagSliceTransform(actionType.Tags)

	err = syncIntentWithType(ctx, actionType)
	if err != nil {
		return err
	}

	// Validate the action type.
	if !interfaces.ActionTypeMap[actionType.ActionType] {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ActionTypeInvalid", map[string]any{"actionType": actionType.ActionType})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	// Validate conditions and parameters according to the object type binding.
	if actionType.ObjectTypeID == "" && strictMode {
		// Conditions must be empty when no object type is bound.
		if actionType.Condition != nil {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionRequiresObjectType", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}

		// Without an object type, parameter ValueFrom can only be const or input.
		for _, param := range actionType.Parameters {
			if param.ValueFrom == interfaces.VALUE_FROM_PROPERTY {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.PropertyValueRequiresObjectType", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
		}
	}

	// Validate the type.
	if actionType.ActionSource.Type != "" {
		// A non-empty type indicates a configured mapping that must be validated.
		if !interfaces.IsValidActionSourceType(actionType.ActionSource.Type) {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ActionSourceTypeInvalid", map[string]any{"actionSourceType": actionType.ActionSource.Type})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		// strict_mode off: allow empty or draft combinations for McpID, ToolName, BoxID, ToolID (no cross-kind checks).
		if strictMode {
			switch actionType.ActionSource.Type {
			case interfaces.ACTION_SOURCE_TYPE_TOOL:
				// For tool mappings, mcp_id and tool_name must be empty.
				if actionType.ActionSource.McpID != "" || actionType.ActionSource.ToolName != "" {
					localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ToolSourceContainsMCP", map[string]any{"mcpID": actionType.ActionSource.McpID, "toolName": actionType.ActionSource.ToolName})
					if renderErr != nil {
						logger.Errorf("localized message rendering failed: %v", renderErr)
					}
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).WithErrorDetails(localizedText)
				}
			case interfaces.ACTION_SOURCE_TYPE_MCP:
				// For map mappings, box_id and tool_id must be empty.
				if actionType.ActionSource.BoxID != "" || actionType.ActionSource.ToolID != "" {
					localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.MCPSourceContainsTool", map[string]any{"boxID": actionType.ActionSource.BoxID, "toolID": actionType.ActionSource.ToolID})
					if renderErr != nil {
						logger.Errorf("localized message rendering failed: %v", renderErr)
					}
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).WithErrorDetails(localizedText)
				}
			}
		}
	}

	// Parameter names are required when parameters are provided.
	if len(actionType.Parameters) > 0 {
		for _, param := range actionType.Parameters {
			if param.Name == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ParameterNameRequired", map[string]any{"actionType": actionType.ATName})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
		}
	}

	err = syncImpactAffect(ctx, actionType)
	if err != nil {
		return err
	}

	if err = validateAffectExpectedOperation(ctx, actionType.Affect); err != nil {
		return err
	}

	err = validateImpactContracts(ctx, actionType.ImpactContracts)
	if err != nil {
		return err
	}

	if actionType.Condition != nil {
		if strictMode {
			err = validateActionCondition(ctx, actionType.Condition, actionType.ObjectTypeID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// syncIntentWithType keeps action_type and action_intent consistent and fills the missing one.
func syncIntentWithType(ctx context.Context, actionType *interfaces.ActionType) error {
	at := strings.TrimSpace(actionType.ActionType)
	ai := strings.TrimSpace(actionType.ActionIntent)
	actionType.ActionType = at
	actionType.ActionIntent = ai
	if at != "" && ai != "" && at != ai {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ActionIntentMismatch", map[string]any{"actionIntent": ai, "actionType": at})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if at != "" && ai == "" {
		actionType.ActionIntent = at
	}
	if ai != "" && at == "" {
		actionType.ActionType = ai
	}
	return nil
}

// validateAffectExpectedOperation validates affect.expected_operation when supplied; folded entries still use action_type.
func validateAffectExpectedOperation(ctx context.Context, aff *interfaces.ActionAffect) error {
	if aff == nil {
		return nil
	}
	op := strings.TrimSpace(string(aff.ExpectedOperation))
	if op == "" {
		return nil
	}
	if !interfaces.IsValidExpectedOperation(op) {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.AffectExpectedOperationInvalid", map[string]any{"expectedOperation": op})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	return nil
}

func validateImpactContracts(ctx context.Context, items []interfaces.ImpactContractItem) error {
	for i := range items {
		if strings.TrimSpace(items[i].ObjectTypeID) == "" {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ImpactContractObjectTypeRequired", map[string]any{"index": i})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		op := strings.TrimSpace(string(items[i].ExpectedOperation))
		if op == "" {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ImpactContractExpectedOperationRequired", map[string]any{"index": i})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		if !interfaces.IsValidExpectedOperation(op) {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ImpactContractExpectedOperationInvalid", map[string]any{"index": i, "expectedOperation": op})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		for j := range items[i].AffectedFields {
			if strings.TrimSpace(items[i].AffectedFields[j]) == "" {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ImpactContractAffectedFieldRequired", map[string]any{"index": i, "fieldIndex": j})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
		}
	}
	return nil
}

// syncImpactAffect rejects requests containing both affect and impact_contracts, except for a folded affect entry.
// When only affect is provided, it appends an impact_contracts entry without changing affect.
func syncImpactAffect(ctx context.Context, actionType *interfaces.ActionType) error {
	hasIC := len(actionType.ImpactContracts) > 0
	hasAff := actionType.Affect != nil
	if hasIC && hasAff {
		if foldedImpactMatchesAffect(actionType) {
			return nil
		}
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.AffectAndImpactContractsConflict", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(localizedText)
	}
	if !hasAff || hasIC {
		return nil
	}
	a := actionType.Affect
	fields := make([]string, len(a.AffectedFields))
	copy(fields, a.AffectedFields)
	actionType.ImpactContracts = []interfaces.ImpactContractItem{
		{
			ObjectTypeID:      strings.TrimSpace(a.ObjectTypeID),
			ExpectedOperation: actionType.ActionType,
			Description:       strings.TrimSpace(a.Comment),
			AffectedFields:    fields,
		},
	}
	return nil
}

// foldedImpactMatchesAffect reports whether the only impact_contracts entry was folded from affect.
func foldedImpactMatchesAffect(at *interfaces.ActionType) bool {
	if len(at.ImpactContracts) != 1 || at.Affect == nil {
		return false
	}
	ic := at.ImpactContracts[0]
	a := at.Affect
	if strings.TrimSpace(ic.ObjectTypeID) != strings.TrimSpace(a.ObjectTypeID) {
		return false
	}
	if string(ic.ExpectedOperation) != at.ActionType {
		return false
	}
	if strings.TrimSpace(ic.Description) != strings.TrimSpace(a.Comment) {
		return false
	}
	if len(ic.AffectedFields) != len(a.AffectedFields) {
		return false
	}
	for i := range ic.AffectedFields {
		if strings.TrimSpace(ic.AffectedFields[i]) != strings.TrimSpace(a.AffectedFields[i]) {
			return false
		}
	}
	return true
}

// validateActionCondition validates action conditions.
func validateActionCondition(ctx context.Context, cfg *interfaces.ActionCondCfg, objectTypeID string) error {
	return validateActionConditionWithPath(ctx, cfg, objectTypeID, "condition")
}

func validateActionConditionWithPath(ctx context.Context, cfg *interfaces.ActionCondCfg, objectTypeID string, path string) error {
	if cfg == nil {
		return nil
	}

	// Default a missing condition object type ID to the action type object type ID.
	if cfg.ObjectTypeID == "" {
		cfg.ObjectTypeID = objectTypeID
	}
	// if cfg.ObjectTypeID == "" {
	// 	return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
	// 		WithErrorDetails("The action condition object type is required.")
	// }

	// Validate the filter operator.
	if cfg.Operation == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionOperationRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	_, exists := interfaces.ActionCondOperationMap[cfg.Operation]
	if !exists {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionOperationUnsupported", map[string]any{"operation": cfg.Operation})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(localizedText)
	}

	switch cfg.Operation {
	case cond.OperationAnd, cond.OperationOr:
		// A filter may contain at most 100 subconditions.
		if len(cfg.SubConds) == 0 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionSubconditionsRequired", map[string]any{"path": path, "operation": cfg.Operation})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).WithErrorDetails(localizedText)
		}

		if len(cfg.SubConds) > cond.MaxSubCondition {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionSubconditionsExceeded", map[string]any{"limit": cond.MaxSubCondition})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_CountExceeded_Conditions).
				WithErrorDetails(localizedText)
		}

		for i, subCond := range cfg.SubConds {
			err := validateActionConditionWithPath(ctx, subCond, objectTypeID, fmt.Sprintf("%s.sub_conditions[%d]", path, i))
			if err != nil {
				return err
			}
		}
	default:
		// The filter field name is required.
		if cfg.Field == "" {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionFieldRequired", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)

		}
	}

	switch cfg.Operation {
	case cond.OperationEq, cond.OperationNotEq, cond.OperationGt, cond.OperationGte, cond.OperationLt, cond.OperationLte:
		// The right-hand side contains a single value.
		_, ok := cfg.Value.([]any)
		if ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionSingleValueRequired", map[string]any{"operation": cfg.Operation})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}

	case cond.OperationIn, cond.OperationNotIn:
		// For in and not_in, value is a non-empty array of primitive values.
		_, ok := cfg.Value.([]any)
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionArrayRequired", map[string]any{"operations": "in, not_in"})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}

		if len(cfg.Value.([]any)) <= 0 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionArrayNonEmpty", map[string]any{"operations": "in, not_in"})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
	case cond.OperationRange, cond.OperationOutRange, cond.OperationBefore, cond.OperationBetween:
		// For range, value is a two-element numeric array containing inclusive bounds.
		// For out_range, value is a two-element numeric array defining (-inf, value[0]) or [value[1], +inf).
		v, ok := cfg.Value.([]any)
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionRangeArrayRequired", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}

		if len(v) != 2 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionRangeArrayLength", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
	case cond.OperationExist, cond.OperationNotExist, cond.OperationNull, cond.OperationNotNull:
		// exist, not_exist, null, and not_null do not require a value.
		// These operators are already covered by NotRequiredValueOperationMap.

	case cond.OperationLike, cond.OperationNotLike, cond.OperationPrefix, cond.OperationNotPrefix, cond.OperationRegex:
		// like, not_like, prefix, not_prefix, and regex require a single string value.
		_, ok := cfg.Value.([]any)
		if ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionSingleStringValueRequired", map[string]any{"operation": cfg.Operation})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		_, ok = cfg.Value.(string)
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionStringValueRequired", map[string]any{"operation": cfg.Operation})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}

	case cond.OperationContain, cond.OperationNotContain:
		// contain and not_contain accept one value or a non-empty array.
		if cfg.Value == nil {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionValueRequired", map[string]any{"operation": cfg.Operation})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		if arr, ok := cfg.Value.([]any); ok {
			if len(arr) <= 0 {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionValueArrayNonEmpty", map[string]any{"operation": cfg.Operation})
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(localizedText)
			}
		}

	case cond.OperationCurrent:
		// current requires a string unit, not an array.
		_, ok := cfg.Value.([]any)
		if ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionCurrentValueNotArray", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		unit, ok := cfg.Value.(string)
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionCurrentValueStringRequired", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
		// Validate the unit value.
		validUnits := map[string]bool{
			"year":   true,
			"month":  true,
			"week":   true,
			"day":    true,
			"hour":   true,
			"minute": true,
		}
		if !validUnits[unit] {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail.ConditionCurrentUnitInvalid", map[string]any{"unit": unit})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(localizedText)
		}
	}

	return nil
}
