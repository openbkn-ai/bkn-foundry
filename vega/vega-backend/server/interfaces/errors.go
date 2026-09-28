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

func (e *SourceReadForbiddenError) Error() string { return "source read forbidden" }
func (e *SourceReadForbiddenError) Unwrap() error { return e.Cause }

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

// RequestSideQueryError returns the client-safe reason for an invalid query shape.
func RequestSideQueryError(err error) (string, bool) {
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
