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
	"github.com/stretchr/testify/require"
)

func TestRequestSideQueryError(t *testing.T) {
	t.Run("recognizes a wrapped condition build error", func(t *testing.T) {
		err := fmt.Errorf("build filter query: %w", NewConditionBuildError("text field %s needs keyword", "body"))

		reason, ok := RequestSideQueryError(err)

		require.True(t, ok)
		assert.Equal(t, "text field body needs keyword", reason)
	})

	t.Run("keeps unsupported operations request-side", func(t *testing.T) {
		reason, ok := RequestSideQueryError(NewUnsupportedOperationError("regex", QueryChannelSQL))

		require.True(t, ok)
		assert.Contains(t, reason, "regex")
	})

	t.Run("does not classify connector failures as request errors", func(t *testing.T) {
		_, ok := RequestSideQueryError(errors.New("opensearch unavailable"))
		assert.False(t, ok)
	})
}
