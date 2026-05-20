// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors Dataset 模块错误码
package errors

// Dataset 错误码（当前全部为 400 Bad Request：schema/字段定义校验）
const (
	// 400 Bad Request
	VegaBackend_Dataset_InvalidParameter_SchemaDefinition  = "VegaBackend.Dataset.InvalidParameter.SchemaDefinition"
	VegaBackend_Dataset_InvalidParameter_FieldName         = "VegaBackend.Dataset.InvalidParameter.FieldName"
	VegaBackend_Dataset_InvalidParameter_FieldType         = "VegaBackend.Dataset.InvalidParameter.FieldType"
	VegaBackend_Dataset_InvalidParameter_FieldFeatureName  = "VegaBackend.Dataset.InvalidParameter.FieldFeatureName"
	VegaBackend_Dataset_InvalidParameter_FieldFeatureType  = "VegaBackend.Dataset.InvalidParameter.FieldFeatureType"
	VegaBackend_Dataset_InvalidParameter_FieldFeatureRef   = "VegaBackend.Dataset.InvalidParameter.FieldFeatureRef"
	VegaBackend_Dataset_LengthExceeded_FieldName           = "VegaBackend.Dataset.LengthExceeded.FieldName"
	VegaBackend_Dataset_LengthExceeded_FieldDisplayName    = "VegaBackend.Dataset.LengthExceeded.FieldDisplayName"
	VegaBackend_Dataset_LengthExceeded_FieldComment        = "VegaBackend.Dataset.LengthExceeded.FieldComment"
	VegaBackend_Dataset_LengthExceeded_FieldFeatureName    = "VegaBackend.Dataset.LengthExceeded.FieldFeatureName"
	VegaBackend_Dataset_LengthExceeded_FieldFeatureComment = "VegaBackend.Dataset.LengthExceeded.FieldFeatureComment"
	VegaBackend_Dataset_Duplicated_FieldName               = "VegaBackend.Dataset.Duplicated.FieldName"
	VegaBackend_Dataset_Duplicated_FieldDisplayName        = "VegaBackend.Dataset.Duplicated.FieldDisplayName"
	VegaBackend_Dataset_Duplicated_FieldFeatureName        = "VegaBackend.Dataset.Duplicated.FieldFeatureName"
	VegaBackend_Dataset_Duplicated_DefaultFeaturePerType   = "VegaBackend.Dataset.Duplicated.DefaultFeaturePerType"
	VegaBackend_Dataset_Unsupported_FieldFeatureRefType    = "VegaBackend.Dataset.Unsupported.FieldFeatureRefType"
)

var DatasetErrCodeList = []string{
	// 400 Bad Request
	VegaBackend_Dataset_InvalidParameter_SchemaDefinition,
	VegaBackend_Dataset_InvalidParameter_FieldName,
	VegaBackend_Dataset_InvalidParameter_FieldType,
	VegaBackend_Dataset_InvalidParameter_FieldFeatureName,
	VegaBackend_Dataset_InvalidParameter_FieldFeatureType,
	VegaBackend_Dataset_InvalidParameter_FieldFeatureRef,
	VegaBackend_Dataset_LengthExceeded_FieldName,
	VegaBackend_Dataset_LengthExceeded_FieldDisplayName,
	VegaBackend_Dataset_LengthExceeded_FieldComment,
	VegaBackend_Dataset_LengthExceeded_FieldFeatureName,
	VegaBackend_Dataset_LengthExceeded_FieldFeatureComment,
	VegaBackend_Dataset_Duplicated_FieldName,
	VegaBackend_Dataset_Duplicated_FieldDisplayName,
	VegaBackend_Dataset_Duplicated_FieldFeatureName,
	VegaBackend_Dataset_Duplicated_DefaultFeaturePerType,
	VegaBackend_Dataset_Unsupported_FieldFeatureRefType,
}
