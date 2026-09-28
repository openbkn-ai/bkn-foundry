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

func TestIndexCapabilitiesUnavailableError(t *testing.T) {
	cause := errors.New("connection refused")
	err := fmt.Errorf("probe index: %w", NewIndexCapabilitiesUnavailableError(cause))
	var unavailable *IndexCapabilitiesUnavailableError
	if !errors.As(err, &unavailable) || !errors.Is(err, cause) {
		t.Fatalf("wrapped index capabilities error lost its type or cause: %v", err)
	}
}

func TestUnsupportedOperationErrorCause(t *testing.T) {
	cause := errors.New("source operation rejected")
	err := NewUnsupportedOperationError("regex", "sql")
	err.Cause = cause
	if !errors.Is(fmt.Errorf("build query: %w", err), cause) {
		t.Fatal("unsupported operation error must preserve its optional cause")
	}
}

func TestUnsupportedOperationErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		operation string
		channel   string
		want      string
	}{
		{operation: "match", channel: "sql", want: "local index"},
		{operation: "knn_vector", channel: "sql", want: "vector feature"},
		{operation: "match", channel: "opensearch", want: "opensearch query channel"},
	} {
		msg := NewUnsupportedOperationError(tc.operation, tc.channel).Error()
		if !strings.Contains(msg, tc.want) {
			t.Errorf("error %q should contain %q", msg, tc.want)
		}
		if tc.channel != "sql" && strings.Contains(msg, "local index") {
			t.Errorf("error %q should not suggest building an index", msg)
		}
	}
	wrapped := fmt.Errorf("execute query: %w", NewUnsupportedOperationError("match", "sql"))
	var unsupported *UnsupportedOperationError
	if !errors.As(wrapped, &unsupported) || unsupported.Operation != "match" {
		t.Fatalf("wrapped unsupported operation lost its type or operation: %v", wrapped)
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
	build := fmt.Errorf("build filter query: %w", NewConditionBuildError("text field %s needs keyword", "body"))
	if reason, ok := RequestSideQueryError(build); !ok || reason != "text field body needs keyword" {
		t.Errorf("wrapped condition build error was not classified: %q, %t", reason, ok)
	}
	if reason, ok := RequestSideQueryError(NewUnsupportedOperationError("regex", "sql")); !ok || !strings.Contains(reason, "regex") {
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
