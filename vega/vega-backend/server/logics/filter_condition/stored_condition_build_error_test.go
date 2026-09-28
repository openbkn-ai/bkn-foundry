// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package filter_condition

import (
	"fmt"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsStoredConditionBuildError(t *testing.T) {
	stored := &StoredConditionBuildError{Cause: interfaces.NewConditionBuildError("field needs keyword")}
	err := fmt.Errorf("build filter query: %w", stored)
	identified, ok := AsStoredConditionBuildError(err)
	require.True(t, ok)
	assert.Same(t, stored, identified)
	assert.ErrorIs(t, err, stored.Cause)
}
