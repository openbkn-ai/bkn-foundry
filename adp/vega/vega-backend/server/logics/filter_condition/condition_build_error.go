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

// ConditionBuildError says a filter condition cannot be turned into a query on the current channel
// because of how the request is shaped: a text field compared without a keyword feature, a range
// with the wrong number of bounds, a value_from the channel does not read.
//
// It is a named type for the same reason UnsupportedOperationError is: the error crosses several
// layers before it reaches the caller, and each layer has to tell "the request needs fixing" (400,
// the caller can self-correct) from "the downstream is down" (500). A bare fmt.Errorf loses that
// distinction on the way up and is answered as an internal error.
type ConditionBuildError struct {
	Reason string
}

// NewConditionBuildError builds a request-side condition error from a format string.
func NewConditionBuildError(format string, args ...any) *ConditionBuildError {
	return &ConditionBuildError{Reason: fmt.Sprintf(format, args...)}
}

func (e *ConditionBuildError) Error() string {
	return e.Reason
}

// AsConditionBuildError unwraps err down to a ConditionBuildError, if one is in the chain.
func AsConditionBuildError(err error) (*ConditionBuildError, bool) {
	var target *ConditionBuildError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// RequestSideQueryError reports whether err is one the caller can fix by changing the request —
// an operator the channel does not implement, or a condition that cannot be built — and returns
// the message to hand back. Callers use it to answer 400 instead of 500.
func RequestSideQueryError(err error) (string, bool) {
	if unsupported, ok := AsUnsupportedOperationError(err); ok {
		return unsupported.Error(), true
	}
	if build, ok := AsConditionBuildError(err); ok {
		return build.Error(), true
	}
	return "", false
}
