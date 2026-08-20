// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

// Error codes related to CatalogHealthCheckSchedule.
const (
	// 400 Bad Request
	VegaBackend_CatalogHealthCheckSchedule_InvalidParameter = "VegaBackend.CatalogHealthCheckSchedule.InvalidParameter"

	// 404 Not Found
	VegaBackend_CatalogHealthCheckSchedule_NotFound = "VegaBackend.CatalogHealthCheckSchedule.NotFound"

	// 409 Conflict
	VegaBackend_CatalogHealthCheckSchedule_UpdateConflict = "VegaBackend.CatalogHealthCheckSchedule.UpdateConflict"

	// 500 Internal Server Error
	VegaBackend_CatalogHealthCheckSchedule_InternalError              = "VegaBackend.CatalogHealthCheckSchedule.InternalError"
	VegaBackend_CatalogHealthCheckSchedule_InternalError_GetFailed    = "VegaBackend.CatalogHealthCheckSchedule.InternalError.GetFailed"
	VegaBackend_CatalogHealthCheckSchedule_InternalError_UpdateFailed = "VegaBackend.CatalogHealthCheckSchedule.InternalError.UpdateFailed"
)

var CatalogHealthCheckScheduleErrCodeList = []string{
	VegaBackend_CatalogHealthCheckSchedule_InvalidParameter,
	VegaBackend_CatalogHealthCheckSchedule_NotFound,
	VegaBackend_CatalogHealthCheckSchedule_UpdateConflict,
	VegaBackend_CatalogHealthCheckSchedule_InternalError,
	VegaBackend_CatalogHealthCheckSchedule_InternalError_GetFailed,
	VegaBackend_CatalogHealthCheckSchedule_InternalError_UpdateFailed,
}
