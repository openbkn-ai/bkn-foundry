// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package local_index

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func TestBuildFieldMappings(t *testing.T) {
	t.Run("integer encoding cannot be overridden by features", func(t *testing.T) {
		for _, config := range []map[string]any{{"scaling_factor": nil}, {"scaling_factor": 1}, {"type": "scaled_float"}, {"coerce": true}, {"ignore_malformed": true}, {"null_value": 0}} {
			mapping, err := buildFieldMappings([]*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "numeric(2,-3)", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: config}}}}, true)
			var failure *interfaces.DecimalIndexError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, "unsafe_mapping_override", failure.Reason)
			assert.Nil(t, mapping)
		}
	})

	t.Run("features cannot override decimal safety", func(t *testing.T) {
		for _, config := range []map[string]any{{"scaling_factor": 1e18}, {"type": "double"}, {"ignore_malformed": true}, {"null_value": 0}} {
			mapping, err := buildFieldMappings([]*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)", Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, Config: config}}}}, true)
			var failure *interfaces.DecimalIndexError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, "unsafe_mapping_override", failure.Reason)
			assert.Nil(t, mapping)
		}
	})
	t.Run("decimal mapping uses declared scale and rejects unknown precision", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			original   string
			attributes map[string]any
			factor     float64
		}{
			{name: "amount", original: "decimal(12,2)", factor: 100},
			{name: "numeric", original: "NUMERIC(15,4)", factor: 10000},
			{name: "dataset", attributes: map[string]any{"precision": 12, "scale": 2}, factor: 100},
			{name: "integer decimal", original: "decimal(15,0)", factor: 1},
			{name: "negative scale", original: "numeric(2,-3)", factor: 1},
			{name: "unknown", original: "decimal"},
			{name: "wide declared precision", original: "decimal(38,18)", factor: 1e18},
			{name: "wide precision with cents", original: "decimal(38,2)", factor: 100},
			{name: "largest finite factor", original: "decimal(1000,308)", factor: 1e308},
			{name: "non finite factor", original: "decimal(1000,309)"},
			{name: "scale exceeds precision", original: "numeric(3,5)", factor: 1e5},
			{name: "dataset scale exceeds precision", attributes: map[string]any{"precision": 3, "scale": 5}, factor: 1e5},
			{name: "invalid precision", original: "decimal(0,13)"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				properties, err := buildFieldMappings([]*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: tc.original, Attributes: tc.attributes}}, true)
				if tc.factor == 0 {
					require.Error(t, err)
					assert.Nil(t, properties)
					return
				}
				require.NoError(t, err)
				if tc.factor == 1 {
					assert.Equal(t, map[string]any{"type": "long", "coerce": false}, properties["amount"])
				} else {
					assert.Equal(t, map[string]any{"type": "scaled_float", "scaling_factor": tc.factor}, properties["amount"])
				}
			})
		}
	})
	t.Run("maps resource types and user-defined feature fields", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer},
			{Name: "wide", Type: interfaces.DataType_LargeInteger},
			{Name: "unsigned_id", Type: interfaces.DataType_UnsignedInteger},
			{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "decimal(12,2)"},
			{Name: "payload", Type: interfaces.DataType_Json, Attributes: map[string]any{"enabled": false}},
			{Name: "location", Type: interfaces.DataType_Point},
			{Name: "embedding", Type: interfaces.DataType_Vector, Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
				Config:      map[string]any{"dimension": 3, "embedding_model": "model-1"},
			}}},
			{Name: "title", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{
				{FeatureName: "user-defined-keyword-name", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 128}},
				{FeatureName: "user-defined-fulltext-name", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "standard"}},
			}},
			{Name: "body", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
				{FeatureName: "user-defined-keyword-name", FeatureType: interfaces.PropertyFeatureType_Keyword, Config: map[string]any{"ignore_above": 256}},
				{FeatureName: "user-defined-fulltext-name", FeatureType: interfaces.PropertyFeatureType_Fulltext, Config: map[string]any{"analyzer": "hanlp_index"}},
				{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 768, "embedding_model": "model-2"}},
			}},
			{Name: "summary", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
				RefProperty: "embedding",
				Config:      map[string]any{"dimension": 3, "embedding_model": "model-1"},
			}}},
		}, true)

		require.NoError(t, err)
		assert.Equal(t, map[string]any{"type": "long"}, properties["id"])
		assert.Equal(t, map[string]any{"type": "keyword"}, properties["wide"])
		assert.Equal(t, map[string]any{"type": "unsigned_long"}, properties["unsigned_id"])
		assert.Equal(t, map[string]any{"type": "object", "enabled": false}, properties["payload"])
		assert.Equal(t, map[string]any{"type": "geo_point"}, properties["location"])
		assert.Equal(t, 100.0, properties["amount"].(map[string]any)["scaling_factor"])
		assert.Equal(t, map[string]any{"type": "knn_vector", "dimension": 3, "method": defaultVectorMethod()}, properties["embedding"])

		title := properties["title"].(map[string]any)
		assert.Equal(t, "keyword", title["type"])
		assert.Equal(t, 128, title["ignore_above"])
		assert.Equal(t, map[string]any{"type": "text", "analyzer": "standard"}, title["fields"].(map[string]any)["user-defined-fulltext-name"])
		body := properties["body"].(map[string]any)
		assert.Equal(t, "text", body["type"])
		assert.Equal(t, "hanlp_index", body["analyzer"])
		assert.Equal(t, map[string]any{"type": "keyword", "ignore_above": 256}, body["fields"].(map[string]any)["user-defined-keyword-name"])
		assert.Equal(t, map[string]any{"type": "knn_vector", "dimension": 768, "method": defaultVectorMethod()}, properties["body_vector"])
		assert.NotContains(t, properties, "summary_vector")
		assert.NotContains(t, properties, "embedding_vector")
	})

	t.Run("preserves JSON enabled attributes without adding defaults", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{
			{Name: "legacy", Type: interfaces.DataType_Json},
			{Name: "enabled", Type: interfaces.DataType_Json, Attributes: map[string]any{"enabled": true}},
			{Name: "disabled", Type: interfaces.DataType_Json, Attributes: map[string]any{"enabled": false}},
		}, true)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"type": "object"}, properties["legacy"])
		assert.Equal(t, map[string]any{"type": "object", "enabled": true}, properties["enabled"])
		assert.Equal(t, map[string]any{"type": "object", "enabled": false}, properties["disabled"])
	})

	t.Run("creates fulltext subfield without feature config", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{{
			Name: "title",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}},
		}}, true)

		require.NoError(t, err)
		title := properties["title"].(map[string]any)
		assert.Equal(t, map[string]any{"type": "text"}, title["fields"].(map[string]any)["fulltext"])
	})

	t.Run("creates keyword subfield without feature config", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{{
			Name: "body",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Keyword,
			}},
		}}, true)

		require.NoError(t, err)
		body := properties["body"].(map[string]any)
		assert.Equal(t, map[string]any{"type": "keyword"}, body["fields"].(map[string]any)["keyword"])
	})

	t.Run("normalizes qualified feature names to subfield names", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{
			{
				Name: "title",
				Type: interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "title.analyzed",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			},
			{
				Name: "body",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "body.raw",
					FeatureType: interfaces.PropertyFeatureType_Keyword,
				}},
			},
		}, true)

		require.NoError(t, err)
		titleFields := properties["title"].(map[string]any)["fields"].(map[string]any)
		assert.Contains(t, titleFields, "analyzed")
		assert.NotContains(t, titleFields, "title.analyzed")
		bodyFields := properties["body"].(map[string]any)["fields"].(map[string]any)
		assert.Contains(t, bodyFields, "raw")
		assert.NotContains(t, bodyFields, "body.raw")
	})

	t.Run("uses property name when original name differs", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{
			{
				Name:         "title",
				OriginalName: "source_title",
				Type:         interfaces.DataType_String,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "title.analyzed",
					FeatureType: interfaces.PropertyFeatureType_Fulltext,
				}},
			},
			{
				Name:         "body",
				OriginalName: "source_body",
				Type:         interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureName: "body.raw",
					FeatureType: interfaces.PropertyFeatureType_Keyword,
				}},
			},
		}, true)

		require.NoError(t, err)
		assert.Contains(t, properties["title"].(map[string]any)["fields"].(map[string]any), "analyzed")
		assert.Contains(t, properties["body"].(map[string]any)["fields"].(map[string]any), "raw")
	})

	t.Run("rejects unsupported resource type", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{{Name: "raw", Type: interfaces.DataType_Other, OriginalType: "_text"}}, true)

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, "unsupported schema field")
	})

	t.Run("rejects unsupported configured feature", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{{
			Name: "name",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{{
				FeatureType: "unsupported",
				Config:      map[string]any{"x": true},
			}},
		}}, true)

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, "unsupported feature type")
	})

	t.Run("rejects unresolved generated vector dimension", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{{
			Name: "content",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
			}},
		}}, true)

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, "has no resolved dimension")
	})

	t.Run("rejects generated vector field collision", func(t *testing.T) {
		properties, err := buildFieldMappings([]*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config:      map[string]any{"dimension": 768},
				}},
			},
			{Name: "content_vector", Type: interfaces.DataType_String},
		}, true)

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, `generated vector field "content_vector" conflicts with declared field`)
	})
}
