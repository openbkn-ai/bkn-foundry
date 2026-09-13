// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See LICENSE-OPENBKN.txt in the project root for details.

package resource

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestValidateVectorFeatureReferenceDimensions(t *testing.T) {
	t.Run("accepts equal positive integer dimensions across numeric representations", func(t *testing.T) {
		schema := []*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					RefProperty: "embedding",
					Config:      map[string]any{"dimension": json.Number("768")},
				}},
			},
			{
				Name: "embedding",
				Type: interfaces.DataType_Vector,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config:      map[string]any{"dimension": float64(768)},
				}},
			},
		}

		require.NoError(t, ValidateVectorFeatureReferenceDimensions(schema))
	})

	t.Run("rejects a referenced vector with a different own dimension", func(t *testing.T) {
		schema := []*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					RefProperty: "embedding",
					Config:      map[string]any{"dimension": 768},
				}},
			},
			{
				Name: "embedding",
				Type: interfaces.DataType_Vector,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config:      map[string]any{"dimension": uint16(1024)},
				}},
			},
		}

		err := ValidateVectorFeatureReferenceDimensions(schema)

		require.Error(t, err)
		assert.ErrorContains(t, err, `vector feature on field "content" has dimension 768`)
		assert.ErrorContains(t, err, `referenced vector field "embedding" has dimension 1024`)
	})

	t.Run("requires the referenced field own vector dimension", func(t *testing.T) {
		schema := []*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					RefProperty: "embedding",
					Config:      map[string]any{"dimension": 768},
				}},
			},
			{Name: "embedding", Type: interfaces.DataType_Vector},
		}

		err := ValidateVectorFeatureReferenceDimensions(schema)

		require.Error(t, err)
		assert.ErrorContains(t, err, `referenced vector field "embedding" must define its own positive integer dimension`)
	})
}

func TestIsFeatureSupported(t *testing.T) {
	tests := []struct {
		propertyType string
		featureType  string
		want         bool
	}{
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_Integer, interfaces.PropertyFeatureType_Fulltext, false},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Keyword, true},
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Keyword, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_Vector, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_Text, "unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.propertyType+"/"+tt.featureType, func(t *testing.T) {
			assert.Equal(t, tt.want, IsFeatureSupported(tt.propertyType, tt.featureType))
		})
	}
}

func TestIsFeatureRefPropertyTypeSupported(t *testing.T) {
	assert.True(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_String, interfaces.PropertyFeatureType_Keyword))
	assert.True(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_Text, interfaces.PropertyFeatureType_Fulltext))
	assert.True(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_Vector, interfaces.PropertyFeatureType_Vector))
	assert.False(t, IsFeatureRefPropertyTypeSupported(interfaces.DataType_Text, interfaces.PropertyFeatureType_Keyword))
}

func TestNormalizeSelfReferencingFeatures(t *testing.T) {
	t.Run("drops ref_property pointing at the owning property", func(t *testing.T) {
		props := []*interfaces.Property{
			{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
				{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"},
			}},
		}

		NormalizeSelfReferencingFeatures(props)

		assert.Empty(t, props[0].Features[0].RefProperty)
	})

	t.Run("keeps ref_property pointing at another property", func(t *testing.T) {
		props := []*interfaces.Property{
			{Name: "title_keyword", Type: interfaces.DataType_String},
			{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
				{FeatureName: "title.keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "title_keyword"},
			}},
		}

		NormalizeSelfReferencingFeatures(props)

		assert.Equal(t, "title_keyword", props[1].Features[0].RefProperty)
	})

	t.Run("tolerates nil properties", func(t *testing.T) {
		assert.NotPanics(t, func() { NormalizeSelfReferencingFeatures([]*interfaces.Property{nil}) })
	})
}

func TestAddDefaultTextKeywordFeatures(t *testing.T) {
	t.Run("adds and persists a default keyword feature for text", func(t *testing.T) {
		props := []*interfaces.Property{{
			Name: "body",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureName: "fulltext",
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
			}},
		}}

		AddDefaultTextKeywordFeatures(props)

		require.Len(t, props[0].Features, 2)
		keyword := props[0].Features[1]
		assert.Equal(t, interfaces.LocalIndexKeywordSubfieldName, keyword.FeatureName)
		assert.Equal(t, interfaces.PropertyFeatureType_Keyword, keyword.FeatureType)
		assert.True(t, keyword.IsDefault)
		assert.Equal(t, interfaces.DefaultTextKeywordIgnoreAbove, keyword.Config["ignore_above"])
		assert.Empty(t, TextFieldsWithoutKeyword(props))
	})

	t.Run("preserves an explicitly configured keyword feature", func(t *testing.T) {
		props := []*interfaces.Property{{
			Name: "body",
			Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureName: "raw",
				FeatureType: interfaces.PropertyFeatureType_Keyword,
				Config:      map[string]any{"ignore_above": 128},
			}},
		}}

		AddDefaultTextKeywordFeatures(props)

		require.Len(t, props[0].Features, 1)
		assert.Equal(t, "raw", props[0].Features[0].FeatureName)
		assert.Equal(t, 128, props[0].Features[0].Config["ignore_above"])
	})

	t.Run("reports only legacy text fields", func(t *testing.T) {
		fields := TextFieldsWithoutKeyword([]*interfaces.Property{
			nil,
			{Name: "code", Type: interfaces.DataType_String},
			{Name: "body", Type: interfaces.DataType_Text},
			{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword}}},
		})

		assert.Equal(t, []string{"body"}, fields)
	})
}

// 存量资源带自引用特征、请求侧已在入口抹平：两边都归一化之后，一次没动 schema 的编辑
// 必须判定为「无 build 相关变更」，否则 resource_service 会清空 LocalIndexName，
// 让这次普通编辑把已建好的索引废掉。
func TestValidateMutableSchemaUpdateIgnoresNormalizedSelfReference(t *testing.T) {
	ctx := context.Background()
	legacyStored := func() []*interfaces.Property {
		return []*interfaces.Property{
			{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
				{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"},
			}},
		}
	}

	t.Run("unchanged schema is not a build relevant change", func(t *testing.T) {
		current := legacyStored()
		requested := legacyStored()
		NormalizeSelfReferencingFeatures(current)
		NormalizeSelfReferencingFeatures(requested)

		changed, err := validateMutableSchemaUpdate(ctx, current, requested, false)

		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("normalizing only one side would look like a feature change", func(t *testing.T) {
		current := legacyStored()
		requested := legacyStored()
		NormalizeSelfReferencingFeatures(requested)

		changed, err := validateMutableSchemaUpdate(ctx, current, requested, false)

		require.NoError(t, err)
		assert.True(t, changed, "guards the regression: a one-sided normalization clears LocalIndexName")
	})

	t.Run("a real feature change is still detected", func(t *testing.T) {
		current := legacyStored()
		NormalizeSelfReferencingFeatures(current)
		requested := []*interfaces.Property{
			{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{
				{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext},
				{FeatureName: "title_vector", FeatureType: interfaces.PropertyFeatureType_Vector},
			}},
		}

		changed, err := validateMutableSchemaUpdate(ctx, current, requested, false)

		require.NoError(t, err)
		assert.True(t, changed)
	})
}

func TestValidateDatasetVectorOutputs(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts a generated field that is not part of the logical schema", func(t *testing.T) {
		err := validateDatasetVectorOutputs(ctx, []*interfaces.Property{{
			Name: "content", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}},
		}})
		require.NoError(t, err)
	})

	t.Run("rejects logical field that collides with a generated vector output", func(t *testing.T) {
		err := validateDatasetVectorOutputs(ctx, []*interfaces.Property{
			{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}},
			{Name: "content_vector", Type: interfaces.DataType_Vector},
		})
		require.Error(t, err)
		assert.ErrorContains(t, err, "conflicts with a logical property")
	})

	t.Run("rejects ref_property on every dataset feature", func(t *testing.T) {
		err := validateDatasetVectorOutputs(ctx, []*interfaces.Property{{
			Name: "content", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Fulltext,
				RefProperty: "other_content",
			}},
		}})
		require.Error(t, err)
		assert.ErrorContains(t, err, "does not support ref_property")
	})
}

func TestMutableFeaturesEqualWithServerManagedDimension(t *testing.T) {
	current := []interfaces.PropertyFeature{{
		FeatureType: interfaces.PropertyFeatureType_Vector,
		Config:      map[string]any{"embedding_model": "model-1", "dimension": 768},
	}}

	withoutDimension := []interfaces.PropertyFeature{{
		FeatureType: interfaces.PropertyFeatureType_Vector,
		Config:      map[string]any{"embedding_model": "model-1"},
	}}
	withDifferentDimension := []interfaces.PropertyFeature{{
		FeatureType: interfaces.PropertyFeatureType_Vector,
		Config:      map[string]any{"embedding_model": "model-1", "dimension": 1024},
	}}

	assert.True(t, mutableFeaturesEqual(current, withoutDimension))
	assert.False(t, mutableFeaturesEqual(current, withDifferentDimension))
}
