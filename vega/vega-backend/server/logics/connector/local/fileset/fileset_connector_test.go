// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package fileset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestColumnMetadata(t *testing.T) {
	t.Run("typed columns retain native types and stored flags", func(t *testing.T) {
		filterable := false
		column := interfaces.FilesetColumnMeta{Name: "path", Type: "unknown-native", Filterable: &filterable}
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": []interfaces.FilesetColumnMeta{column}, "mapping": make(chan int)}}
		columns, err := ColumnMetadata(resource)
		require.NoError(t, err)
		assert.Equal(t, map[string]interfaces.FilesetColumnMeta{"path": column}, columns)
		assert.Same(t, column.Filterable, columns["path"].Filterable)
		columns["new"] = interfaces.FilesetColumnMeta{Name: "new"}
		assert.Len(t, resource.SourceMetadata["columns"], 1)
		assert.False(t, filterable)
	})
	t.Run("persisted columns restore metadata without changing source", func(t *testing.T) {
		filterable := false
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"columns": []interfaces.FilesetColumnMeta{{Name: "path", Type: "string", Filterable: &filterable}}}, SchemaDefinition: []*interfaces.Property{{Name: "historical", OriginalType: "integer"}}}
		before, err := json.Marshal(resource)
		require.NoError(t, err)
		var stored interfaces.Resource
		require.NoError(t, json.Unmarshal(before, &stored))
		columns, err := ColumnMetadata(&stored)
		require.NoError(t, err)
		assert.Equal(t, map[string]interfaces.FilesetColumnMeta{"path": {Name: "path", Type: "string", Filterable: &filterable}}, columns)
		assert.NotContains(t, columns, "historical", "schema fallback belongs to the connector")
		after, err := json.Marshal(stored)
		require.NoError(t, err)
		assert.JSONEq(t, string(before), string(after))
	})
	t.Run("missing and explicitly empty columns are valid", func(t *testing.T) {
		for _, resource := range []*interfaces.Resource{nil, {}, {SchemaDefinition: []*interfaces.Property{{Name: "historical", OriginalType: "string"}}}, {SourceMetadata: map[string]any{}}} {
			columns, err := ColumnMetadata(resource)
			require.NoError(t, err)
			assert.Nil(t, columns)
		}
		for _, raw := range []any{[]interfaces.FilesetColumnMeta{}, []any{}} {
			columns, err := ColumnMetadata(&interfaces.Resource{SourceMetadata: map[string]any{"columns": raw}})
			require.NoError(t, err)
			assert.NotNil(t, columns)
			assert.Empty(t, columns)
		}
	})
	t.Run("corrupt stored columns return ordinary metadata errors", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			raw  any
		}{
			{"explicit nil", nil}, {"typed nil", []interfaces.FilesetColumnMeta(nil)}, {"JSON null", json.RawMessage("null")},
			{"invalid shape", "corrupt"}, {"marshal failure", make(chan int)}, {"invalid native type", []any{map[string]any{"type": 4}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				columns, err := ColumnMetadata(&interfaces.Resource{SourceMetadata: map[string]any{"columns": tc.raw}})
				require.Error(t, err)
				assert.Nil(t, columns)
				var invalid *interfaces.ConditionBuildError
				assert.NotErrorAs(t, err, &invalid)
			})
		}
	})
}
