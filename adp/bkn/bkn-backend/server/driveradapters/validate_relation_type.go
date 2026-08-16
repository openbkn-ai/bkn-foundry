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

	"github.com/mitchellh/mapstructure"
	libCommon "github.com/openbkn-ai/bkn-foundry/comm-go/common"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	berrors "bkn-backend/errors"
	"bkn-backend/interfaces"
)

func ValidateRelationTypes(ctx context.Context, knID string, relationTypes []*interfaces.RelationType, strictMode bool) error {
	idMap := make(map[string]any)
	for i := 0; i < len(relationTypes); i++ {
		// Validate that imported models are relation types.
		if relationTypes[i].ModuleType != "" && relationTypes[i].ModuleType != interfaces.MODULE_TYPE_RELATION_TYPE {
			return rest.NewHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_InvalidParameter_ModuleType).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "ModuleType", nil))
		}

		// Reject duplicate model IDs in the request body.
		rtID := relationTypes[i].RTID
		if _, ok := idMap[rtID]; !ok || rtID == "" {
			idMap[rtID] = nil
		} else {
			errDetails := relationTypeInvalidDetail(ctx, "DuplicatedIDInFile", map[string]any{"id": rtID})
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_Duplicated_IDInFile).
				WithDescription(map[string]any{"relationTypeID": rtID}).
				WithErrorDetails(errDetails)
		}

		// Validate required relation-type fields, including presence and enum values.
		err := ValidateRelationType(ctx, relationTypes[i], strictMode)
		if err != nil {
			return err
		}

		// Default an empty branch to main.
		if relationTypes[i].Branch == "" {
			relationTypes[i].Branch = interfaces.MAIN_BRANCH
		}

		relationTypes[i].KNID = knID
	}
	return nil
}

// ValidateRelationType validates the required relation-type creation fields.
func ValidateRelationType(ctx context.Context, relationType *interfaces.RelationType, strictMode bool) error {
	// Validate the ID.
	err := validateID(ctx, relationType.RTID)
	if err != nil {
		return err
	}

	// Normalize and validate the name.
	relationType.RTName = strings.TrimSpace(relationType.RTName)
	err = validateObjectName(ctx, relationType.RTName, interfaces.MODULE_TYPE_RELATION_TYPE)
	if err != nil {
		return err
	}

	// Validate tags when provided.
	err = ValidateTags(ctx, relationType.Tags)
	if err != nil {
		return err
	}

	// Trim tags and remove duplicates.
	relationType.Tags = libCommon.TagSliceTransform(relationType.Tags)

	// Validate the type field.
	if relationType.Type != "" {
		if relationType.Type != interfaces.RELATION_TYPE_DIRECT &&
			relationType.Type != interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "TypeNotSupported", map[string]any{
					"directType":    interfaces.RELATION_TYPE_DIRECT,
					"crossJoinType": interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN,
					"type":          relationType.Type,
				}))
		}
	}

	if relationType.SourceObjectTypeID == "" {
		if strictMode {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "SourceObjectTypeIDRequired", nil))
		}
	}
	if relationType.TargetObjectTypeID == "" {
		if strictMode {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "TargetObjectTypeIDRequired", nil))
		}
	}

	// Validate the mapping_rules field.
	if relationType.MappingRules == nil {
		if strictMode {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "MappingRulesRequired", nil))
		}
		return nil
	}

	// A non-empty mapping_rules value requires type.
	if relationType.Type == "" {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(relationTypeInvalidDetail(ctx, "TypeRequired", nil))
	}

	rules, err := validateMappingRules(ctx, relationType.Type, relationType.MappingRules, strictMode)
	if err != nil {
		return err
	}
	relationType.MappingRules = rules

	return nil
}

// validateMappingRules validates mapping_rules according to the relation type.
func validateMappingRules(ctx context.Context, relationType string, mappingRules any, strictMode bool) (any, error) {
	switch relationType {
	case interfaces.RELATION_TYPE_DIRECT:
		return validateDirectMappingRules(ctx, mappingRules, strictMode)
	case interfaces.RELATION_TYPE_FILTERED_CROSS_JOIN:
		return validateFilteredCrossJoinMappingRules(ctx, mappingRules, strictMode)
	default:
		// Reject unsupported relation types.
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(relationTypeInvalidDetail(ctx, "UnsupportedType", map[string]any{"type": relationType}))
	}
}

// validateDirectMappingRules validates direct-relation mapping_rules.
func validateDirectMappingRules(ctx context.Context, mappingRules any, strictMode bool) ([]interfaces.Mapping, error) {
	// Decode the input into []interfaces.Mapping.
	var mappings []interfaces.Mapping
	if err := mapstructure.Decode(mappingRules, &mappings); err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(relationTypeInvalidDetail(ctx, "DirectMappingRulesDecodeFailed", map[string]any{"error": err.Error()}))
	}

	// Validate that the list is not empty.
	if len(mappings) == 0 {
		if strictMode {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "DirectMappingRulesRequired", nil))
		}
	}

	// Each source-target mapping pair must be unique.
	mappingsRuleMap := map[string]bool{}
	for idx, item := range mappings {
		// Validate the source property.
		if item.SourceProp.Name == "" {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "DirectSourcePropertyRequired", map[string]any{"index": idx}))
		}

		// Validate the target property.
		if item.TargetProp.Name == "" {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "DirectTargetPropertyRequired", map[string]any{"index": idx}))
		}

		// Reject duplicate mapping pairs.
		if mappingsRuleMap[item.SourceProp.Name+":"+item.TargetProp.Name] {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "DirectMappingRuleDuplicated", map[string]any{"index": idx}))
		}
		mappingsRuleMap[item.SourceProp.Name+":"+item.TargetProp.Name] = true
	}

	return mappings, nil
}

// validateInDirectMappingRules validates indirect-relation mapping_rules.
func validateInDirectMappingRules(ctx context.Context, mappingRules any, strictMode bool) (*interfaces.InDirectMapping, error) {
	// Decode the input into an indirect mapping.
	var mapping interfaces.InDirectMapping
	if err := mapstructure.Decode(mappingRules, &mapping); err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(relationTypeInvalidDetail(ctx, "IndirectMappingRulesInvalid", nil))
	}

	// Validate indirect relation backing data source. Only vega resource is supported.
	if mapping.BackingDataSource == nil {
		if strictMode {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "BackingDataSourceRequired", nil))
		}
	} else {
		if mapping.BackingDataSource.Type == "" {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "BackingDataSourceTypeRequired", nil))
		}
		if mapping.BackingDataSource.Type != interfaces.DATA_SOURCE_TYPE_RESOURCE {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "BackingDataSourceTypeInvalid", map[string]any{
					"expected": interfaces.DATA_SOURCE_TYPE_RESOURCE,
					"actual":   mapping.BackingDataSource.Type,
				}))
		}
		// Validate that the referenced data-view ID is present.
		if mapping.BackingDataSource.ID == "" {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "BackingDataSourceIDRequired", nil))
		}
	}

	// Validate source-to-dataset mapping rules.
	if len(mapping.SourceMappingRules) == 0 {
		if strictMode {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "SourceMappingRulesRequired", nil))
		}
	} else {
		// Each source-to-bridge mapping pair must be unique.
		sourceMappingsRuleMap := map[string]bool{}
		for idx, item := range mapping.SourceMappingRules {
			// Validate the source object-type property.
			if item.SourceProp.Name == "" {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
					WithErrorDetails(relationTypeInvalidDetail(ctx, "SourceMappingSourcePropertyRequired", map[string]any{"index": idx}))
			}
			// Validate the bridge property.
			if item.TargetProp.Name == "" {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
					WithErrorDetails(relationTypeInvalidDetail(ctx, "SourceMappingBridgePropertyRequired", map[string]any{"index": idx}))
			}

			// Reject duplicate mapping pairs.
			if sourceMappingsRuleMap[item.SourceProp.Name+":"+item.TargetProp.Name] {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
					WithErrorDetails(relationTypeInvalidDetail(ctx, "SourceMappingRuleDuplicated", map[string]any{"index": idx}))
			}
			sourceMappingsRuleMap[item.SourceProp.Name+":"+item.TargetProp.Name] = true
		}
	}

	// Validate dataset-to-target mapping rules.
	if len(mapping.TargetMappingRules) == 0 {
		if strictMode {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
				WithErrorDetails(relationTypeInvalidDetail(ctx, "TargetMappingRulesRequired", nil))
		}
	} else {
		// Each bridge-to-target mapping pair must be unique.
		targetMappingsRuleMap := map[string]bool{}
		for idx, item := range mapping.TargetMappingRules {
			// Validate the bridge property.
			if item.SourceProp.Name == "" {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
					WithErrorDetails(relationTypeInvalidDetail(ctx, "TargetMappingBridgePropertyRequired", map[string]any{"index": idx}))
			}
			// Validate the target object-type property.
			if item.TargetProp.Name == "" {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
					WithErrorDetails(relationTypeInvalidDetail(ctx, "TargetMappingTargetPropertyRequired", map[string]any{"index": idx}))
			}

			// Reject duplicate mapping pairs.
			if targetMappingsRuleMap[item.SourceProp.Name+":"+item.TargetProp.Name] {
				return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
					WithErrorDetails(relationTypeInvalidDetail(ctx, "TargetMappingRuleDuplicated", map[string]any{"index": idx}))
			}
			targetMappingsRuleMap[item.SourceProp.Name+":"+item.TargetProp.Name] = true
		}
	}

	return &mapping, nil
}

func validateFilteredCrossJoinMappingRules(ctx context.Context, mappingRules any, _ bool) (*interfaces.FilteredCrossJoinMapping, error) {
	var mapping interfaces.FilteredCrossJoinMapping
	if err := mapstructure.Decode(mappingRules, &mapping); err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_RelationType_InvalidParameter).
			WithErrorDetails(relationTypeInvalidDetail(ctx, "FilteredCrossJoinMappingRulesDecodeFailed", map[string]any{"error": err.Error()}))
	}
	// source_condition and target_condition are optional; nil means no extra filter for that side.
	return &mapping, nil
}

func relationTypeInvalidDetail(ctx context.Context, name string, templateData map[string]any) string {
	return i18n.Translate(rest.GetLanguageByCtx(ctx), "BknBackend.RelationType.InvalidParameter.Detail."+name, templateData)
}
