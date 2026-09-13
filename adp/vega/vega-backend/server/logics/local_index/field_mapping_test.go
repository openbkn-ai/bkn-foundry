// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0. See the LICENSE file in the project root for details.

package local_index

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestBuildFieldMappings(t *testing.T) {
	t.Run("maps resource types and user-defined feature fields", func(t *testing.T) {
		properties, hasVectorField, err := buildFieldMappings([]*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer},
			{Name: "unsigned_id", Type: interfaces.DataType_UnsignedInteger},
			{Name: "amount", Type: interfaces.DataType_Decimal},
			{Name: "payload", Type: interfaces.DataType_Json},
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
		})

		require.NoError(t, err)
		assert.True(t, hasVectorField)
		assert.Equal(t, map[string]any{"type": "long"}, properties["id"])
		assert.Equal(t, map[string]any{"type": "unsigned_long"}, properties["unsigned_id"])
		assert.Equal(t, map[string]any{"type": "object"}, properties["payload"])
		assert.Equal(t, map[string]any{"type": "geo_point"}, properties["location"])
		assert.Equal(t, 1000000000000000000.0, properties["amount"].(map[string]any)["scaling_factor"])
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

	t.Run("creates fulltext subfield without feature config", func(t *testing.T) {
		properties, _, err := buildFieldMappings([]*interfaces.Property{{
			Name: "title",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}},
		}})

		require.NoError(t, err)
		title := properties["title"].(map[string]any)
		assert.Equal(t, map[string]any{"type": "text"}, title["fields"].(map[string]any)["fulltext"])
	})

	t.Run("creates keyword subfield without feature config", func(t *testing.T) {
		properties, _, err := buildFieldMappings([]*interfaces.Property{{
			Name: "body",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Keyword,
			}},
		}})

		require.NoError(t, err)
		body := properties["body"].(map[string]any)
		assert.Equal(t, map[string]any{"type": "keyword"}, body["fields"].(map[string]any)["keyword"])
	})

	t.Run("normalizes qualified feature names to subfield names", func(t *testing.T) {
		properties, _, err := buildFieldMappings([]*interfaces.Property{
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
		})

		require.NoError(t, err)
		titleFields := properties["title"].(map[string]any)["fields"].(map[string]any)
		assert.Contains(t, titleFields, "analyzed")
		assert.NotContains(t, titleFields, "title.analyzed")
		bodyFields := properties["body"].(map[string]any)["fields"].(map[string]any)
		assert.Contains(t, bodyFields, "raw")
		assert.NotContains(t, bodyFields, "body.raw")
	})

	t.Run("rejects unsupported resource type", func(t *testing.T) {
		properties, _, err := buildFieldMappings([]*interfaces.Property{{Name: "raw", Type: interfaces.DataType_Other, OriginalType: "_text"}})

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, "unsupported schema field")
	})

	t.Run("rejects unsupported configured feature", func(t *testing.T) {
		properties, _, err := buildFieldMappings([]*interfaces.Property{{
			Name: "name",
			Type: interfaces.DataType_String,
			Features: []interfaces.PropertyFeature{{
				FeatureType: "unsupported",
				Config:      map[string]any{"x": true},
			}},
		}})

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, "unsupported feature type")
	})

	t.Run("rejects unresolved generated vector dimension", func(t *testing.T) {
		properties, _, err := buildFieldMappings([]*interfaces.Property{{
			Name: "content",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
			}},
		}})

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, "has no resolved dimension")
	})

	t.Run("rejects generated vector field collision", func(t *testing.T) {
		properties, _, err := buildFieldMappings([]*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config:      map[string]any{"dimension": 768},
				}},
			},
			{Name: "content_vector", Type: interfaces.DataType_String},
		})

		require.Error(t, err)
		assert.Nil(t, properties)
		assert.ErrorContains(t, err, `generated vector field "content_vector" conflicts with declared field`)
	})
}
