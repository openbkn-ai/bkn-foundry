// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"errors"
	"fmt"
)

type DependencyErrorKind string

const (
	DependencyInvalidBinding  DependencyErrorKind = "invalid_binding"
	DependencyForbidden       DependencyErrorKind = "forbidden"
	DependencyNotFound        DependencyErrorKind = "not_found"
	DependencyTimeout         DependencyErrorKind = "timeout"
	DependencyUnavailable     DependencyErrorKind = "unavailable"
	DependencyDownstreamError DependencyErrorKind = "downstream_error"
	DependencyInvalidResponse DependencyErrorKind = "invalid_response"
)

// DependencyError carries only a safe machine-readable category. Raw response
// bodies and transport causes belong in controlled logs and traces.
type DependencyError struct {
	Dependency string
	Operation  string
	Kind       DependencyErrorKind
	HTTPStatus int
}

func (e *DependencyError) Error() string {
	if e == nil {
		return "dependency request failed"
	}
	return fmt.Sprintf("%s dependency %s failed: %s", e.Dependency, e.Operation, e.Kind)
}

func NewDependencyError(dependency, operation string, kind DependencyErrorKind, status int) error {
	return &DependencyError{Dependency: dependency, Operation: operation, Kind: kind, HTTPStatus: status}
}

// DependencyTransportKind distinguishes timeouts without retaining the raw
// network error in the error returned across the adapter boundary.
func DependencyTransportKind(err error) DependencyErrorKind {
	if errors.Is(err, context.DeadlineExceeded) {
		return DependencyTimeout
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return DependencyTimeout
	}
	return DependencyUnavailable
}
