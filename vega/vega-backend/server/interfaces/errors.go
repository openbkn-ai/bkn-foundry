// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"errors"
	"fmt"
)

// SourceReadForbiddenError reports that a source account cannot read a resource.
// Connectors return this error so callers can choose the appropriate response.
type SourceReadForbiddenError struct{ Cause error }

// NewSourceReadForbiddenError preserves the source failure for diagnostics.
func NewSourceReadForbiddenError(cause error) *SourceReadForbiddenError {
	return &SourceReadForbiddenError{Cause: cause}
}

func (e *SourceReadForbiddenError) Error() string {
	if e.Cause == nil {
		return "source read forbidden"
	}
	return fmt.Sprintf("source read forbidden: %v", e.Cause)
}
func (e *SourceReadForbiddenError) Unwrap() error { return e.Cause }

// SourceMemoryLimitError 表示源数据库达到内存限制，原始错误仅通过错误链保留。
type SourceMemoryLimitError struct{ Cause error }

// NewSourceMemoryLimitError 将连接器识别到的源端内存限制转换为公共错误类型。
func NewSourceMemoryLimitError(cause error) *SourceMemoryLimitError {
	return &SourceMemoryLimitError{Cause: cause}
}

func (e *SourceMemoryLimitError) Error() string { return "source memory limit exceeded" }
func (e *SourceMemoryLimitError) Unwrap() error { return e.Cause }

// SourceQueryInvalidParameterReason identifies a client-safe source query failure category.
type SourceQueryInvalidParameterReason string

const SourceQueryInvalidParameterUnknownColumn SourceQueryInvalidParameterReason = "unknown_column"

// SourceQueryInvalidParameterError reports a source query rejected for invalid input.
// Connectors preserve the driver failure for diagnostics; callers decide the HTTP response.
type SourceQueryInvalidParameterError struct {
	Reason SourceQueryInvalidParameterReason
	Cause  error
}

// NewSourceQueryInvalidParameterError preserves the failure and its reason.
func NewSourceQueryInvalidParameterError(reason SourceQueryInvalidParameterReason, cause error) *SourceQueryInvalidParameterError {
	return &SourceQueryInvalidParameterError{Reason: reason, Cause: cause}
}

func (e *SourceQueryInvalidParameterError) Error() string {
	if e.Cause == nil {
		return "source query invalid parameter"
	}
	return fmt.Sprintf("source query invalid parameter: %v", e.Cause)
}
func (e *SourceQueryInvalidParameterError) Unwrap() error { return e.Cause }

// UnsupportedOperationError reports an unsupported operator with a caller-safe explanation.
type UnsupportedOperationError struct {
	Operation string
	Cause     error
}

// NewUnsupportedOperationError preserves the caller's explanation and original error chain.
// Cause must be safe for callers because Error includes its message.
func NewUnsupportedOperationError(operation string, cause error) *UnsupportedOperationError {
	return &UnsupportedOperationError{Operation: operation, Cause: cause}
}

func (e *UnsupportedOperationError) Error() string {
	msg := fmt.Sprintf("operation %s is not supported", e.Operation)
	if e.Cause != nil {
		msg += fmt.Sprintf(": %v", e.Cause)
	}
	return msg
}

func (e *UnsupportedOperationError) Unwrap() error { return e.Cause }

// ConditionBuildError identifies a caller-supplied condition that the query channel cannot represent.
type ConditionBuildError struct {
	Reason string
	Cause  error
}

// NewConditionBuildError formats a caller-safe condition failure.
func NewConditionBuildError(format string, args ...any) *ConditionBuildError {
	return &ConditionBuildError{Reason: fmt.Sprintf(format, args...)}
}

func (e *ConditionBuildError) Error() string { return e.Reason }
func (e *ConditionBuildError) Unwrap() error { return e.Cause }

// KeywordLimitError 表示精确查询值超过字段的 keyword 索引长度上限。
// 字段与上限独立保存，调用方无需解析错误文案。
type KeywordLimitError struct {
	Field string
	Limit int
	*ConditionBuildError
}

func NewKeywordLimitError(field string, limit int) *KeywordLimitError {
	return &KeywordLimitError{
		Field: field, Limit: limit,
		ConditionBuildError: NewConditionBuildError("value for field %s exceeds keyword ignore_above %d and cannot be compared exactly", field, limit),
	}
}

func (e *KeywordLimitError) Error() string { return e.ConditionBuildError.Error() }

func (e *KeywordLimitError) Unwrap() error { return e.ConditionBuildError }

// StoredConditionBuildError identifies a saved view condition that can no
// longer be compiled against the current source schema or index configuration.
type StoredConditionBuildError struct{ Cause error }

// NewStoredConditionBuildError preserves the failed saved condition's cause.
func NewStoredConditionBuildError(cause error) *StoredConditionBuildError {
	return &StoredConditionBuildError{Cause: cause}
}

func (e *StoredConditionBuildError) Error() string {
	if e.Cause == nil {
		return "stored view condition cannot be built"
	}
	return fmt.Sprintf("stored view condition cannot be built: %v", e.Cause)
}
func (e *StoredConditionBuildError) Unwrap() error { return e.Cause }

// OutputFieldErrorReason identifies a projection rejected before execution.
type OutputFieldErrorReason string

const (
	OutputFieldUnknown       OutputFieldErrorReason = "unknown_output_field"
	OutputFieldUnsupported   OutputFieldErrorReason = "unsupported_output_field"
	OutputFieldNoneQueryable OutputFieldErrorReason = "no_queryable_fields"
)

// OutputFieldError contains only public field names, never source SQL or driver errors.
type OutputFieldError struct {
	Reason OutputFieldErrorReason
	Field  string
}

// NewOutputFieldError identifies a field or channel capability rejected before execution.
func NewOutputFieldError(reason OutputFieldErrorReason, field string) *OutputFieldError {
	return &OutputFieldError{Reason: reason, Field: field}
}

func (e *OutputFieldError) Error() string {
	switch e.Reason {
	case OutputFieldUnknown:
		return fmt.Sprintf("unknown output field %q", e.Field)
	case OutputFieldUnsupported:
		return fmt.Sprintf("output field %q is not supported by this query channel", e.Field)
	default:
		return "resource schema has no queryable fields"
	}
}

// RequestSideQueryError returns the client-safe reason for an invalid query shape.
func RequestSideQueryError(err error) (string, bool) {
	var stored *StoredConditionBuildError
	if errors.As(err, &stored) {
		return "", false
	}
	var output *OutputFieldError
	if errors.As(err, &output) {
		return output.Error(), true
	}
	var invalidParameter *SourceQueryInvalidParameterError
	if errors.As(err, &invalidParameter) {
		if invalidParameter.Reason == SourceQueryInvalidParameterUnknownColumn {
			return "query references an unknown column", true
		}
		return "invalid source query parameter", true
	}
	var unsupported *UnsupportedOperationError
	if errors.As(err, &unsupported) {
		return unsupported.Error(), true
	}
	var build *ConditionBuildError
	if errors.As(err, &build) {
		return build.Error(), true
	}
	return "", false
}

// IndexCapabilitiesUnavailableError reports a failure to obtain index capabilities.
type IndexCapabilitiesUnavailableError struct{ Cause error }

// NewIndexCapabilitiesUnavailableError preserves the capability probe failure.
func NewIndexCapabilitiesUnavailableError(cause error) *IndexCapabilitiesUnavailableError {
	return &IndexCapabilitiesUnavailableError{Cause: cause}
}

func (e *IndexCapabilitiesUnavailableError) Error() string {
	return fmt.Sprintf("index capabilities unavailable: %v", e.Cause)
}
func (e *IndexCapabilitiesUnavailableError) Unwrap() error { return e.Cause }

// RawAggregationValidationError reports an aggregation that cannot be represented as resource data rows.
type RawAggregationValidationError struct {
	Path   string
	Reason string
	Cause  error
}

// NewRawAggregationValidationError identifies an invalid aggregation at a query path.
func NewRawAggregationValidationError(path, reason string) *RawAggregationValidationError {
	return &RawAggregationValidationError{Path: path, Reason: reason}
}

func (e *RawAggregationValidationError) Error() string {
	return fmt.Sprintf("invalid OpenSearch aggregation at %s: %s", e.Path, e.Reason)
}
func (e *RawAggregationValidationError) Unwrap() error { return e.Cause }

// ServerTimeZoneError 表示服务器时区配置错误，不属于调用方输入错误。
type ServerTimeZoneError struct{}

// NewServerTimeZoneError 创建不泄漏配置细节的服务时区错误。
func NewServerTimeZoneError() *ServerTimeZoneError { return &ServerTimeZoneError{} }
func (*ServerTimeZoneError) Error() string         { return "invalid server time zone configuration" }
