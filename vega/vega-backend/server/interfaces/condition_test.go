// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"encoding/json"
	"testing"

	"github.com/mitchellh/mapstructure"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterCondCfgWireContract(t *testing.T) {
	const stored = `{"operation":"and","sub_conditions":[{"field":"amount","operation":"eq","value":9007199254740993},{"operation":"or","sub_conditions":[{"field":"enabled","operation":"eq","value":false},{"field":"count","operation":"eq","value":0}]}]}`
	var fromJSON FilterCondCfg
	require.NoError(t, common.UnmarshalPreciseJSON([]byte(stored), &fromJSON))
	assert.Equal(t, "amount", fromJSON.SubConds[0].Field)
	assert.Equal(t, json.Number("9007199254740993"), fromJSON.SubConds[0].Value)
	assert.Equal(t, "enabled", fromJSON.SubConds[1].SubConds[0].Field)
	assert.Equal(t, false, fromJSON.SubConds[1].SubConds[0].Value)
	assert.Equal(t, json.Number("0"), fromJSON.SubConds[1].SubConds[1].Value)

	var storedMap map[string]any
	require.NoError(t, common.UnmarshalPreciseJSON([]byte(stored), &storedMap))
	var fromMap FilterCondCfg
	require.NoError(t, mapstructure.Decode(storedMap, &fromMap))
	assert.Equal(t, fromJSON, fromMap)

	for _, condition := range []*FilterCondCfg{&fromJSON, &fromMap} {
		condition.LegacyLikeWildcards = true
		encoded, err := json.Marshal(condition)
		require.NoError(t, err)
		assert.JSONEq(t, stored, string(encoded))
		assert.Contains(t, string(encoded), "9007199254740993")
		assert.NotContains(t, string(encoded), `"name"`)
		assert.NotContains(t, string(encoded), "LegacyLikeWildcards")

		var encodedMap map[string]any
		require.NoError(t, common.UnmarshalPreciseJSON(encoded, &encodedMap))
		var roundTrip FilterCondCfg
		require.NoError(t, mapstructure.Decode(encodedMap, &roundTrip))
		expected := *condition
		expected.LegacyLikeWildcards = false
		assert.Equal(t, expected, roundTrip)
	}
}
