// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package errors

const (
	BknBackend_KNDiff_InvalidParameter     = "BknBackend.KNDiff.InvalidParameter"
	BknBackend_KNDiff_ObjectTypeNotFound   = "BknBackend.KNDiff.ObjectTypeNotFound"
	BknBackend_KNDiff_DataStatsUnavailable = "BknBackend.KNDiff.DataStatsUnavailable"
	BknBackend_KNDiff_DataStatsQueryFailed = "BknBackend.KNDiff.DataStatsQueryFailed"
)

var KNDiffErrCodeList = []string{
	BknBackend_KNDiff_InvalidParameter,
	BknBackend_KNDiff_ObjectTypeNotFound,
	BknBackend_KNDiff_DataStatsUnavailable,
	BknBackend_KNDiff_DataStatsQueryFailed,
}
