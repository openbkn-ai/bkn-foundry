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
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	resourcelogic "vega-backend/logics/resource"
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
		return validateLogicViewRequest(ctx, req)
	case interfaces.ResourceCategoryDataset:
		if len(req.SchemaDefinition) == 0 {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_SchemaDefinition).
				WithErrorDetails("schema_definition is required and must contain at least one field")
		}
		return validateSchemaProperties(ctx, req.SchemaDefinition, false)
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
	enabledMap := make(map[string]bool)
	featureNameMap := make(map[string]struct{})
	for _, f := range prop.Features {
		if f.FeatureName == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureName).
				WithErrorDetails("The field feature name is null")
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

		if f.IsDefault {
			if enabledMap[f.FeatureType] {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Duplicated_DefaultFeaturePerType).
					WithErrorDetails(fmt.Sprintf("Same feature type can only have one default; field feature '%s' type '%s'", f.FeatureName, f.FeatureType))
			}
			enabledMap[f.FeatureType] = true
		}
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
	return nil
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

func validateLogicViewRequest(ctx context.Context, req *interfaces.ResourceRequest) error {
	outputFields, err := validateLogicDefinition(ctx, req.LogicDefinition)
	if err != nil {
		return err
	}

	// Verification field
	err = validateViewFields(ctx, outputFields)
	if err != nil {
		return err
	}

	return nil

}

// Verify the logical view definition
func validateLogicDefinition(ctx context.Context, nodes []*interfaces.LogicDefinitionNode) (outputFields []*interfaces.ViewProperty, err error) {
	if nodes == nil {
		return nil, nil
	}

	if len(nodes) > 20 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition nodes cannot be more than 20")
	}

	for _, node := range nodes {
		// Detect nodeType
		if _, ok := interfaces.LogicDefinitionNodeTypeMap[node.Type]; !ok {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
				WithErrorDetails("The logic definition node type is invalid")
		}

		if node.Type == interfaces.LogicDefinitionNodeType_Output {
			outputFields = node.OutputFields
		}
	}

	return outputFields, nil
}

// Verify the fields and field characteristics
func validateViewFields(ctx context.Context, viewFields []*interfaces.ViewProperty) error {
	fieldsMap := make(map[string]*interfaces.ViewProperty)
	for _, field := range viewFields {
		fieldsMap[field.Name] = field
	}

	// Check whether the field names and display names are duplicated
	nameMap := make(map[string]struct{})
	displayNameMap := make(map[string]struct{})
	for _, field := range viewFields {
		if field.Name == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_FieldName).
				WithErrorDetails("The field name is null")
		}

		// Verify the length of the field name. The length limit is 255
		if utf8.RuneCountInString(field.Name) > interfaces.MaxLength_PropertyName {
			errDetails := fmt.Sprintf("The length of the field name %s exceeds %d", field.Name, interfaces.MaxLength_PropertyName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldName).
				WithErrorDetails(errDetails)
		}

		// If display_name is "", set the value of display_name equal to the value of field
		if field.DisplayName == "" {
			field.DisplayName = field.Name
		}

		// The verification field displays the name length, with a length limit of 255
		if utf8.RuneCountInString(field.DisplayName) > interfaces.MaxLength_PropertyDisplayName {
			errDetails := fmt.Sprintf("The length of the field display name %s exceeds %d", field.DisplayName, interfaces.MaxLength_PropertyDisplayName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldDisplayName).
				WithErrorDetails(errDetails)
		}

		// Note the length of the verification field. The length limit is 1000
		if utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription {
			errDetails := fmt.Sprintf("The length of the field comment %s exceeds %d", field.Description, interfaces.MaxLength_PropertyDescription)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldComment).
				WithErrorDetails(errDetails)
		}

		// Check whether the field names are duplicated
		if _, ok := nameMap[field.Name]; !ok {
			nameMap[field.Name] = struct{}{}
		} else {
			errDetails := fmt.Sprintf("Logic view field '%s' name '%s' already exists", field.Name, field.Name)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_Duplicated_FieldName).
				WithDescription(map[string]any{"FieldName": field.Name}).
				WithErrorDetails(errDetails)
		}

		if _, ok := displayNameMap[field.DisplayName]; !ok {
			displayNameMap[field.DisplayName] = struct{}{}
		} else {
			errDetails := fmt.Sprintf("Logic view field '%s' display name '%s' already exists", field.Name, field.DisplayName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_Duplicated_FieldDisplayName).
				WithDescription(map[string]any{"FieldName": field.Name, "DisplayName": field.DisplayName}).
				WithErrorDetails(errDetails)
		}

		// Verification feature
		err := validateFeatures(ctx, fieldsMap, field.Features)
		if err != nil {
			return err
		}

	}

	return nil
}

// Verification feature
func validateFeatures(ctx context.Context, fieldsMap map[string]*interfaces.ViewProperty, features []interfaces.PropertyFeature) error {
	enabledMap := make(map[string]bool)
	featureNameMap := make(map[string]struct{})
	for _, f := range features {
		if f.FeatureName == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_FieldFeatureName).
				WithErrorDetails("The field feature name is null")
		}

		// Verify the length of the feature name, with a length limit of 255
		if utf8.RuneCountInString(f.FeatureName) > interfaces.MaxLength_PropertyFeatureName {
			errDetails := fmt.Sprintf("The length of the field feature name %s exceeds %d", f.FeatureName, interfaces.MaxLength_PropertyFeatureName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldFeatureName).
				WithErrorDetails(errDetails)
		}

		// Verify whether the feature names are duplicated
		if _, ok := featureNameMap[f.FeatureName]; !ok {
			featureNameMap[f.FeatureName] = struct{}{}
		} else {
			errDetails := fmt.Sprintf("Logic view field feature '%s' name '%s' already exists", f.FeatureName, f.FeatureName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_Duplicated_FieldFeatureName).
				WithDescription(map[string]any{"FieldFeatureName": f.FeatureName}).
				WithErrorDetails(errDetails)
		}

		// feature type
		if _, ok := interfaces.PropertyFeatureTypeMap[f.FeatureType]; !ok {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
				WithErrorDetails("The field feature type is invalid")
		}

		// Verification feature remarks, length limit 1000
		if utf8.RuneCountInString(f.Description) > interfaces.MaxLength_PropertyFeatureDescription {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldFeatureComment).
				WithErrorDetails(fmt.Sprintf("The length of the field feature comment %s exceeds %d", f.Description, interfaces.MaxLength_PropertyFeatureDescription))
		}

		if f.RefProperty == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
				WithErrorDetails("The field feature ref field is null")
		}

		// Verify the reference fields of non-native features
		if !f.IsNative {
			// Whether the referenced field is in the field list
			if _, ok := fieldsMap[f.RefProperty]; !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
					WithErrorDetails(fmt.Sprintf("The field feature ref field '%s' is not in the field list", f.RefProperty))
			}

			// Whether the type of the referenced field conforms to the feature type
			if !resourcelogic.IsFeatureSupported(fieldsMap[f.RefProperty].Type, f.FeatureType) {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
					WithErrorDetails(fmt.Sprintf("The field feature ref field '%s' type '%s' is not supported", f.RefProperty, fieldsMap[f.RefProperty].Type))
			}
		}

		// Verify whether it has been enabled
		if f.IsDefault {
			if enabledMap[f.FeatureType] {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
					WithErrorDetails(fmt.Sprintf("Same type features can only have one default feature, current field feature name '%s' type is '%s'",
						f.FeatureName, f.FeatureType))
			}
			enabledMap[f.FeatureType] = true
		}
	}

	return nil
}
