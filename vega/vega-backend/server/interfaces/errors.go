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

// UnsupportedOperationError reports an operator unsupported by a query channel.
type UnsupportedOperationError struct {
	Operation string
	Channel   string
	Cause     error
}

// NewUnsupportedOperationError identifies an operator unsupported by the selected channel.
func NewUnsupportedOperationError(operation, channel string) *UnsupportedOperationError {
	return &UnsupportedOperationError{Operation: operation, Channel: channel}
}

func (e *UnsupportedOperationError) Error() string {
	msg := fmt.Sprintf("operation %s is not supported", e.Operation)
	if e.Channel != "" {
		msg += fmt.Sprintf(" by the %s query channel", e.Channel)
	}
	if hint := e.hint(); hint != "" {
		msg += "; " + hint
	}
	return msg
}

func (e *UnsupportedOperationError) Unwrap() error { return e.Cause }

func (e *UnsupportedOperationError) hint() string {
	if e.Channel != "sql" {
		return ""
	}
	switch e.Operation {
	case "match", "match_phrase", "multi_match":
		return "full-text operations need a local index on the resource: build one, " +
			"or use like/not_like/contain/prefix/regex on this channel"
	case "knn_vector":
		return "vector search needs a local index with a vector feature on the field: build one first"
	}
	return ""
}

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

// RequestSideQueryError returns the client-safe reason for an invalid query shape.
func RequestSideQueryError(err error) (string, bool) {
	var stored *StoredConditionBuildError
	if errors.As(err, &stored) {
		return "", false
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
