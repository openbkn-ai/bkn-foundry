// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCatalogVariableJSONRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"false", false, `{"value":false,"sensitive":false}`},
		{"zero", json.Number("0"), `{"value":0,"sensitive":false}`},
		{"empty string", "", `{"value":"","sensitive":false}`},
		{"nested null", map[string]any{"key": nil}, `{"value":{"key":null},"sensitive":false}`},
		{"redacted", nil, `{"sensitive":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(CatalogVariable{Value: tc.value})
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(encoded))
			var decoded CatalogVariable
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			roundTrip, err := json.Marshal(decoded)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(roundTrip))
		})
	}
}
