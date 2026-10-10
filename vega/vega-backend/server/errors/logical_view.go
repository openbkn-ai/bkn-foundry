// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors LogicalView module error code
package errors

// LogicalView error code (currently all 400 Bad Request: join/ field definition check)
const (
	// 400 Bad Request
	VegaBackend_LogicalView_InvalidParameter_JoinType          = "VegaBackend.LogicalView.InvalidParameter.JoinType"
	VegaBackend_LogicalView_InvalidParameter_LogicDefinition   = "VegaBackend.LogicalView.InvalidParameter.LogicDefinition"
	VegaBackend_LogicalView_InvalidParameter_FieldName         = "VegaBackend.LogicalView.InvalidParameter.FieldName"
	VegaBackend_LogicalView_InvalidParameter_FieldFeatureName  = "VegaBackend.LogicalView.InvalidParameter.FieldFeatureName"
	VegaBackend_LogicalView_LengthExceeded_FieldName           = "VegaBackend.LogicalView.LengthExceeded.FieldName"
	VegaBackend_LogicalView_LengthExceeded_FieldDisplayName    = "VegaBackend.LogicalView.LengthExceeded.FieldDisplayName"
	VegaBackend_LogicalView_LengthExceeded_FieldComment        = "VegaBackend.LogicalView.LengthExceeded.FieldComment"
	VegaBackend_LogicalView_LengthExceeded_FieldFeatureName    = "VegaBackend.LogicalView.LengthExceeded.FieldFeatureName"
	VegaBackend_LogicalView_LengthExceeded_FieldFeatureComment = "VegaBackend.LogicalView.LengthExceeded.FieldFeatureComment"
	VegaBackend_LogicalView_Duplicated_NodeID                  = "VegaBackend.LogicalView.Duplicated.NodeID"
	VegaBackend_LogicalView_Duplicated_FieldName               = "VegaBackend.LogicalView.Duplicated.FieldName"
	VegaBackend_LogicalView_Duplicated_FieldDisplayName        = "VegaBackend.LogicalView.Duplicated.FieldDisplayName"
	VegaBackend_LogicalView_Duplicated_FieldFeatureName        = "VegaBackend.LogicalView.Duplicated.FieldFeatureName"
)

var LogicalViewErrCodeList = []string{
	// 400 Bad Request
	VegaBackend_LogicalView_InvalidParameter_JoinType,
	VegaBackend_LogicalView_InvalidParameter_LogicDefinition,
	VegaBackend_LogicalView_InvalidParameter_FieldName,
	VegaBackend_LogicalView_InvalidParameter_FieldFeatureName,
	VegaBackend_LogicalView_LengthExceeded_FieldName,
	VegaBackend_LogicalView_LengthExceeded_FieldDisplayName,
	VegaBackend_LogicalView_LengthExceeded_FieldComment,
	VegaBackend_LogicalView_LengthExceeded_FieldFeatureName,
	VegaBackend_LogicalView_LengthExceeded_FieldFeatureComment,
	VegaBackend_LogicalView_Duplicated_NodeID,
	VegaBackend_LogicalView_Duplicated_FieldName,
	VegaBackend_LogicalView_Duplicated_FieldDisplayName,
	VegaBackend_LogicalView_Duplicated_FieldFeatureName,
}
