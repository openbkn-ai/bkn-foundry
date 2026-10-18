// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSourceReadForbiddenError(t *testing.T) {
	cause := errors.New("database permission details")
	err := fmt.Errorf("execute query: %w", NewSourceReadForbiddenError(cause))
	var forbidden *SourceReadForbiddenError
	if !errors.As(err, &forbidden) || !errors.Is(err, cause) {
		t.Fatalf("wrapped source permission error lost its type or cause: %v", err)
	}
	if !strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("source permission error lost database diagnostics: %v", err)
	}
	if got := NewSourceReadForbiddenError(nil).Error(); got != "source read forbidden" {
		t.Fatalf("source permission error without cause = %q", got)
	}
}

func TestSourceQueryInvalidParameterError(t *testing.T) {
	cause := errors.New("unknown column in source query")
	err := fmt.Errorf("execute query: %w", NewSourceQueryInvalidParameterError(SourceQueryInvalidParameterUnknownColumn, cause))
	var invalid *SourceQueryInvalidParameterError
	if !errors.As(err, &invalid) || !errors.Is(err, cause) || invalid.Reason != SourceQueryInvalidParameterUnknownColumn {
		t.Fatalf("wrapped source query parameter error lost its type, reason, or cause: %v", err)
	}
	if got := NewSourceQueryInvalidParameterError("", nil).Error(); got != "source query invalid parameter" {
		t.Fatalf("source query parameter error without cause = %q", got)
	}
}

func TestIndexCapabilitiesUnavailableError(t *testing.T) {
	cause := errors.New("connection refused")
	err := fmt.Errorf("probe index: %w", NewIndexCapabilitiesUnavailableError(cause))
	var unavailable *IndexCapabilitiesUnavailableError
	if !errors.As(err, &unavailable) || !errors.Is(err, cause) {
		t.Fatalf("wrapped index capabilities error lost its type or cause: %v", err)
	}
}

func TestUnsupportedOperationErrorCause(t *testing.T) {
	cause := errors.New("full-text search requires a local index")
	err := NewUnsupportedOperationError("match", cause)
	wrapped := fmt.Errorf("build query: %w", err)
	if !errors.Is(wrapped, cause) {
		t.Fatal("unsupported operation error must preserve the supplied cause")
	}
	var unsupported *UnsupportedOperationError
	if !errors.As(wrapped, &unsupported) || unsupported.Operation != "match" || !errors.Is(unsupported.Cause, cause) {
		t.Fatalf("wrapped unsupported operation lost its type, operation, or cause: %v", wrapped)
	}
	if reason, ok := RequestSideQueryError(wrapped); !ok || !strings.Contains(reason, cause.Error()) {
		t.Fatalf("caller explanation was not classified as a request-side error: %q, %t", reason, ok)
	}
}

func TestUnsupportedOperationErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{nil, "operation match is not supported"},
		{errors.New("full-text search requires a local index"), "operation match is not supported: full-text search requires a local index"},
	} {
		err := NewUnsupportedOperationError("match", tc.cause)
		if got := err.Error(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
		if !errors.Is(err.Unwrap(), tc.cause) {
			t.Errorf("cause changed: %v", err.Unwrap())
		}
	}
}

func TestConditionBuildErrorCause(t *testing.T) {
	cause := errors.New("invalid source condition")
	err := NewConditionBuildError("field %s is unavailable", "title")
	err.Cause = cause
	if !errors.Is(fmt.Errorf("build query: %w", err), cause) {
		t.Fatal("condition build error must preserve its optional cause")
	}
}

func TestStoredConditionBuildError(t *testing.T) {
	cause := NewConditionBuildError("field needs keyword")
	stored := NewStoredConditionBuildError(cause)
	err := fmt.Errorf("build filter query: %w", stored)
	var identified *StoredConditionBuildError
	if !errors.As(err, &identified) || identified != stored || !errors.Is(err, cause) {
		t.Fatalf("wrapped stored condition error lost its type or cause: %v", err)
	}
	if !strings.Contains(stored.Error(), cause.Error()) {
		t.Fatalf("stored condition error lost its cause: %v", stored)
	}
	if got := NewStoredConditionBuildError(nil).Error(); got != "stored view condition cannot be built" {
		t.Fatalf("stored condition error without cause = %q", got)
	}
	if reason, ok := RequestSideQueryError(err); ok || reason != "" {
		t.Fatalf("saved condition was classified as a request error: %q, %t", reason, ok)
	}
}

func TestRequestSideQueryError(t *testing.T) {
	unknownColumn := fmt.Errorf("execute query: %w", NewSourceQueryInvalidParameterError(SourceQueryInvalidParameterUnknownColumn, errors.New("private driver column details")))
	if reason, ok := RequestSideQueryError(unknownColumn); !ok || reason != "query references an unknown column" {
		t.Errorf("unknown source column was not classified safely: %q, %t", reason, ok)
	}
	otherSourceFailure := NewSourceQueryInvalidParameterError("type_mismatch", errors.New("private driver type details"))
	if reason, ok := RequestSideQueryError(otherSourceFailure); !ok || reason != "invalid source query parameter" {
		t.Errorf("source query parameter failure was not classified safely: %q, %t", reason, ok)
	}
	build := fmt.Errorf("build filter query: %w", NewConditionBuildError("text field %s needs keyword", "body"))
	if reason, ok := RequestSideQueryError(build); !ok || reason != "text field body needs keyword" {
		t.Errorf("wrapped condition build error was not classified: %q, %t", reason, ok)
	}
	if reason, ok := RequestSideQueryError(NewUnsupportedOperationError("regex", errors.New("the sql query channel does not support this operation"))); !ok || !strings.Contains(reason, "regex") {
		t.Errorf("unsupported operation was not classified: %q, %t", reason, ok)
	}
	if reason, ok := RequestSideQueryError(errors.New("opensearch unavailable")); ok || reason != "" {
		t.Errorf("connector failure was classified as request error: %q, %t", reason, ok)
	}
}

func TestRawAggregationValidationError(t *testing.T) {
	cause := errors.New("invalid source aggregation")
	aggregationErr := NewRawAggregationValidationError("aggs.total", "unsupported aggregation")
	aggregationErr.Cause = cause
	err := fmt.Errorf("execute query: %w", aggregationErr)
	var validation *RawAggregationValidationError
	if !errors.As(err, &validation) || !errors.Is(err, cause) ||
		validation.Path != "aggs.total" || validation.Reason != "unsupported aggregation" {
		t.Fatalf("wrapped aggregation error lost its path or reason: %v", err)
	}
}

func TestContentTaskUnavailableError(t *testing.T) {
	t.Run("without cause", func(t *testing.T) {
		err := NewContentTaskUnavailableError(nil)

		if err.Error() != "content task dispatcher unavailable" {
			t.Fatalf("Error() = %q", err.Error())
		}
		if errors.Unwrap(err) != nil {
			t.Fatalf("Unwrap() = %v, want nil", errors.Unwrap(err))
		}
	})

	t.Run("wraps cause and is detectable through wrapping", func(t *testing.T) {
		cause := errors.New("worker down")
		wrapped := fmt.Errorf("dispatch: %w", NewContentTaskUnavailableError(cause))

		var target *ContentTaskUnavailableError
		if !errors.As(wrapped, &target) {
			t.Fatalf("errors.As(%v) = false", wrapped)
		}
		if !errors.Is(wrapped, cause) {
			t.Fatalf("errors.Is(%v, cause) = false", wrapped)
		}
		if target.Error() != "content task dispatcher unavailable: worker down" {
			t.Fatalf("Error() = %q", target.Error())
		}
	})
}
