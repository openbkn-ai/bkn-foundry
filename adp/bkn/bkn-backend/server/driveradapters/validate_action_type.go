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
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func actionTypeInvalidDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.ActionType.InvalidParameter.Detail."+name, templateData)
}

func ValidateActionTypes(ctx context.Context, knID string, actionTypes []*interfaces.ActionType, strictMode bool) error {
	tmpNameMap := make(map[string]any)
	idMap := make(map[string]any)
	for i := 0; i < len(actionTypes); i++ {
		// Verify that imported models are action types.
		if actionTypes[i].ModuleType != "" && actionTypes[i].ModuleType != interfaces.MODULE_TYPE_ACTION_TYPE {
			return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ModuleType", nil))
		}

		// Verify that model IDs in the request are unique.
		atID := actionTypes[i].ATID
		if _, ok := idMap[atID]; !ok || atID == "" {
			idMap[atID] = nil
		} else {
			errDetails := actionTypeInvalidDetail(ctx, "DuplicatedIDInFile", map[string]any{"actionTypeID": atID})
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_Duplicated_IDInFile).
				WithDescription(map[string]any{"actionTypeID": atID}).
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(actionTypeInvalidDetail(ctx, "ActionTypeInvalid", map[string]any{"actionType": actionType.ActionType}))
	}

	// Validate conditions and parameters according to the object type binding.
	if actionType.ObjectTypeID == "" && strictMode {
		// Conditions must be empty when no object type is bound.
		if actionType.Condition != nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionRequiresObjectType", nil))
		}

		// Without an object type, parameter ValueFrom can only be const or input.
		for _, param := range actionType.Parameters {
			if param.ValueFrom == interfaces.VALUE_FROM_PROPERTY {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(actionTypeInvalidDetail(ctx, "PropertyValueRequiresObjectType", nil))
			}
		}
	}

	// Validate the type.
	if actionType.ActionSource.Type != "" {
		// A non-empty type indicates a configured mapping that must be validated.
		if !interfaces.IsValidActionSourceType(actionType.ActionSource.Type) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ActionSourceTypeInvalid", map[string]any{"actionSourceType": actionType.ActionSource.Type}))
		}
		// strict_mode off: allow empty or draft combinations for McpID, ToolName, BoxID, ToolID (no cross-kind checks).
		if strictMode {
			switch actionType.ActionSource.Type {
			case interfaces.ACTION_SOURCE_TYPE_TOOL:
				// For tool mappings, mcp_id and tool_name must be empty.
				if actionType.ActionSource.McpID != "" || actionType.ActionSource.ToolName != "" {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
						WithErrorDetails(actionTypeInvalidDetail(ctx, "ToolSourceContainsMCP", map[string]any{"mcpID": actionType.ActionSource.McpID, "toolName": actionType.ActionSource.ToolName}))
				}
			case interfaces.ACTION_SOURCE_TYPE_MCP:
				// For map mappings, box_id and tool_id must be empty.
				if actionType.ActionSource.BoxID != "" || actionType.ActionSource.ToolID != "" {
					return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
						WithErrorDetails(actionTypeInvalidDetail(ctx, "MCPSourceContainsTool", map[string]any{"boxID": actionType.ActionSource.BoxID, "toolID": actionType.ActionSource.ToolID}))
				}
			}
		}
	}

	// Parameter names are required when parameters are provided.
	if len(actionType.Parameters) > 0 {
		for _, param := range actionType.Parameters {
			if param.Name == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(actionTypeInvalidDetail(ctx, "ParameterNameRequired", map[string]any{"actionType": actionType.ATName}))
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(actionTypeInvalidDetail(ctx, "ActionIntentMismatch", map[string]any{"actionIntent": ai, "actionType": at}))
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(actionTypeInvalidDetail(ctx, "AffectExpectedOperationInvalid", map[string]any{"expectedOperation": op}))
	}
	return nil
}

func validateImpactContracts(ctx context.Context, items []interfaces.ImpactContractItem) error {
	for i := range items {
		if strings.TrimSpace(items[i].ObjectTypeID) == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ImpactContractObjectTypeRequired", map[string]any{"index": i}))
		}
		op := strings.TrimSpace(string(items[i].ExpectedOperation))
		if op == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ImpactContractExpectedOperationRequired", map[string]any{"index": i}))
		}
		if !interfaces.IsValidExpectedOperation(op) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ImpactContractExpectedOperationInvalid", map[string]any{"index": i, "expectedOperation": op}))
		}
		for j := range items[i].AffectedFields {
			if strings.TrimSpace(items[i].AffectedFields[j]) == "" {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(actionTypeInvalidDetail(ctx, "ImpactContractAffectedFieldRequired", map[string]any{"index": i, "fieldIndex": j}))
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(actionTypeInvalidDetail(ctx, "AffectAndImpactContractsConflict", nil))
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
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionOperationRequired", nil))
	}

	_, exists := interfaces.ActionCondOperationMap[cfg.Operation]
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
			WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionOperationUnsupported", map[string]any{"operation": cfg.Operation}))
	}

	switch cfg.Operation {
	case cond.OperationAnd, cond.OperationOr:
		// A filter may contain at most 100 subconditions.
		if len(cfg.SubConds) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionSubconditionsRequired", map[string]any{"path": path, "operation": cfg.Operation}))
		}

		if len(cfg.SubConds) > cond.MaxSubCondition {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_CountExceeded_Conditions).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionSubconditionsExceeded", map[string]any{"limit": cond.MaxSubCondition}))
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
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionFieldRequired", nil))

		}
	}

	switch cfg.Operation {
	case cond.OperationEq, cond.OperationNotEq, cond.OperationGt, cond.OperationGte, cond.OperationLt, cond.OperationLte:
		// The right-hand side contains a single value.
		_, ok := cfg.Value.([]any)
		if ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionSingleValueRequired", map[string]any{"operation": cfg.Operation}))
		}

	case cond.OperationIn, cond.OperationNotIn:
		// For in and not_in, value is a non-empty array of primitive values.
		_, ok := cfg.Value.([]any)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionArrayRequired", map[string]any{"operations": "in, not_in"}))
		}

		if len(cfg.Value.([]any)) <= 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionArrayNonEmpty", map[string]any{"operations": "in, not_in"}))
		}
	case cond.OperationRange, cond.OperationOutRange, cond.OperationBefore, cond.OperationBetween:
		// For range, value is a two-element numeric array containing inclusive bounds.
		// For out_range, value is a two-element numeric array defining (-inf, value[0]) or [value[1], +inf).
		v, ok := cfg.Value.([]any)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionRangeArrayRequired", nil))
		}

		if len(v) != 2 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionRangeArrayLength", nil))
		}
	case cond.OperationExist, cond.OperationNotExist, cond.OperationNull, cond.OperationNotNull:
		// exist, not_exist, null, and not_null do not require a value.
		// These operators are already covered by NotRequiredValueOperationMap.

	case cond.OperationLike, cond.OperationNotLike, cond.OperationPrefix, cond.OperationNotPrefix, cond.OperationRegex:
		// like, not_like, prefix, not_prefix, and regex require a single string value.
		_, ok := cfg.Value.([]any)
		if ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionSingleStringValueRequired", map[string]any{"operation": cfg.Operation}))
		}
		_, ok = cfg.Value.(string)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionStringValueRequired", map[string]any{"operation": cfg.Operation}))
		}

	case cond.OperationContain, cond.OperationNotContain:
		// contain and not_contain accept one value or a non-empty array.
		if cfg.Value == nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionValueRequired", map[string]any{"operation": cfg.Operation}))
		}
		if arr, ok := cfg.Value.([]any); ok {
			if len(arr) <= 0 {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
					WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionValueArrayNonEmpty", map[string]any{"operation": cfg.Operation}))
			}
		}

	case cond.OperationCurrent:
		// current requires a string unit, not an array.
		_, ok := cfg.Value.([]any)
		if ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionCurrentValueNotArray", nil))
		}
		unit, ok := cfg.Value.(string)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionCurrentValueStringRequired", nil))
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
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_ActionType_InvalidParameter).
				WithErrorDetails(actionTypeInvalidDetail(ctx, "ConditionCurrentUnitInvalid", map[string]any{"unit": unit}))
		}
	}

	return nil
}
