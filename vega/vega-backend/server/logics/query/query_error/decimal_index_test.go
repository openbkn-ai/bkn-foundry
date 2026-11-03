// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_error

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestDecimalIndexHTTPError(t *testing.T) {
	for _, tc := range []struct {
		reason string
		status int
	}{{"value_out_of_range", http.StatusBadRequest}, {"invalid_aggregate_value", http.StatusInternalServerError}} {
		t.Run(tc.reason, func(t *testing.T) {
			err := fmt.Errorf("private source failure: %w", interfaces.NewDecimalIndexError("amount", tc.reason))
			got := DecimalIndexHTTPError(context.Background(), err)
			require.NotNil(t, got)
			assert.Equal(t, tc.status, got.HTTPCode)
			assert.Equal(t, map[string]any{"field": "amount", "reason": tc.reason}, got.Metadata)
			assert.NotContains(t, got.Error(), "private source failure")
		})
	}
	assert.Nil(t, DecimalIndexHTTPError(context.Background(), nil))
	assert.Nil(t, DecimalIndexHTTPError(context.Background(), errors.New("unknown")))
}
