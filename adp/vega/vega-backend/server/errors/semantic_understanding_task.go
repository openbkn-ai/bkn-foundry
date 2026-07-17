// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package errors SemanticUnderstandingTask 模块错误码
package errors

const (
	// 404 Not Found
	VegaBackend_SemanticUnderstandingTask_NotFound = "VegaBackend.SemanticUnderstandingTask.NotFound"

	// 409 Conflict
	VegaBackend_SemanticUnderstandingTask_HasRunningExecution = "VegaBackend.SemanticUnderstandingTask.HasRunningExecution"

	// 500 Internal Server Error
	VegaBackend_SemanticUnderstandingTask_InternalError_DeleteFailed          = "VegaBackend.SemanticUnderstandingTask.InternalError.DeleteFailed"
	VegaBackend_SemanticUnderstandingTask_InternalError_GetAccountNamesFailed = "VegaBackend.SemanticUnderstandingTask.InternalError.GetAccountNamesFailed"
)

var SemanticUnderstandingTaskErrCodeList = []string{
	// 404 Not Found
	VegaBackend_SemanticUnderstandingTask_NotFound,

	// 409 Conflict
	VegaBackend_SemanticUnderstandingTask_HasRunningExecution,

	// 500 Internal Server Error
	VegaBackend_SemanticUnderstandingTask_InternalError_DeleteFailed,
	VegaBackend_SemanticUnderstandingTask_InternalError_GetAccountNamesFailed,
}
