// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConditionBuildError_SurvivesWrapping(t *testing.T) {
	inner := NewConditionBuildError("text field %s has no keyword feature", "material_number")
	wrapped := fmt.Errorf("failed to build filter query: %w", inner)

	got, ok := AsConditionBuildError(wrapped)
	assert.True(t, ok)
	assert.Equal(t, inner, got)

	msg, ok := RequestSideQueryError(wrapped)
	assert.True(t, ok)
	assert.Equal(t, "text field material_number has no keyword feature", msg)
}

func TestRequestSideQueryError_CoversUnsupportedOperation(t *testing.T) {
	msg, ok := RequestSideQueryError(fmt.Errorf("x: %w", NewUnsupportedOperationError(OperationMatch, QueryChannelSQL)))
	assert.True(t, ok)
	assert.Contains(t, msg, "operation match is not supported")

	_, ok = RequestSideQueryError(errors.New("connection refused"))
	assert.False(t, ok, "a transport failure is not a request-side error")
}
