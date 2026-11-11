// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package index

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestFieldMetadata(t *testing.T) {
	t.Run("typed fields retain original attributes without JSON round trip", func(t *testing.T) {
		filterable := false
		value := make(chan int)
		field := interfaces.IndexFieldMeta{Name: "parent", Type: "unknown-native", ResolvedType: "native-target", Filterable: &filterable,
			Attributes: map[string]any{"index": false, "private": value},
			SubFields:  []interfaces.IndexSubFieldMeta{{Name: "raw", Type: "keyword", Attributes: map[string]any{"doc_values": true}}}}
		mapping := map[string]interfaces.IndexFieldMeta{"parent": field}
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"mapping": mapping, "columns": make(chan int)}}
		fields, err := FieldMetadata(resource)
		require.NoError(t, err)
		assert.Equal(t, map[string]interfaces.IndexFieldMeta{"parent": field}, fields)
		assert.Same(t, field.Filterable, fields["parent"].Filterable)
		assert.Equal(t, value, fields["parent"].Attributes["private"])
		assert.NotContains(t, fields, "parent.raw", "field lookup expansion belongs to the connector")
		fields["alias"] = interfaces.IndexFieldMeta{Name: "alias", Type: "alias"}
		assert.NotContains(t, mapping, "alias", "the returned lookup must not alias the source map")
		assert.False(t, filterable)
	})
	t.Run("persisted mapping retains native alias and child declarations", func(t *testing.T) {
		mapping := map[string]interfaces.IndexFieldMeta{
			"parent": {Name: "parent", Type: "text", Attributes: map[string]any{"index": false}, SubFields: []interfaces.IndexSubFieldMeta{{Name: "raw", Type: "keyword", Attributes: map[string]any{"doc_values": true}}}},
			"alias":  {Name: "alias", Type: "alias", Attributes: map[string]any{"path": "parent.raw"}},
		}
		resource := &interfaces.Resource{SourceMetadata: map[string]any{"mapping": mapping}, SchemaDefinition: []*interfaces.Property{{Name: "historical", OriginalType: "keyword"}}}
		before, err := json.Marshal(resource)
		require.NoError(t, err)
		var stored interfaces.Resource
		require.NoError(t, json.Unmarshal(before, &stored))
		fields, err := FieldMetadata(&stored)
		require.NoError(t, err)
		assert.Equal(t, mapping, fields)
		assert.NotContains(t, fields, "historical", "schema fallback belongs to the connector")
		assert.NotContains(t, fields, "parent.raw")
		after, err := json.Marshal(stored)
		require.NoError(t, err)
		assert.JSONEq(t, string(before), string(after))
	})
	t.Run("missing and explicitly empty mappings are valid", func(t *testing.T) {
		for _, resource := range []*interfaces.Resource{nil, {}, {SchemaDefinition: []*interfaces.Property{{Name: "historical", OriginalType: "keyword"}}}, {SourceMetadata: map[string]any{}}} {
			fields, err := FieldMetadata(resource)
			require.NoError(t, err)
			assert.Nil(t, fields)
		}
		for _, raw := range []any{map[string]interfaces.IndexFieldMeta{}, map[string]any{}} {
			fields, err := FieldMetadata(&interfaces.Resource{SourceMetadata: map[string]any{"mapping": raw}})
			require.NoError(t, err)
			assert.NotNil(t, fields)
			assert.Empty(t, fields)
		}
	})
	t.Run("corrupt mapping is not treated as absent metadata", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			raw  any
		}{
			{"explicit nil", nil}, {"typed nil", map[string]interfaces.IndexFieldMeta(nil)}, {"JSON null", json.RawMessage("null")},
			{"invalid shape", "corrupt"}, {"marshal failure", make(chan int)}, {"invalid native type", map[string]any{"id": map[string]any{"type": 4}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				fields, err := FieldMetadata(&interfaces.Resource{SourceMetadata: map[string]any{"mapping": tc.raw}})
				require.Error(t, err)
				assert.Nil(t, fields)
				var invalid *interfaces.ConditionBuildError
				assert.NotErrorAs(t, err, &invalid)
			})
		}
	})
}
