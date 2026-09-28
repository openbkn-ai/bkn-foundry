// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"errors"
	"fmt"
)

// ConditionBuildError identifies a caller-supplied condition that the selected query channel
// cannot represent. The named type keeps request failures distinguishable from connector faults
// while the error crosses the local-index layers.
type ConditionBuildError struct {
	Reason string
}

// StoredConditionBuildError identifies a saved view condition that can no
// longer be compiled against the current source schema or index configuration.
type StoredConditionBuildError struct{ Cause error }

func (e *StoredConditionBuildError) Error() string {
	return "stored view condition cannot be built: " + e.Cause.Error()
}
func (e *StoredConditionBuildError) Unwrap() error { return e.Cause }

func AsStoredConditionBuildError(err error) (*StoredConditionBuildError, bool) {
	var target *StoredConditionBuildError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

func NewConditionBuildError(format string, args ...any) *ConditionBuildError {
	return &ConditionBuildError{Reason: fmt.Sprintf(format, args...)}
}

func (e *ConditionBuildError) Error() string {
	return e.Reason
}

func AsConditionBuildError(err error) (*ConditionBuildError, bool) {
	var target *ConditionBuildError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// RequestSideQueryError returns the client-safe reason for errors caused by the query shape.
func RequestSideQueryError(err error) (string, bool) {
	if _, stored := AsStoredConditionBuildError(err); stored {
		return "", false
	}
	if unsupported, ok := AsUnsupportedOperationError(err); ok {
		return unsupported.Error(), true
	}
	if build, ok := AsConditionBuildError(err); ok {
		return build.Error(), true
	}
	return "", false
}
