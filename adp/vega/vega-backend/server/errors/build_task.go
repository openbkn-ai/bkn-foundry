// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors BuildTask module error code
package errors

// Buildtask-related error codes
const (
	// 400 Bad Request
	VegaBackend_BuildTask_Exist                                    = "VegaBackend.BuildTask.Exist"
	VegaBackend_BuildTask_Running                                  = "VegaBackend.BuildTask.Running"
	VegaBackend_BuildTask_InvalidStatus                            = "VegaBackend.BuildTask.InvalidStatus"
	VegaBackend_BuildTask_InvalidExecuteType                       = "VegaBackend.BuildTask.InvalidExecuteType"
	VegaBackend_BuildTask_InvalidParameter_ResourceID              = "VegaBackend.BuildTask.InvalidParameter.ResourceID"
	VegaBackend_BuildTask_InvalidParameter_Mode                    = "VegaBackend.BuildTask.InvalidParameter.Mode"
	VegaBackend_BuildTask_InvalidParameter_BuildKeyFields          = "VegaBackend.BuildTask.InvalidParameter.BuildKeyFields"
	VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields = "VegaBackend.BuildTask.InvalidParameter.UnsupportedSchemaFields"
	VegaBackend_BuildTask_InvalidParameter_Analyzer                = "VegaBackend.BuildTask.InvalidParameter.Analyzer"
	VegaBackend_BuildTask_InvalidParameter_EmbeddingModel          = "VegaBackend.BuildTask.InvalidParameter.EmbeddingModel"

	// 404 Not Found
	VegaBackend_BuildTask_NotFound = "VegaBackend.BuildTask.NotFound"

	// 409 Conflict
	VegaBackend_BuildTask_InvalidStateTransition = "VegaBackend.BuildTask.InvalidStateTransition"
	VegaBackend_BuildTask_HasRunningExecution    = "VegaBackend.BuildTask.HasRunningExecution"
	VegaBackend_BuildTask_ActiveIndexInUse       = "VegaBackend.BuildTask.ActiveIndexInUse"

	// 500 Internal Server Error
	VegaBackend_BuildTask_InternalError_CreateFailed           = "VegaBackend.BuildTask.InternalError.CreateFailed"
	VegaBackend_BuildTask_InternalError_GetFailed              = "VegaBackend.BuildTask.InternalError.GetFailed"
	VegaBackend_BuildTask_InternalError_UpdateFailed           = "VegaBackend.BuildTask.InternalError.UpdateFailed"
	VegaBackend_BuildTask_InternalError_DeleteFailed           = "VegaBackend.BuildTask.InternalError.DeleteFailed"
	VegaBackend_BuildTask_InternalError_GetAccountNamesFailed  = "VegaBackend.BuildTask.InternalError.GetAccountNamesFailed"
	VegaBackend_BuildTask_InternalError_ValidateAnalyzerFailed = "VegaBackend.BuildTask.InternalError.ValidateAnalyzerFailed"
)

var BuildTaskErrCodeList = []string{
	// 400 Bad Request
	VegaBackend_BuildTask_Exist,
	VegaBackend_BuildTask_Running,
	VegaBackend_BuildTask_InvalidStatus,
	VegaBackend_BuildTask_InvalidExecuteType,
	VegaBackend_BuildTask_InvalidParameter_ResourceID,
	VegaBackend_BuildTask_InvalidParameter_Mode,
	VegaBackend_BuildTask_InvalidParameter_BuildKeyFields,
	VegaBackend_BuildTask_InvalidParameter_UnsupportedSchemaFields,
	VegaBackend_BuildTask_InvalidParameter_Analyzer,
	VegaBackend_BuildTask_InvalidParameter_EmbeddingModel,

	// 404 Not Found
	VegaBackend_BuildTask_NotFound,

	// 409 Conflict
	VegaBackend_BuildTask_InvalidStateTransition,
	VegaBackend_BuildTask_HasRunningExecution,
	VegaBackend_BuildTask_ActiveIndexInUse,

	// 500 Internal Server Error
	VegaBackend_BuildTask_InternalError_CreateFailed,
	VegaBackend_BuildTask_InternalError_GetFailed,
	VegaBackend_BuildTask_InternalError_UpdateFailed,
	VegaBackend_BuildTask_InternalError_DeleteFailed,
	VegaBackend_BuildTask_InternalError_GetAccountNamesFailed,
	VegaBackend_BuildTask_InternalError_ValidateAnalyzerFailed,
}
