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
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	opensearchConnector "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/index/opensearch"
	"go.uber.org/mock/gomock"
)

func TestLocalIndexManagerGeneratedFields(t *testing.T) {
	manager := &localIndexManager{lic: &opensearchConnector.OpenSearchConnector{}}
	t.Run("生成字段构造失败不生成过滤默认值", func(t *testing.T) {
		connector := vmock.NewMockIndexConnector(gomock.NewController(t))
		generated, err := (&localIndexManager{lic: connector}).GeneratedFields([]*interfaces.Property{{Name: "body", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}})
		require.ErrorContains(t, err, "no resolved dimension")
		assert.Nil(t, generated)
	})
	t.Run("派生向量字段使用实际 lic 的原生类型判定", func(t *testing.T) {
		for _, available := range []bool{false, true} {
			for _, original := range []*bool{nil, new(false), new(true)} {
				connector := vmock.NewMockIndexConnector(gomock.NewController(t))
				connector.EXPECT().IsFilterable(gomock.Any()).DoAndReturn(func(meta *interfaces.IndexFieldMeta) bool {
					assert.Equal(t, "knn_vector", meta.Type)
					assert.Equal(t, "body_vector", meta.Name)
					assert.Equal(t, 2, meta.Attributes["dimension"])
					assert.Equal(t, defaultVectorMethod(), meta.Attributes["method"])
					return available
				})
				property := &interfaces.Property{Name: "body", Type: interfaces.DataType_Text, Filterable: original,
					Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}
				generated, projectionErr := (&localIndexManager{lic: connector}).GeneratedFields([]*interfaces.Property{property})
				require.NoError(t, projectionErr)
				require.Contains(t, generated, "body_vector")
				require.NotNil(t, generated["body_vector"].Filterable)
				assert.Equal(t, available, *generated["body_vector"].Filterable)
				assert.Same(t, original, property.Filterable)
			}
		}
	})

	generated, projectionErr := manager.GeneratedFields([]*interfaces.Property{
		{Name: "stadium_name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}},
		{Name: "description", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{
			FeatureType: interfaces.PropertyFeatureType_Vector,
			RefProperty: "summary",
		}}},
		{Name: "embedding", Type: interfaces.DataType_Vector, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}},
		{Name: "city_name", Type: interfaces.DataType_String, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Fulltext}}},
	})

	require.NoError(t, projectionErr)
	require.Len(t, generated, 1)
	assert.Equal(t, interfaces.DataType_Vector, generated["stadium_name_vector"].Type)
	assert.NotContains(t, generated, "summary_vector")
	assert.NotContains(t, generated, "embedding_vector")
}
