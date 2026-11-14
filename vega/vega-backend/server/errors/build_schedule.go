// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package errors

const (
	VegaBackend_BuildSchedule_InvalidCronExpr            = "VegaBackend.BuildSchedule.InvalidCronExpr"
	VegaBackend_BuildSchedule_InvalidExecuteType         = "VegaBackend.BuildSchedule.InvalidExecuteType"
	VegaBackend_BuildSchedule_InvalidTimeRange           = "VegaBackend.BuildSchedule.InvalidTimeRange"
	VegaBackend_BuildSchedule_InvalidParameter           = "VegaBackend.BuildSchedule.InvalidParameter"
	VegaBackend_BuildSchedule_NotFound                   = "VegaBackend.BuildSchedule.NotFound"
	VegaBackend_BuildSchedule_ResourceMismatch           = "VegaBackend.BuildSchedule.ResourceMismatch"
	VegaBackend_BuildSchedule_EnabledFieldNotAllowed     = "VegaBackend.BuildSchedule.EnabledFieldNotAllowed"
	VegaBackend_BuildSchedule_UpdateConflict             = "VegaBackend.BuildSchedule.UpdateConflict"
	VegaBackend_BuildSchedule_InternalError_GetFailed    = "VegaBackend.BuildSchedule.InternalError.GetFailed"
	VegaBackend_BuildSchedule_InternalError_CreateFailed = "VegaBackend.BuildSchedule.InternalError.CreateFailed"
	VegaBackend_BuildSchedule_InternalError_UpdateFailed = "VegaBackend.BuildSchedule.InternalError.UpdateFailed"
	VegaBackend_BuildSchedule_InternalError_DeleteFailed = "VegaBackend.BuildSchedule.InternalError.DeleteFailed"
)

var BuildScheduleErrCodeList = []string{
	VegaBackend_BuildSchedule_InvalidCronExpr,
	VegaBackend_BuildSchedule_InvalidExecuteType,
	VegaBackend_BuildSchedule_InvalidTimeRange,
	VegaBackend_BuildSchedule_InvalidParameter,
	VegaBackend_BuildSchedule_NotFound,
	VegaBackend_BuildSchedule_ResourceMismatch,
	VegaBackend_BuildSchedule_EnabledFieldNotAllowed,
	VegaBackend_BuildSchedule_UpdateConflict,
	VegaBackend_BuildSchedule_InternalError_GetFailed,
	VegaBackend_BuildSchedule_InternalError_CreateFailed,
	VegaBackend_BuildSchedule_InternalError_UpdateFailed,
	VegaBackend_BuildSchedule_InternalError_DeleteFailed,
}
