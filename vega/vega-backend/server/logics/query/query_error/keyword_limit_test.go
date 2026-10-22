// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE for the full text.

package query_error

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	_ "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/locale"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeywordLimitHTTPError(t *testing.T) {
	cause := interfaces.NewKeywordLimitError("status", 256)
	t.Run("preserves only safe metadata from a wrapped failure", func(t *testing.T) {
		got := KeywordLimitHTTPError(context.Background(), fmt.Errorf("private diagnostic: %w", cause))
		require.NotNil(t, got)
		assert.Equal(t, http.StatusBadRequest, got.HTTPCode)
		assert.Equal(t, "VegaBackend.Resource.QueryValueExceedsKeywordLimit", got.BaseError.ErrorCode)
		assert.Equal(t, map[string]any{"field": "status", "limit": 256}, got.BaseError.Metadata)
		assert.NotContains(t, got.Error(), "private diagnostic")
	})
	for name, err := range map[string]error{"nil": nil, "unknown": errors.New("ignore_above 256"), "generic condition": interfaces.NewConditionBuildError("ignore_above 256"), "stored": interfaces.NewStoredConditionBuildError(cause)} {
		t.Run(name, func(t *testing.T) { assert.Nil(t, KeywordLimitHTTPError(context.Background(), err)) })
	}
}
