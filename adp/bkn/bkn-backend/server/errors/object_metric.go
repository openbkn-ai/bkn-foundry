// Copyright openbkn.ai

package errors

const (
	BknBackend_ObjectMetric_InvalidDefinition = "BknBackend.ObjectMetric.InvalidDefinition"
	BknBackend_ObjectMetric_NotFound          = "BknBackend.ObjectMetric.NotFound"
	BknBackend_ObjectMetric_DuplicateCode     = "BknBackend.ObjectMetric.DuplicateCode"
	BknBackend_ObjectMetric_ImmutableVersion  = "BknBackend.ObjectMetric.ImmutableVersion"
	BknBackend_ObjectMetric_DependencyInvalid = "BknBackend.ObjectMetric.DependencyInvalid"
	BknBackend_ObjectMetric_InternalError     = "BknBackend.ObjectMetric.InternalError"
)

var ObjectMetricErrCodeList = []string{
	BknBackend_ObjectMetric_InvalidDefinition,
	BknBackend_ObjectMetric_NotFound,
	BknBackend_ObjectMetric_DuplicateCode,
	BknBackend_ObjectMetric_ImmutableVersion,
	BknBackend_ObjectMetric_DependencyInvalid,
	BknBackend_ObjectMetric_InternalError,
}
