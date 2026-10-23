// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

const (
	// 400
	BknBackend_ValueType_InvalidParameter     = "BknBackend.ValueType.InvalidParameter"
	BknBackend_ValueType_Duplicated_IDInFile  = "BknBackend.ValueType.Duplicated.IDInFile"
	BknBackend_ValueType_Duplicated_Name      = "BknBackend.ValueType.Duplicated.Name"
	BknBackend_ValueType_ValueTypeIDExisted   = "BknBackend.ValueType.ValueTypeIDExisted"
	BknBackend_ValueType_ValueTypeNameExisted = "BknBackend.ValueType.ValueTypeNameExisted"
	BknBackend_ValueType_NullParameter_Name   = "BknBackend.ValueType.NullParameter.Name"
	BknBackend_ValueType_LengthExceeded_Name  = "BknBackend.ValueType.LengthExceeded.Name"

	// 404
	BknBackend_ValueType_ValueTypeNotFound = "BknBackend.ValueType.ValueTypeNotFound"

	// 409
	BknBackend_ValueType_Referenced = "BknBackend.ValueType.Referenced"

	// 500
	BknBackend_ValueType_InternalError = "BknBackend.ValueType.InternalError"
)

var (
	ValueTypeErrCodeList = []string{
		BknBackend_ValueType_InvalidParameter,
		BknBackend_ValueType_Duplicated_IDInFile,
		BknBackend_ValueType_Duplicated_Name,
		BknBackend_ValueType_ValueTypeIDExisted,
		BknBackend_ValueType_ValueTypeNameExisted,
		BknBackend_ValueType_NullParameter_Name,
		BknBackend_ValueType_LengthExceeded_Name,
		BknBackend_ValueType_ValueTypeNotFound,
		BknBackend_ValueType_Referenced,
		BknBackend_ValueType_InternalError,
	}
)
