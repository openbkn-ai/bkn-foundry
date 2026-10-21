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
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common/condition"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
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

	interfaces.MODULE_TYPE_VALUE_TYPE: {
		berrors.BknBackend_ValueType_NullParameter_Name,
		berrors.BknBackend_ValueType_LengthExceeded_Name,
	},
}

func validateImportMode(ctx context.Context, mode string) *httperrors.HTTPError {
	switch mode {
	case interfaces.ImportMode_Normal,
		interfaces.ImportMode_Ignore,
		interfaces.ImportMode_Overwrite:
	default:
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ImportMode", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest,
			berrors.BknBackend_InvalidParameter_ImportMode).
			WithErrorDetails(localizedText)
	}

	return nil
}

// validateObjectName validates an object name.
func validateObjectName(ctx context.Context, objectName string, objectType string) error {
	if objectName == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, objectNameErrorCode[objectType][0])
	}

	if utf8.RuneCountInString(objectName) > interfaces.RESOURCE_NAME_MAX_LENGTH {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.NameLengthExceeded", map[string]any{"objectType": objectType, "objectName": objectName, "limit": interfaces.RESOURCE_NAME_MAX_LENGTH})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, objectNameErrorCode[objectType][1]).WithErrorDetails(localizedText)
	}

	return nil
}

// ValidateTags validates tag values.
func ValidateTags(ctx context.Context, Tags []string) error {
	if len(Tags) > interfaces.TAGS_MAX_NUMBER {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_CountExceeded_TagTotal)
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
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_DataTagName)
		// .WithErrorDetails("Data tag name is null")
	}

	if utf8.RuneCountInString(dataTagName) > interfaces.OBJECT_NAME_MAX_LENGTH {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.DataTagNameLength", map[string]any{"limit": interfaces.OBJECT_NAME_MAX_LENGTH})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_DataTagName).
			WithErrorDetails(localizedText)
	}

	if isInvalid := strings.ContainsAny(interfaces.NAME_INVALID_CHARACTER, dataTagName); isInvalid {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.DataTagNameInvalidCharacters", map[string]any{"characters": interfaces.NAME_INVALID_CHARACTER})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_DataTagName).
			WithErrorDetails(localizedText)
	}

	return nil
}

// validatePaginationQueryParameters validates pagination query parameters.
func validatePaginationQueryParameters(ctx context.Context, offset, limit, sort, direction string,
	supportedSortTypes map[string]string) (interfaces.PaginationQueryParameters, error) {
	pageParams := interfaces.PaginationQueryParameters{}

	off, err := strconv.Atoi(offset)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.OffsetIntegerRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return pageParams, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Offset).
			WithErrorDetails(localizedText)
	}

	if off < interfaces.MIN_OFFSET {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.OffsetMin", map[string]any{"min": interfaces.MIN_OFFSET})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return pageParams, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Offset).
			WithErrorDetails(localizedText)
	}

	lim, err := strconv.Atoi(limit)
	if err != nil {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.LimitIntegerRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return pageParams, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Limit).
			WithErrorDetails(localizedText)
	}

	if limit != interfaces.NO_LIMIT && (lim < interfaces.MIN_LIMIT || lim > interfaces.MAX_LIMIT) {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.LimitRange", map[string]any{"noLimit": interfaces.NO_LIMIT, "min": interfaces.MIN_LIMIT, "max": interfaces.MAX_LIMIT})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return pageParams, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Limit).WithErrorDetails(localizedText)
	}

	_, ok := supportedSortTypes[sort]
	if !ok {
		types := make([]string, 0)
		for t := range supportedSortTypes {
			types = append(types, t)
		}
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.SortInvalid", map[string]any{"types": types})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return pageParams, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Sort).
			WithErrorDetails(localizedText)
	}

	if direction != interfaces.DESC_DIRECTION && direction != interfaces.ASC_DIRECTION {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.DirectionInvalid", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return pageParams, httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Direction).
			WithErrorDetails(localizedText)
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
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionDecodeFailed", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_Condition).
			WithErrorDetails(localizedText)
	}
	query.ActualCondition = actualCond

	knFilter := &cond.CondCfg{
		Field:     "kn_id",
		Operation: cond.OperationEq,
		ValueFrom: cond.ValueFrom_Const,
		Value:     query.KNID,
	}

	// Add the module type filter.
	typeFilter := &cond.CondCfg{
		Field:     "module_type",
		Operation: cond.OperationEq,
		ValueFrom: cond.ValueFrom_Const,
		Value:     query.ModuleType,
	}

	// Add the branch filter.
	branchFilter := &cond.CondCfg{
		Field:     "branch",
		Operation: cond.OperationEq,
		ValueFrom: cond.ValueFrom_Const,
		Value:     query.Branch,
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
	if query.ExcludeDeprecated {
		excludeDeprecated(query.ActualCondition)
	}

	return nil
}

// notDeprecatedFilter keeps the concepts without lifecycle_status deprecated; only a deprecated
// concept carries the field, so those without a lifecycle are kept.
func notDeprecatedFilter() *cond.CondCfg {
	return &cond.CondCfg{
		Field:     interfaces.CONCEPT_FIELD_LIFECYCLE_STATUS,
		Operation: cond.OperationNotEq,
		ValueOptCfg: cond.ValueOptCfg{
			ValueFrom: cond.ValueFrom_Const,
			Value:     interfaces.LIFECYCLE_DEPRECATED,
		},
	}
}

// excludeDeprecated adds the filter around the whole condition, and inside each knn as its own
// filter so deprecated concepts take none of its k neighbours.
func excludeDeprecated(root *cond.CondCfg) {
	var addToKnn func(cfg *cond.CondCfg)
	addToKnn = func(cfg *cond.CondCfg) {
		if cfg == nil {
			return
		}
		if cfg.Operation == cond.OperationKNN {
			cfg.SubConds = append(cfg.SubConds, notDeprecatedFilter())
			return
		}
		for _, sub := range cfg.SubConds {
			addToKnn(sub)
		}
	}
	addToKnn(root)
	root.SubConds = append(root.SubConds, notDeprecatedFilter())
}

func validateCond(ctx context.Context, cfg *cond.CondCfg) error {
	if cfg == nil {
		return nil
	}

	// Validate the filter operation.
	if cfg.Operation == "" {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_NullParameter_ConditionOperation)
	}

	_, exists := cond.OperationMap[cfg.Operation]
	if !exists {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_UnsupportConditionOperation)
	}

	switch cfg.Operation {
	case cond.OperationAnd, cond.OperationOr, cond.OperationKNN:
		// Limit the number of sub-conditions.
		if len(cfg.SubConds) > cond.MaxSubCondition {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionCountExceeded", map[string]any{"limit": cond.MaxSubCondition})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_CountExceeded_Conditions).
				WithErrorDetails(localizedText)
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
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_NullParameter_ConditionName)
		}

		// if _, ok := cond.NotRequiredValueOperationMap[cfg.Operation]; !ok {
		// 	if cfg.ValueFrom != cond.ValueFrom_Const {
		// 		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.Uniquery_InvalidParameter_ValueFrom).
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
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionSingleValueRequired", map[string]any{"operation": cfg.Operation})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(localizedText)
		}

		if cfg.Operation == cond.OperationLike || cfg.Operation == cond.OperationNotLike ||
			cfg.Operation == cond.OperationPrefix || cfg.Operation == cond.OperationNotPrefix {
			_, ok := cfg.Value.(string)
			if !ok {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionStringValueRequired", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(localizedText)
			}
		}

		if cfg.Operation == cond.OperationRegex {
			val, ok := cfg.Value.(string)
			if !ok {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionRegexStringRequired", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(localizedText)
			}

			_, err := regexp2.Compile(val, regexp2.RE2)
			if err != nil {
				localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionRegexInvalid", nil)
				if renderErr != nil {
					logger.Errorf("localized message rendering failed: %v", renderErr)
				}
				return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
					WithErrorDetails(localizedText)
			}

		}

	case cond.OperationIn, cond.OperationNotIn:
		// The in and not_in operations require a non-empty array value.
		_, ok := cfg.Value.([]any)
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionArrayRequired", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(localizedText)
		}

		if len(cfg.Value.([]any)) <= 0 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionArrayNonEmpty", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(localizedText)
		}
	case cond.OperationRange, cond.OperationOutRange, cond.OperationBefore, cond.OperationBetween:
		// Range-like operations require an array with two values.
		// out_range excludes the interval between the two values.
		v, ok := cfg.Value.([]any)
		if !ok {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionRangeArrayRequired", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(localizedText)
		}

		if len(v) != 2 {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.ConditionRangeArrayLength", nil)
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ConditionValue).
				WithErrorDetails(localizedText)
		}
	}

	return nil
}

func validateID(ctx context.Context, id string) error {
	if id != "" {
		// IDs may contain lowercase letters, digits, underscores, and hyphens; they cannot start with an underscore or exceed the resource ID limit.
		re := regexp2.MustCompile(interfaces.RegexPattern_NonBuiltin_ID, regexp2.RE2)
		match, err := re.MatchString(id)
		if err != nil || !match {
			localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.IdentifierInvalid", map[string]any{"id": id})
			if renderErr != nil {
				logger.Errorf("localized message rendering failed: %v", renderErr)
			}
			return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_ID).
				WithErrorDetails(localizedText)
		}
	}

	return nil
}

// ValidateHeaderMethodOverride validates the x-http-method-override request header.
func ValidateHeaderMethodOverride(ctx context.Context, methodOverride string) error {

	if methodOverride == "" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.OverrideMethodRequired", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_OverrideMethod).
			WithErrorDetails(localizedText)
	}

	if methodOverride != "GET" {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "BknBackend.Validation.Detail.OverrideMethodInvalid", map[string]any{"method": methodOverride})
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_InvalidParameter_OverrideMethod).
			WithErrorDetails(localizedText)
	}
	return nil
}
