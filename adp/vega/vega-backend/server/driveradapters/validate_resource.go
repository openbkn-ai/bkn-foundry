// Copyright 2026 kowell.ai
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

	"github.com/kweaver-ai/kweaver-go-lib/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics/extensions"
)

func ValidateResourceRequest(ctx context.Context, req *interfaces.ResourceRequest) error {
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
	if req.Extensions != nil {
		if err := extensions.ValidateEntityExtensionsMap(ctx, *req.Extensions); err != nil {
			return err
		}
	}

	switch req.Category {
	case interfaces.ResourceCategoryLogicView:
		return validateLogicViewRequest(ctx, req)
	case interfaces.ResourceCategoryDataset:
		return validateDatasetRequest(ctx, req)
	default:
		// 物理类资源在 createResource 入口已被 validateCreateResourceCategory 拦截，
		// 此处仅作 extensions 兜底校验。
		if err := extensions.ValidateSchemaPropertiesExtensions(ctx, req.SchemaDefinition); err != nil {
			return err
		}
		return nil
	}
}

// validateDatasetRequest 校验 dataset 类资源：schema_definition 必填且字段名/长度/重复/类型/特征均合法。
func validateDatasetRequest(ctx context.Context, req *interfaces.ResourceRequest) error {
	if len(req.SchemaDefinition) == 0 {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_SchemaDefinition).
			WithErrorDetails("schema_definition is required and must contain at least one field")
	}
	if err := validateDatasetFields(ctx, req.SchemaDefinition); err != nil {
		return err
	}
	return extensions.ValidateSchemaPropertiesExtensions(ctx, req.SchemaDefinition)
}

// validateDatasetFields 校验字段名/长度/重复/类型/特征。
func validateDatasetFields(ctx context.Context, props []*interfaces.Property) error {
	fieldsMap := make(map[string]*interfaces.Property, len(props))
	for _, p := range props {
		fieldsMap[p.Name] = p
	}

	nameMap := make(map[string]struct{})
	displayNameMap := make(map[string]struct{})
	for _, field := range props {
		if field.Name == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldName).
				WithErrorDetails("The field name is null")
		}
		if utf8.RuneCountInString(field.Name) > interfaces.MaxLength_PropertyName {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldName).
				WithErrorDetails(fmt.Sprintf("The length of the field name %s exceeds %d", field.Name, interfaces.MaxLength_PropertyName))
		}
		if field.DisplayName == "" {
			field.DisplayName = field.Name
		}
		if utf8.RuneCountInString(field.DisplayName) > interfaces.MaxLength_PropertyDisplayName {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldDisplayName).
				WithErrorDetails(fmt.Sprintf("The length of the field display name %s exceeds %d", field.DisplayName, interfaces.MaxLength_PropertyDisplayName))
		}
		if utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_LengthExceeded_FieldComment).
				WithErrorDetails(fmt.Sprintf("The length of the field comment %s exceeds %d", field.Description, interfaces.MaxLength_PropertyDescription))
		}
		if _, dup := nameMap[field.Name]; dup {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Duplicated_FieldName).
				WithDescription(map[string]any{"FieldName": field.Name}).
				WithErrorDetails(fmt.Sprintf("Dataset field '%s' already exists", field.Name))
		}
		nameMap[field.Name] = struct{}{}

		if _, dup := displayNameMap[field.DisplayName]; dup {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Duplicated_FieldDisplayName).
				WithDescription(map[string]any{"FieldName": field.Name, "DisplayName": field.DisplayName}).
				WithErrorDetails(fmt.Sprintf("Dataset field '%s' display name '%s' already exists", field.Name, field.DisplayName))
		}
		displayNameMap[field.DisplayName] = struct{}{}

		if !isValidDataType(field.Type) {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldType).
				WithErrorDetails(fmt.Sprintf("Dataset field '%s' type '%s' is invalid", field.Name, field.Type))
		}

		if err := validateDatasetFeatures(ctx, fieldsMap, field.Features); err != nil {
			return err
		}

		if len(field.Extensions) > 0 {
			if err := extensions.ValidatePropertyExtensionsMap(ctx, field.Extensions); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDatasetFeatures(ctx context.Context, fieldsMap map[string]*interfaces.Property, features []interfaces.PropertyFeature) error {
	enabledMap := make(map[string]bool)
	featureNameMap := make(map[string]struct{})
	for _, f := range features {
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

		if f.RefProperty == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureRef).
				WithErrorDetails("The field feature ref_property is null")
		}

		refField, exists := fieldsMap[f.RefProperty]
		if !f.IsNative {
			if !exists {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_InvalidParameter_FieldFeatureRef).
					WithErrorDetails(fmt.Sprintf("The field feature ref_property '%s' is not in the field list", f.RefProperty))
			}
			if !IsFeatureSupported(refField.Type, f.FeatureType) {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Dataset_Unsupported_FieldFeatureRefType).
					WithErrorDetails(fmt.Sprintf("The field feature ref_property '%s' type '%s' does not match feature type '%s'", f.RefProperty, refField.Type, f.FeatureType))
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
	if err := extensions.ValidateExtensionQueryPairs(ctx, params.ExtensionKeys, params.ExtensionValues); err != nil {
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
		interfaces.ResourceStatusDisabled,
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

	// 校验字段
	err = validateViewFields(ctx, outputFields)
	if err != nil {
		return err
	}

	return nil

}

// 校验逻辑视图定义
func validateLogicDefinition(ctx context.Context, nodes []*interfaces.LogicDefinitionNode) (outputFields []*interfaces.ViewProperty, err error) {
	if nodes == nil {
		return nil, nil
	}

	if len(nodes) > 20 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_LogicDefinition).
			WithErrorDetails("The logic definition nodes cannot be more than 20")
	}

	for _, node := range nodes {
		// 检测 nodeType
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

// 校验字段和字段特征
func validateViewFields(ctx context.Context, viewFields []*interfaces.ViewProperty) error {
	fieldsMap := make(map[string]*interfaces.ViewProperty)
	for _, field := range viewFields {
		fieldsMap[field.Name] = field
	}

	// 校验字段名称、显示名是否重复
	nameMap := make(map[string]struct{})
	displayNameMap := make(map[string]struct{})
	for _, field := range viewFields {
		if field.Name == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_FieldName).
				WithErrorDetails("The field name is null")
		}

		// 校验字段名称长度, 长度限制255
		if utf8.RuneCountInString(field.Name) > interfaces.MaxLength_PropertyName {
			errDetails := fmt.Sprintf("The length of the field name %s exceeds %d", field.Name, interfaces.MaxLength_PropertyName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldName).
				WithErrorDetails(errDetails)
		}

		// 如果display_name为 "", 将display_name的值等于field的值
		if field.DisplayName == "" {
			field.DisplayName = field.Name
		}

		// 校验字段显示名长度, 长度限制255
		if utf8.RuneCountInString(field.DisplayName) > interfaces.MaxLength_PropertyDisplayName {
			errDetails := fmt.Sprintf("The length of the field display name %s exceeds %d", field.DisplayName, interfaces.MaxLength_PropertyDisplayName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldDisplayName).
				WithErrorDetails(errDetails)
		}

		// 校验字段备注长度，长度限制1000
		if utf8.RuneCountInString(field.Description) > interfaces.MaxLength_PropertyDescription {
			errDetails := fmt.Sprintf("The length of the field comment %s exceeds %d", field.Description, interfaces.MaxLength_PropertyDescription)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldComment).
				WithErrorDetails(errDetails)
		}

		// 校验字段名称是否重复
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

		// 校验特征
		err := validateFeatures(ctx, fieldsMap, field.Features)
		if err != nil {
			return err
		}

		if len(field.Extensions) > 0 {
			if err := extensions.ValidatePropertyExtensionsMap(ctx, field.Extensions); err != nil {
				return err
			}
		}
	}

	return nil
}

// 校验特征
func validateFeatures(ctx context.Context, fieldsMap map[string]*interfaces.ViewProperty, features []interfaces.PropertyFeature) error {
	enabledMap := make(map[string]bool)
	featureNameMap := make(map[string]struct{})
	for _, f := range features {
		if f.FeatureName == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_InvalidParameter_FieldFeatureName).
				WithErrorDetails("The field feature name is null")
		}

		// 校验特征名称长度, 长度限制255
		if utf8.RuneCountInString(f.FeatureName) > interfaces.MaxLength_PropertyFeatureName {
			errDetails := fmt.Sprintf("The length of the field feature name %s exceeds %d", f.FeatureName, interfaces.MaxLength_PropertyFeatureName)
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldFeatureName).
				WithErrorDetails(errDetails)
		}

		// 校验特征名称是否重复
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

		// 校验特征备注，长度限制1000
		if utf8.RuneCountInString(f.Description) > interfaces.MaxLength_PropertyFeatureDescription {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_LogicView_LengthExceeded_FieldFeatureComment).
				WithErrorDetails(fmt.Sprintf("The length of the field feature comment %s exceeds %d", f.Description, interfaces.MaxLength_PropertyFeatureDescription))
		}

		if f.RefProperty == "" {
			return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
				WithErrorDetails("The field feature ref field is null")
		}

		// 校验非原生特征的引用字段
		if !f.IsNative {
			// 引用字段是否在字段列表里
			if _, ok := fieldsMap[f.RefProperty]; !ok {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
					WithErrorDetails(fmt.Sprintf("The field feature ref field '%s' is not in the field list", f.RefProperty))
			}

			// 引用字段的类型是否符合特征类型
			if !IsFeatureSupported(fieldsMap[f.RefProperty].Type, f.FeatureType) {
				return rest.NewHTTPError(ctx, http.StatusBadRequest, rest.PublicError_BadRequest).
					WithErrorDetails(fmt.Sprintf("The field feature ref field '%s' type '%s' is not supported", f.RefProperty, fieldsMap[f.RefProperty].Type))
			}
		}

		// 校验是否已启用
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

func IsFeatureSupported(fieldType string, featureType string) bool {
	switch featureType {
	case interfaces.PropertyFeatureType_Fulltext:
		return fieldType == interfaces.DataType_Text
	case interfaces.PropertyFeatureType_Keyword:
		// OpenSearch multi-field 模式：text 字段允许带 keyword 子字段（同一个
		// 物理字段同时支持全文检索 + 精确匹配），这是 ES/OpenSearch 行业标准，
		// 也是本服务自己 parseProperties 收编 sub-fields 时产出的 mapping 形态。
		// 因此 keyword 特征允许引用 string 或 text 字段。
		return fieldType == interfaces.DataType_String || fieldType == interfaces.DataType_Text
	case interfaces.PropertyFeatureType_Vector:
		return fieldType == interfaces.DataType_Vector
	default:
		return false
	}
}
