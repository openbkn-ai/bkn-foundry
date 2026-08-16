// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	cond "bkn-backend/common/condition"
	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

// objectNameErrorCode maps object types to their required-name and overlength error codes.
var objectNameErrorCode = map[string][]string{

	interfaces.MODULE_TYPE_KN: {
		berrors.BknBackend_KnowledgeNetwork_NullParameter_Name,
		berrors.BknBackend_KnowledgeNetwork_LengthExceeded_Name,
	},

	interfaces.MODULE_TYPE_OBJECT_TYPE: {
		berrors.BknBackend_ObjectType_NullParameter_Name,
		berrors.BknBackend_ObjectType_LengthExceeded_Name,
	},

	interfaces.MODULE_TYPE_RELATION_TYPE: {
		berrors.BknBackend_RelationType_NullParameter_Name,
		berrors.BknBackend_RelationType_LengthExceeded_Name,
	},

	interfaces.MODULE_TYPE_ACTION_TYPE: {
		berrors.BknBackend_ActionType_NullParameter_Name,
		berrors.BknBackend_ActionType_LengthExceeded_Name,
	},

	interfaces.MODULE_TYPE_CONCEPT_GROUP: {
		berrors.BknBackend_ConceptGroup_NullParameter_Name,
		berrors.BknBackend_ConceptGroup_LengthExceeded_Name,
	},

	interfaces.MODULE_TYPE_RISK_TYPE: {
		berrors.BknBackend_RiskType_NullParameter_Name,
		berrors.BknBackend_RiskType_LengthExceeded_Name,
	},

	interfaces.MODULE_TYPE_METRIC: {
		berrors.BknBackend_Metric_NullParameter_Name,
		berrors.BknBackend_Metric_LengthExceeded_Name,
	},
}

func commonValidationDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(
		rest.GetLanguageByCtx(ctx),
		"BknBackend.Validation.Detail."+name,
		templateData,
	)
}

// Validate the import mode.
func validateImportMode(ctx context.Context, mode string) *rest.HTTPError {
	switch mode {
	case interfaces.ImportMode_Normal,
		interfaces.ImportMode_Ignore,
		interfaces.ImportMode_Overwrite:
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_ImportMode).
			WithErrorDetails(commonValidationDetail(ctx, "ImportMode", nil))
	}

	return nil
}

// validateObjectName validates an object name.
func validateObjectName(ctx context.Context, objectName string, objectType string) error {
	if objectName == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, objectNameErrorCode[objectType][0])
	}

	if utf8.RuneCountInString(objectName) > interfaces.OBJECT_NAME_MAX_LENGTH {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, objectNameErrorCode[objectType][1]).
			WithErrorDetails(commonValidationDetail(ctx, "NameLengthExceeded", map[string]any{"objectType": objectType, "objectName": objectName, "limit": interfaces.OBJECT_NAME_MAX_LENGTH}))
	}

	return nil
}

// ValidateTags validates tag values.
func ValidateTags(ctx context.Context, Tags []string) error {
	if len(Tags) > interfaces.TAGS_MAX_NUMBER {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_CountExceeded_TagTotal)
	}

	for _, tag := range Tags {
		err := validateDataTagName(ctx, tag)
		if err != nil {
			return err
		}
	}
	return nil
}

// validateDataTagName validates a data tag name.
func validateDataTagName(ctx context.Context, dataTagName string) error {
	// Trim surrounding whitespace from the data tag name.
	dataTagName = strings.Trim(dataTagName, " ")

	if dataTagName == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_DataTagName)
		// .WithErrorDetails("Data tag name is null")
	}

	if utf8.RuneCountInString(dataTagName) > interfaces.OBJECT_NAME_MAX_LENGTH {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_DataTagName).
			WithErrorDetails(commonValidationDetail(ctx, "DataTagNameLength", map[string]any{"limit": interfaces.OBJECT_NAME_MAX_LENGTH}))
	}

	if isInvalid := strings.ContainsAny(interfaces.NAME_INVALID_CHARACTER, dataTagName); isInvalid {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_DataTagName).
			WithErrorDetails(commonValidationDetail(ctx, "DataTagNameInvalidCharacters", map[string]any{"characters": interfaces.NAME_INVALID_CHARACTER}))
	}

	return nil
}

// validatePaginationQueryParameters validates pagination query parameters.
func validatePaginationQueryParameters(ctx context.Context, offset, limit, sort, direction string,
	supportedSortTypes map[string]string) (interfaces.PaginationQueryParameters, error) {
	pageParams := interfaces.PaginationQueryParameters{}

	off, err := strconv.Atoi(offset)
	if err != nil {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Offset).
			WithErrorDetails(commonValidationDetail(ctx, "OffsetIntegerRequired", nil))
	}

	if off < interfaces.MIN_OFFSET {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Offset).
			WithErrorDetails(commonValidationDetail(ctx, "OffsetMin", map[string]any{"min": interfaces.MIN_OFFSET}))
	}

	lim, err := strconv.Atoi(limit)
	if err != nil {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Limit).
			WithErrorDetails(commonValidationDetail(ctx, "LimitIntegerRequired", nil))
	}

	if limit != interfaces.NO_LIMIT && (lim < interfaces.MIN_LIMIT || lim > interfaces.MAX_LIMIT) {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Limit).
			WithErrorDetails(commonValidationDetail(ctx, "LimitRange", map[string]any{"noLimit": interfaces.NO_LIMIT, "min": interfaces.MIN_LIMIT, "max": interfaces.MAX_LIMIT}))
	}

	_, ok := supportedSortTypes[sort]
	if !ok {
		types := make([]string, 0)
		for t := range supportedSortTypes {
			types = append(types, t)
		}
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Sort).
			WithErrorDetails(commonValidationDetail(ctx, "SortInvalid", map[string]any{"types": types}))
	}

	if direction != interfaces.DESC_DIRECTION && direction != interfaces.ASC_DIRECTION {
		return pageParams, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Direction).
			WithErrorDetails(commonValidationDetail(ctx, "DirectionInvalid", nil))
	}

	return interfaces.PaginationQueryParameters{
		Offset:    off,
		Limit:     lim,
		Sort:      supportedSortTypes[sort],
		Direction: direction,
	}, nil
}

func validateConceptsQuery(ctx context.Context, query *interfaces.ConceptsQuery) error {

	// Decode the filter condition into its typed representation.
	var actualCond *cond.CondCfg
	err := mapstructure.Decode(query.Condition, &actualCond)
	if err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Condition).
			WithErrorDetails(commonValidationDetail(ctx, "ConditionDecodeFailed", nil))
	}
	query.ActualCondition = actualCond

	knFilter := &cond.CondCfg{
		Field:     "kn_id",
		Operation: cond.OperationEq,
		ValueOptCfg: cond.ValueOptCfg{
			ValueFrom: cond.ValueFrom_Const,
			Value:     query.KNID,
		},
	}

	// Add the module type filter.
	typeFilter := &cond.CondCfg{
		Field:     "module_type",
		Operation: cond.OperationEq,
		ValueOptCfg: cond.ValueOptCfg{
			ValueFrom: cond.ValueFrom_Const,
			Value:     query.ModuleType,
		},
	}

	// Add the branch filter.
	branchFilter := &cond.CondCfg{
		Field:     "branch",
		Operation: cond.OperationEq,
		ValueOptCfg: cond.ValueOptCfg{
			ValueFrom: cond.ValueFrom_Const,
			Value:     query.Branch,
		},
	}

	// Include required BKN filters in the condition tree.
	err = validateCond(ctx, query.ActualCondition)
	if err != nil {
		return err
	}

	query.ActualCondition = &cond.CondCfg{
		Operation: cond.OperationAnd,
		SubConds:  []*cond.CondCfg{query.ActualCondition, knFilter, typeFilter, branchFilter},
	}

	return nil
}

func validateCond(ctx context.Context, cfg *cond.CondCfg) error {
	if cfg == nil {
		return nil
	}

	// Validate the filter operation.
	if cfg.Operation == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_NullParameter_ConditionOperation)
	}

	_, exists := cond.OperationMap[cfg.Operation]
	if !exists {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_UnsupportConditionOperation)
	}

	switch cfg.Operation {
	case cond.OperationAnd, cond.OperationOr, cond.OperationKNN:
		// Limit the number of sub-conditions.
		if len(cfg.SubConds) > cond.MaxSubCondition {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_CountExceeded_Conditions).
				WithErrorDetails(commonValidationDetail(ctx, "ConditionCountExceeded", map[string]any{"limit": cond.MaxSubCondition}))
		}

		for _, subCond := range cfg.SubConds {
			err := validateCond(ctx, subCond)
			if err != nil {
				return err
			}
		}
	default:
		// A field is required except for multi-match operations.
		if cfg.Operation != cond.OperationMultiMatch && cfg.Field == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_NullParameter_ConditionName)
		}

		// if _, ok := cond.NotRequiredValueOperationMap[cfg.Operation]; !ok {
		// 	if cfg.ValueFrom != cond.ValueFrom_Const {
		// 		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.Uniquery_InvalidParameter_ValueFrom).
		// 			WithErrorDetails(fmt.Sprintf("condition does not support value_from type('%s')", cfg.ValueFrom))
		// 	}
		// }
	}

	switch cfg.Operation {
	case cond.OperationEq, cond.OperationNotEq, cond.OperationGt, cond.OperationGte, cond.OperationLt, cond.OperationLte,
		cond.OperationLike, cond.OperationNotLike, cond.OperationPrefix, cond.OperationNotPrefix, cond.OperationRegex,
		cond.OperationMatch, cond.OperationMatchPhrase, cond.OperationCurrent, cond.OperationMultiMatch:
		// These operations require a single value.
		_, ok := cfg.Value.([]any)
		if ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(commonValidationDetail(ctx, "ConditionSingleValueRequired", map[string]any{"operation": cfg.Operation}))
		}

		if cfg.Operation == cond.OperationLike || cfg.Operation == cond.OperationNotLike ||
			cfg.Operation == cond.OperationPrefix || cfg.Operation == cond.OperationNotPrefix {
			_, ok := cfg.Value.(string)
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(commonValidationDetail(ctx, "ConditionStringValueRequired", nil))
			}
		}

		if cfg.Operation == cond.OperationRegex {
			val, ok := cfg.Value.(string)
			if !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(commonValidationDetail(ctx, "ConditionRegexStringRequired", nil))
			}

			_, err := regexp2.Compile(val, regexp2.RE2)
			if err != nil {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(commonValidationDetail(ctx, "ConditionRegexInvalid", nil))
			}

		}

	case cond.OperationIn, cond.OperationNotIn:
		// The in and not_in operations require a non-empty array value.
		_, ok := cfg.Value.([]any)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(commonValidationDetail(ctx, "ConditionArrayRequired", nil))
		}

		if len(cfg.Value.([]any)) <= 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(commonValidationDetail(ctx, "ConditionArrayNonEmpty", nil))
		}
	case cond.OperationRange, cond.OperationOutRange, cond.OperationBefore, cond.OperationBetween:
		// Range-like operations require an array with two values.
		// out_range excludes the interval between the two values.
		v, ok := cfg.Value.([]any)
		if !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(commonValidationDetail(ctx, "ConditionRangeArrayRequired", nil))
		}

		if len(v) != 2 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(commonValidationDetail(ctx, "ConditionRangeArrayLength", nil))
		}
	}

	return nil
}

func validateID(ctx context.Context, id string) error {
	if id != "" {
		// IDs may contain lowercase letters, digits, underscores, and hyphens; they cannot start with an underscore or exceed 40 characters.
		re := regexp2.MustCompile(interfaces.RegexPattern_NonBuiltin_ID, regexp2.RE2)
		match, err := re.MatchString(id)
		if err != nil || !match {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ID).
				WithErrorDetails(commonValidationDetail(ctx, "IdentifierInvalid", map[string]any{"id": id}))
		}
	}

	return nil
}

// ValidateHeaderMethodOverride validates the x-http-method-override request header.
func ValidateHeaderMethodOverride(ctx context.Context, methodOverride string) error {

	if methodOverride == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_OverrideMethod).
			WithErrorDetails(commonValidationDetail(ctx, "OverrideMethodRequired", nil))
	}

	if methodOverride != "GET" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_OverrideMethod).
			WithErrorDetails(commonValidationDetail(ctx, "OverrideMethodInvalid", map[string]any{"method": methodOverride}))
	}
	return nil
}
