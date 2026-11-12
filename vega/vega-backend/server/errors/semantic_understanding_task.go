// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors SemanticUnderstandingTask module error code
package errors

const (
	VegaBackend_SemanticUnderstandingTask_NotApplicable             = "VegaBackend.SemanticUnderstandingTask.NotApplicable"
	VegaBackend_SemanticUnderstandingTask_SchemaMismatch            = "VegaBackend.SemanticUnderstandingTask.SchemaMismatch"
	VegaBackend_SemanticUnderstandingTask_ConfidenceBelowThreshold  = "VegaBackend.SemanticUnderstandingTask.ConfidenceBelowThreshold"
	VegaBackend_SemanticUnderstandingTask_InvalidResult             = "VegaBackend.SemanticUnderstandingTask.InvalidResult"
	VegaBackend_SemanticUnderstandingTask_InternalError_ApplyFailed = "VegaBackend.SemanticUnderstandingTask.InternalError.ApplyFailed"

	// 404 Not Found
	VegaBackend_SemanticUnderstandingTask_NotFound = "VegaBackend.SemanticUnderstandingTask.NotFound"

	// 409 Conflict
	VegaBackend_SemanticUnderstandingTask_HasRunningExecution = "VegaBackend.SemanticUnderstandingTask.HasRunningExecution"

	// 500 Internal Server Error
	VegaBackend_SemanticUnderstandingTask_InternalError_GetFailed             = "VegaBackend.SemanticUnderstandingTask.InternalError.GetFailed"
	VegaBackend_SemanticUnderstandingTask_InternalError_DeleteFailed          = "VegaBackend.SemanticUnderstandingTask.InternalError.DeleteFailed"
	VegaBackend_SemanticUnderstandingTask_InternalError_GetAccountNamesFailed = "VegaBackend.SemanticUnderstandingTask.InternalError.GetAccountNamesFailed"
)

var SemanticUnderstandingTaskErrCodeList = []string{
	VegaBackend_SemanticUnderstandingTask_NotApplicable,
	VegaBackend_SemanticUnderstandingTask_SchemaMismatch,
	VegaBackend_SemanticUnderstandingTask_ConfidenceBelowThreshold,
	VegaBackend_SemanticUnderstandingTask_InvalidResult,
	VegaBackend_SemanticUnderstandingTask_InternalError_ApplyFailed,

	// 404 Not Found
	VegaBackend_SemanticUnderstandingTask_NotFound,

	// 409 Conflict
	VegaBackend_SemanticUnderstandingTask_HasRunningExecution,

	// 500 Internal Server Error
	VegaBackend_SemanticUnderstandingTask_InternalError_GetFailed,
	VegaBackend_SemanticUnderstandingTask_InternalError_DeleteFailed,
	VegaBackend_SemanticUnderstandingTask_InternalError_GetAccountNamesFailed,
}
