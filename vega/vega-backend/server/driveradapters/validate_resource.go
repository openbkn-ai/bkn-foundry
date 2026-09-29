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
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

func ValidateResourceRequest(ctx context.Context, req *interfaces.ResourceRequest) error {
	if err := validateResourceRequestBase(ctx, req); err != nil {
		return err
	}
	return validateResourceRequestSchema(ctx, req)
}

func validateResourceRequestBase(ctx context.Context, req *interfaces.ResourceRequest) error {
	if err := validateID(ctx, req.ID); err != nil {
		return err
	}
	if err := validateName(ctx, req.Name); err != nil {
		return err
	}
	if err := ValidateTags(ctx, req.Tags); err != nil {
		return err
	}
	if err := validateDescription(ctx, req.Description); err != nil {
		return err
	}
	return nil
}

func validateResourceRequestSchema(ctx context.Context, req *interfaces.ResourceRequest) error {
	switch req.Category {
	case interfaces.ResourceCategoryLogicView:
		return resourcelogic.ValidateLogicViewRequest(ctx, req)
	case interfaces.ResourceCategoryDataset:
		if req.IndexConfig != nil {
			if len(req.IndexConfig.PrimaryKeyFields) > 0 {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_PrimaryKeyFields).
					WithErrorDetails("primary_key_fields is not supported for dataset resources")
			}
			if len(req.IndexConfig.IncrementalFields) > 0 {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter_IncrementalFields).
					WithErrorDetails("incremental_fields is not supported for dataset resources")
			}
		}
		if len(req.SchemaDefinition) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_SchemaDefinition).
				WithErrorDetails("schema_definition is required and must contain at least one field")
		}
		if err := validateSchemaProperties(ctx, req.SchemaDefinition, false); err != nil {
			return err
		}
		return validateDatasetSchemaTypes(ctx, req.SchemaDefinition)
	default:
		// Only raw resources support ref_property, and only their legacy data can contain
		// self-references. Normalize this branch alone: dataset ref_property already returned
		// 400 before #837, so relaxing it would be unrelated behavior change. The logics layer
		// performs the same normalization on stored schemas; both sides must match or an update
		// is treated as a build-related change and clears LocalIndexName.
		resourcelogic.NormalizeSelfReferencingFeatures(req.SchemaDefinition)
		return validateSchemaProperties(ctx, req.SchemaDefinition, true)
	}
}

func validateDatasetSchemaTypes(ctx context.Context, props []*interfaces.Property) error {
	for _, prop := range props {
		if prop.Type == interfaces.DataType_Binary || prop.Type == interfaces.DataType_Other {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldType).
				WithErrorDetails(fmt.Sprintf("Dataset field %q type %q is not supported", prop.Name, prop.Type))
		}
	}
	return nil
}

// validateSchemaProperties verifies the name, type and Feature of the schema field.
func validateSchemaProperties(ctx context.Context, props []*interfaces.Property, allowFeatureRefProperty bool) error {
	propsMap := make(map[string]*interfaces.Property, len(props))
	for _, prop := range props {
		if prop == nil {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldName).
				WithErrorDetails("The field is null")
		}
		propsMap[prop.Name] = prop
	}

	nameMap := make(map[string]struct{})
	displayNameMap := make(map[string]struct{})
	for _, prop := range props {
		if prop.Name == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldName).
				WithErrorDetails("The field name is null")
		}
		if utf8.RuneCountInString(prop.Name) > interfaces.MaxLength_PropertyName {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldName).
				WithErrorDetails(fmt.Sprintf("The length of the field name %s exceeds %d", prop.Name, interfaces.MaxLength_PropertyName))
		}
		if prop.DisplayName == "" {
			prop.DisplayName = prop.Name
		}
		if utf8.RuneCountInString(prop.DisplayName) > interfaces.MaxLength_PropertyDisplayName {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldDisplayName).
				WithErrorDetails(fmt.Sprintf("The length of the field display name %s exceeds %d", prop.DisplayName, interfaces.MaxLength_PropertyDisplayName))
		}
		if utf8.RuneCountInString(prop.Description) > interfaces.MaxLength_PropertyDescription {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldComment).
				WithErrorDetails(fmt.Sprintf("The length of the field comment %s exceeds %d", prop.Description, interfaces.MaxLength_PropertyDescription))
		}
		if _, dup := nameMap[prop.Name]; dup {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Duplicated_FieldName).
				WithDescription(map[string]any{"FieldName": prop.Name}).
				WithErrorDetails(fmt.Sprintf("Dataset field '%s' already exists", prop.Name))
		}
		nameMap[prop.Name] = struct{}{}

		if _, dup := displayNameMap[prop.DisplayName]; dup {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Duplicated_FieldDisplayName).
				WithDescription(map[string]any{"FieldName": prop.Name, "DisplayName": prop.DisplayName}).
				WithErrorDetails(fmt.Sprintf("Dataset field '%s' display name '%s' already exists", prop.Name, prop.DisplayName))
		}
		displayNameMap[prop.DisplayName] = struct{}{}

		if !isValidDataType(prop.Type) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldType).
				WithErrorDetails(fmt.Sprintf("Dataset field '%s' type '%s' is invalid", prop.Name, prop.Type))
		}

		if err := validatePropertyFeatures(ctx, prop, propsMap, allowFeatureRefProperty); err != nil {
			return err
		}

	}
	return nil
}

func validatePropertyFeatures(ctx context.Context, prop *interfaces.Property, propsMap map[string]*interfaces.Property, allowRefProperty bool) error {
	featureNameMap := make(map[string]struct{})
	featureTypeMap := make(map[string]struct{})
	for i := range prop.Features {
		f := &prop.Features[i]
		if f.FeatureName == "" {
			switch f.FeatureType {
			case interfaces.PropertyFeatureType_Keyword:
				f.FeatureName = interfaces.LocalIndexKeywordSubfieldName
			case interfaces.PropertyFeatureType_Fulltext:
				f.FeatureName = interfaces.LocalIndexFulltextSubfieldName
			case interfaces.PropertyFeatureType_Vector:
				if f.RefProperty != "" {
					break
				}
				fallthrough
			default:
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureName).
					WithErrorDetails("The field feature name is null")
			}
		}
		if strings.HasPrefix(f.FeatureName, prop.Name+".") {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureName).
				WithErrorDetails(fmt.Sprintf("feature name %q must be relative to property %q", f.FeatureName, prop.Name))
		}
		if utf8.RuneCountInString(f.FeatureName) > interfaces.MaxLength_PropertyFeatureName {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldFeatureName).
				WithErrorDetails(fmt.Sprintf("The length of the field feature name %s exceeds %d", f.FeatureName, interfaces.MaxLength_PropertyFeatureName))
		}
		if _, dup := featureNameMap[f.FeatureName]; dup {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Duplicated_FieldFeatureName).
				WithDescription(map[string]any{"FieldFeatureName": f.FeatureName}).
				WithErrorDetails(fmt.Sprintf("Dataset field feature '%s' already exists", f.FeatureName))
		}
		featureNameMap[f.FeatureName] = struct{}{}

		if _, ok := interfaces.PropertyFeatureTypeMap[f.FeatureType]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureType).
				WithErrorDetails(fmt.Sprintf("The field feature type '%s' is invalid", f.FeatureType))
		}

		if utf8.RuneCountInString(f.Description) > interfaces.MaxLength_PropertyFeatureDescription {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldFeatureComment).
				WithErrorDetails(fmt.Sprintf("The length of the field feature comment %s exceeds %d", f.Description, interfaces.MaxLength_PropertyFeatureDescription))
		}

		if !resourcelogic.IsFeatureSupported(prop.Type, f.FeatureType) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Unsupported_FieldFeatureRefType).
				WithErrorDetails(fmt.Sprintf("The field '%s' type '%s' does not support feature type '%s'", prop.Name, prop.Type, f.FeatureType))
		}
		if f.RefProperty != "" {
			if !allowRefProperty {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureRef).
					WithErrorDetails("ref_property is only supported by original resources")
			}
			refProp, exists := propsMap[f.RefProperty]
			if !exists {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureRef).
					WithErrorDetails(fmt.Sprintf("The field feature ref_property '%s' is not in the field list", f.RefProperty))
			}
			if !resourcelogic.IsFeatureRefPropertyTypeSupported(refProp.Type, f.FeatureType) {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Unsupported_FieldFeatureRefType).
					WithErrorDetails(fmt.Sprintf("The field feature ref_property '%s' type '%s' does not match feature type '%s'", f.RefProperty, refProp.Type, f.FeatureType))
			}
		}

		if _, duplicate := featureTypeMap[f.FeatureType]; duplicate {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_Duplicated_FieldFeatureType).
				WithDescription(map[string]any{"FieldName": prop.Name, "FieldFeatureType": f.FeatureType}).
				WithErrorDetails(fmt.Sprintf("property %q has more than one %q feature", prop.Name, f.FeatureType))
		}
		featureTypeMap[f.FeatureType] = struct{}{}
	}
	return nil
}

func isValidDataType(t string) bool {
	switch t {
	case interfaces.DataType_Integer, interfaces.DataType_UnsignedInteger,
		interfaces.DataType_Float, interfaces.DataType_Decimal,
		interfaces.DataType_String, interfaces.DataType_Text,
		interfaces.DataType_Date, interfaces.DataType_Time,
		interfaces.DataType_Datetime, interfaces.DataType_Timestamp,
		interfaces.DataType_Ip, interfaces.DataType_Boolean,
		interfaces.DataType_Binary, interfaces.DataType_Json,
		interfaces.DataType_Point, interfaces.DataType_Shape,
		interfaces.DataType_Vector, interfaces.DataType_Other:
		return true
	default:
		return false
	}
}

func ValidateResourceListQueryParams(ctx context.Context, params interfaces.ResourcesQueryParams) error {
	if err := validateResourceCategoryQueryParam(ctx, params.Category); err != nil {
		return err
	}
	if err := validateResourceStatusQueryParam(ctx, params.Status); err != nil {
		return err
	}
	return validateResourceDiscoverStatusQueryParam(ctx, params.LastDiscoverStatus)
}

// validateCreateResourceCategory enforces the business boundary that only
// 'dataset' and 'logicview' resources can be created via the REST API.
// Other categories must be produced by a discover task.
func validateCreateResourceCategory(ctx context.Context, category string) error {
	switch category {
	case interfaces.ResourceCategoryDataset, interfaces.ResourceCategoryLogicView:
		return nil
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_CategoryNotCreatable).
			WithErrorDetails(fmt.Sprintf("category %q cannot be created via API; only 'dataset' and 'logicview' are allowed, other categories must be created via discover task", category))
	}
}

func validateResourceCategoryQueryParam(ctx context.Context, category string) error {
	if category == "" {
		return nil
	}

	switch category {
	case interfaces.ResourceCategoryTable,
		interfaces.ResourceCategoryFile,
		interfaces.ResourceCategoryFileset,
		interfaces.ResourceCategoryAPI,
		interfaces.ResourceCategoryMetric,
		interfaces.ResourceCategoryTopic,
		interfaces.ResourceCategoryIndex,
		interfaces.ResourceCategoryLogicView,
		interfaces.ResourceCategoryDataset:
		return nil
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("invalid category: %s", category))
	}
}

func validateResourceStatusQueryParam(ctx context.Context, status string) error {
	if status == "" {
		return nil
	}

	switch status {
	case interfaces.ResourceStatusActive,
		interfaces.ResourceStatusDeprecated,
		interfaces.ResourceStatusStale:
		return nil
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("invalid status: %s", status))
	}
}

func validateResourceDiscoverStatusQueryParam(ctx context.Context, status string) error {
	if status == "" {
		return nil
	}

	switch status {
	case interfaces.DiscoverStatusError,
		interfaces.DiscoverStatusMissing,
		interfaces.DiscoverStatusNew,
		interfaces.DiscoverStatusRestored,
		interfaces.DiscoverStatusUnchanged,
		interfaces.DiscoverStatusUpdated:
		return nil
	default:
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Resource_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("invalid last_discover_status: %s", status))
	}
}
