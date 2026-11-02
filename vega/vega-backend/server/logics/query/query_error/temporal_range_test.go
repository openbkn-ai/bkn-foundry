// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query_error

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemporalRangeHTTPError(t *testing.T) {
	cause := interfaces.NewTemporalRangeError("created", "date_nanos", 2147483647, "day")
	for name, err := range map[string]error{
		"request": fmt.Errorf("private diagnostic: %w", cause),
		"saved":   interfaces.NewStoredConditionBuildError(cause),
	} {
		t.Run(name, func(t *testing.T) {
			got := TemporalRangeHTTPError(t.Context(), err)
			require.NotNil(t, got)
			assert.Equal(t, http.StatusBadRequest, got.HTTPCode)
			assert.Equal(t, "VegaBackend.Resource.QueryTimeOutOfRange", got.ErrorCode)
			assert.Equal(t, map[string]any{"field": "created", "field_type": "date_nanos", "interval": int64(2147483647), "unit": "day"}, got.Metadata)
			assert.NotContains(t, got.Error(), "private diagnostic")
			for _, lang := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
				got.Lang = lang
				require.NoError(t, got.Render())
				assert.Contains(t, got.Description, "created")
				assert.NotContains(t, got.Description, "<no value>")
			}
		})
	}
	for _, err := range []error{nil, errors.New("date before epoch"), interfaces.NewConditionBuildError("other condition")} {
		assert.Nil(t, TemporalRangeHTTPError(t.Context(), err))
	}
}
