// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

const (
	BknBackend_CapabilityBinding_InvalidParameter                     = "BknBackend.CapabilityBinding.InvalidParameter"
	BknBackend_CapabilityBinding_NullParameter_CapabilityID           = "BknBackend.CapabilityBinding.NullParameter.CapabilityID"
	BknBackend_CapabilityBinding_NullParameter_OwnerID                = "BknBackend.CapabilityBinding.NullParameter.OwnerID"
	BknBackend_CapabilityBinding_InvalidCapabilityType                = "BknBackend.CapabilityBinding.InvalidCapabilityType"
	BknBackend_CapabilityBinding_NotFound                             = "BknBackend.CapabilityBinding.NotFound"
	BknBackend_CapabilityBinding_InternalError                        = "BknBackend.CapabilityBinding.InternalError"
	BknBackend_CapabilityBinding_InternalError_BeginTransaction       = "BknBackend.CapabilityBinding.InternalError.BeginTransactionFailed"
	BknBackend_CapabilityBinding_InternalError_CreateBindingsFailed   = "BknBackend.CapabilityBinding.InternalError.CreateBindingsFailed"
	BknBackend_CapabilityBinding_InternalError_DeleteBindingsFailed   = "BknBackend.CapabilityBinding.InternalError.DeleteBindingsFailed"
	BknBackend_CapabilityBinding_InternalError_ListBindingsFailed     = "BknBackend.CapabilityBinding.InternalError.ListBindingsFailed"
	BknBackend_CapabilityBinding_InternalError_GetBindingsTotalFailed = "BknBackend.CapabilityBinding.InternalError.GetBindingsTotalFailed"
)

var CapabilityBindingErrCodeList = []string{
	BknBackend_CapabilityBinding_InvalidParameter,
	BknBackend_CapabilityBinding_NullParameter_CapabilityID,
	BknBackend_CapabilityBinding_NullParameter_OwnerID,
	BknBackend_CapabilityBinding_InvalidCapabilityType,
	BknBackend_CapabilityBinding_NotFound,
	BknBackend_CapabilityBinding_InternalError,
	BknBackend_CapabilityBinding_InternalError_BeginTransaction,
	BknBackend_CapabilityBinding_InternalError_CreateBindingsFailed,
	BknBackend_CapabilityBinding_InternalError_DeleteBindingsFailed,
	BknBackend_CapabilityBinding_InternalError_ListBindingsFailed,
	BknBackend_CapabilityBinding_InternalError_GetBindingsTotalFailed,
}
