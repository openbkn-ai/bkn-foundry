// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestValidateLogicViewRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts valid logic view", func(t *testing.T) {
		req := &interfaces.ResourceRequest{
			Name:     "view",
			Category: interfaces.ResourceCategoryLogicView,
			LogicDefinition: []*interfaces.LogicDefinitionNode{
				{
					ID:   "out",
					Type: interfaces.LogicDefinitionNodeType_Output,
					OutputFields: []*interfaces.ViewProperty{
						{
							Property: interfaces.Property{
								Name: "title",
								Type: interfaces.DataType_Text,
								Features: []interfaces.PropertyFeature{
									{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"},
								},
							},
						},
					},
				},
			},
		}

		require.NoError(t, ValidateResourceRequest(ctx, req))
	})

	t.Run("returns logic definition error", func(t *testing.T) {
		req := &interfaces.ResourceRequest{
			Name:     "view",
			Category: interfaces.ResourceCategoryLogicView,
			LogicDefinition: []*interfaces.LogicDefinitionNode{
				{ID: "bad", Type: "bad"},
			},
		}

		require.Error(t, validateLogicViewRequest(ctx, req))
	})
}

func TestValidateLogicDefinition(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts nil definition", func(t *testing.T) {
		fields, err := validateLogicDefinition(ctx, nil)

		require.NoError(t, err)
		assert.Nil(t, fields)
	})

	t.Run("returns output fields", func(t *testing.T) {
		want := []*interfaces.ViewProperty{{Property: interfaces.Property{Name: "id", Type: interfaces.DataType_Integer}}}

		fields, err := validateLogicDefinition(ctx, []*interfaces.LogicDefinitionNode{
			{ID: "resource", Type: interfaces.LogicDefinitionNodeType_Resource},
			{ID: "out", Type: interfaces.LogicDefinitionNodeType_Output, OutputFields: want},
		})

		require.NoError(t, err)
		assert.Equal(t, want, fields)
	})

	t.Run("rejects too many nodes", func(t *testing.T) {
		nodes := make([]*interfaces.LogicDefinitionNode, 21)
		for i := range nodes {
			nodes[i] = &interfaces.LogicDefinitionNode{ID: "n", Type: interfaces.LogicDefinitionNodeType_Resource}
		}

		_, err := validateLogicDefinition(ctx, nodes)

		require.Error(t, err)
	})

	t.Run("rejects invalid node type", func(t *testing.T) {
		_, err := validateLogicDefinition(ctx, []*interfaces.LogicDefinitionNode{{ID: "bad", Type: "bad"}})

		require.Error(t, err)
	})
}

func TestValidateViewFields(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts valid fields and sets display name", func(t *testing.T) {
		fields := []*interfaces.ViewProperty{
			{Property: interfaces.Property{Name: "title", Type: interfaces.DataType_Text}},
		}

		err := validateViewFields(ctx, fields)

		require.NoError(t, err)
		assert.Equal(t, "title", fields[0].DisplayName)
	})

	t.Run("rejects invalid fields", func(t *testing.T) {
		tests := []struct {
			name   string
			fields []*interfaces.ViewProperty
		}{
			{name: "empty name", fields: []*interfaces.ViewProperty{{Property: interfaces.Property{Type: interfaces.DataType_String}}}},
			{name: "name too long", fields: []*interfaces.ViewProperty{{Property: interfaces.Property{Name: strings.Repeat("a", interfaces.MaxLength_PropertyName+1), Type: interfaces.DataType_String}}}},
			{name: "display name too long", fields: []*interfaces.ViewProperty{{Property: interfaces.Property{Name: "f1", DisplayName: strings.Repeat("a", interfaces.MaxLength_PropertyDisplayName+1), Type: interfaces.DataType_String}}}},
			{name: "description too long", fields: []*interfaces.ViewProperty{{Property: interfaces.Property{Name: "f1", Description: strings.Repeat("a", interfaces.MaxLength_PropertyDescription+1), Type: interfaces.DataType_String}}}},
			{name: "duplicate name", fields: []*interfaces.ViewProperty{
				{Property: interfaces.Property{Name: "f1", Type: interfaces.DataType_String}},
				{Property: interfaces.Property{Name: "f1", Type: interfaces.DataType_Integer}},
			}},
			{name: "duplicate display name", fields: []*interfaces.ViewProperty{
				{Property: interfaces.Property{Name: "f1", DisplayName: "same", Type: interfaces.DataType_String}},
				{Property: interfaces.Property{Name: "f2", DisplayName: "same", Type: interfaces.DataType_Integer}},
			}},
			{name: "invalid feature", fields: []*interfaces.ViewProperty{
				{Property: interfaces.Property{Name: "f1", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureName: "", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "f1"}}}},
			}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.Error(t, validateViewFields(ctx, tt.fields))
			})
		}
	})
}

func TestValidateFeatures(t *testing.T) {
	ctx := context.Background()
	fieldsMap := map[string]*interfaces.ViewProperty{
		"title": {Property: interfaces.Property{Name: "title", Type: interfaces.DataType_Text}},
		"name":  {Property: interfaces.Property{Name: "name", Type: interfaces.DataType_String}},
	}

	t.Run("accepts valid native and referenced features", func(t *testing.T) {
		features := []interfaces.PropertyFeature{
			{FeatureName: "title_fulltext", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"},
			{FeatureName: "native_keyword", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "not_in_map", IsNative: true},
		}

		require.NoError(t, validateFeatures(ctx, fieldsMap, features))
	})

	t.Run("rejects invalid features", func(t *testing.T) {
		tests := []struct {
			name     string
			features []interfaces.PropertyFeature
		}{
			{name: "empty feature name", features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "name"}}},
			{name: "feature name too long", features: []interfaces.PropertyFeature{{FeatureName: strings.Repeat("a", interfaces.MaxLength_PropertyFeatureName+1), FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "name"}}},
			{name: "duplicate feature name", features: []interfaces.PropertyFeature{
				{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "name"},
				{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Fulltext, RefProperty: "title"},
			}},
			{name: "invalid feature type", features: []interfaces.PropertyFeature{{FeatureName: "bad", FeatureType: "bad", RefProperty: "name"}}},
			{name: "description too long", features: []interfaces.PropertyFeature{{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "name", Description: strings.Repeat("a", interfaces.MaxLength_PropertyFeatureDescription+1)}}},
			{name: "empty ref property", features: []interfaces.PropertyFeature{{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Keyword}}},
			{name: "missing ref property", features: []interfaces.PropertyFeature{{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "missing"}}},
			{name: "unsupported ref type", features: []interfaces.PropertyFeature{{FeatureName: "kw", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "title"}}},
			{name: "duplicate default per type", features: []interfaces.PropertyFeature{
				{FeatureName: "kw1", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "name", IsDefault: true},
				{FeatureName: "kw2", FeatureType: interfaces.PropertyFeatureType_Keyword, RefProperty: "name", IsDefault: true},
			}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.Error(t, validateFeatures(ctx, fieldsMap, tt.features))
			})
		}
	})
}

func TestIsFeatureSupported(t *testing.T) {
	tests := []struct {
		fieldType   string
		featureType string
		want        bool
	}{
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Fulltext, true},
		{interfaces.DataType_Integer, interfaces.PropertyFeatureType_Fulltext, false},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Keyword, true},
		{interfaces.DataType_Text, interfaces.PropertyFeatureType_Keyword, false},
		{interfaces.DataType_Vector, interfaces.PropertyFeatureType_Vector, true},
		{interfaces.DataType_String, interfaces.PropertyFeatureType_Vector, false},
		{interfaces.DataType_Text, "unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.fieldType+"/"+tt.featureType, func(t *testing.T) {
			assert.Equal(t, tt.want, IsFeatureSupported(tt.fieldType, tt.featureType))
		})
	}
}
