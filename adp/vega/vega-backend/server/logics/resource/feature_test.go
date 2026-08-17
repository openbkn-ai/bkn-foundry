// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See LICENSE-OPENBKN.txt in the project root for details.

package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

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
