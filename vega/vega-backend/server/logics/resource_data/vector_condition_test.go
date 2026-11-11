// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	mock_interfaces "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/local_index"
)

func TestResolveVectorConditionsResolvesModelIDBeforeVectorizing(t *testing.T) {
	ctrl := gomock.NewController(t)
	mfs := mock_interfaces.NewMockModelFactoryService(ctrl)
	model := &interfaces.SmallModel{ModelID: "model-1", BatchSize: 16}
	resource := &interfaces.Resource{
		ID:               "resource1",
		Category:         interfaces.ResourceCategoryTable,
		Name:             "orders",
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
		LocalIndexName:   "managed-index",
		IndexConfig:      &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model-1"},
		SchemaDefinition: []*interfaces.Property{{
			Name: "content",
			Features: []interfaces.PropertyFeature{{
				FeatureType: interfaces.PropertyFeatureType_Vector,
			}},
		}},
	}
	cfg := &interfaces.FilterCondCfg{
		Field:     "content",
		Operation: filter_condition.OperationKnnVector,
		Value:     "search text",
	}
	mfs.EXPECT().GetModelByID(gomock.Any(), "model-1").Return(model, nil)
	mfs.EXPECT().GetVector(gomock.Any(), model, []string{"search text"}).
		Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil)

	rds := &resourceDataService{mfs: mfs, lim: newResourceDataLocalIndexManagerMock(ctrl)}
	err := rds.resolveVectorConditions(context.Background(), resource, cfg, false)

	require.NoError(t, err)
	assert.Equal(t, local_index.VectorFieldName("content"), cfg.Field)
	assert.Equal(t, []float32{0.1, 0.2}, cfg.Value)
}

func TestResolveVectorConditionsRejectsForcedSourceQueries(t *testing.T) {
	resource := &interfaces.Resource{
		Name:             "orders",
		Category:         interfaces.ResourceCategoryTable,
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
		LocalIndexName:   "managed-index",
	}
	for _, value := range []any{"search text", []float32{0.1, 0.2}} {
		cfg := &interfaces.FilterCondCfg{
			Field:     "content",
			Operation: filter_condition.OperationKnnVector,
			Value:     value,
		}

		err := (&resourceDataService{}).resolveVectorConditions(
			context.Background(), resource, cfg, true)

		require.ErrorContains(t, err, "ignore_local_index is true")
	}
}

func TestVectorFieldForReusesReferencedVectorField(t *testing.T) {
	resource := &interfaces.Resource{
		Name:             "articles",
		LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
		LocalIndexName:   "managed-index",
		SchemaDefinition: []*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					RefProperty: "embedding",
				}},
			},
			{Name: "embedding", Type: interfaces.DataType_Vector},
		},
	}

	field, err := vectorFieldFor(resource, "embedding")

	require.NoError(t, err)
	assert.Equal(t, "embedding", field)
}

func TestEmbeddingModelForIndexUsesReferencedVectorFieldOwner(t *testing.T) {
	resource := &interfaces.Resource{
		Name:        "articles",
		IndexConfig: &interfaces.ResourceIndexConfig{},
		SchemaDefinition: []*interfaces.Property{
			{
				Name: "content",
				Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					RefProperty: "embedding",
				}},
			},
			{
				Name: "embedding",
				Type: interfaces.DataType_Vector,
				Features: []interfaces.PropertyFeature{{
					FeatureType: interfaces.PropertyFeatureType_Vector,
					Config:      map[string]any{"embedding_model": "target-model"},
				}},
			},
		},
	}

	modelID, err := (&resourceDataService{}).embeddingModelForIndex(context.Background(), resource, "embedding")

	require.NoError(t, err)
	assert.Equal(t, "target-model", modelID)
}

func TestResolveVectorConditionsRejectsModelDimensionMismatch(t *testing.T) {
	t.Run("generated metadata errors precede model lookup and retain their cause", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
		models := mock_interfaces.NewMockModelFactoryService(ctrl)
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable, LocalIndexName: "index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}}}}}
		cause := errors.New("mapping failed")
		indexes.EXPECT().GeneratedFields(resource.SchemaDefinition).Return(nil, cause)
		cfg := &interfaces.FilterCondCfg{Field: "title", Operation: filter_condition.OperationKnnVector, Value: "query"}
		err := (&resourceDataService{lim: indexes, mfs: models}).resolveVectorConditions(t.Context(), resource, cfg, false)
		require.ErrorIs(t, err, cause)
		assert.Equal(t, "title", cfg.Field)
		assert.Equal(t, "query", cfg.Value)
	})

	t.Run("native index uses scanned vector metadata without requesting LIM schema", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		models := mock_interfaces.NewMockModelFactoryService(ctrl)
		indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
		model := &interfaces.SmallModel{ModelID: "model"}
		resource := &interfaces.Resource{Category: interfaces.ResourceCategoryIndex, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable,
			LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"},
			SchemaDefinition: []*interfaces.Property{{Name: "embedding", Type: interfaces.DataType_Vector,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 1024}}}}}}
		models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
		models.EXPECT().GetVector(gomock.Any(), model, []string{"query"}).Return([]*interfaces.VectorResp{{Vector: []float32{1, 2}}}, nil)
		cfg := &interfaces.FilterCondCfg{Field: "embedding", Operation: filter_condition.OperationKnnVector, Value: "query"}
		err := (&resourceDataService{mfs: models, lim: indexes}).resolveVectorConditions(t.Context(), resource, cfg, false)
		require.ErrorContains(t, err, "1024")
		assert.Equal(t, "embedding", cfg.Field)
		assert.Equal(t, "query", cfg.Value)
	})

	ctrl := gomock.NewController(t)
	models := mock_interfaces.NewMockModelFactoryService(ctrl)
	indexes := mock_interfaces.NewMockLocalIndexManager(ctrl)
	model := &interfaces.SmallModel{ModelID: "model"}
	resource := &interfaces.Resource{Category: interfaces.ResourceCategoryTable, LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable, LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "title", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 768}}}}}}
	cfg := &interfaces.FilterCondCfg{Field: "title", Operation: filter_condition.OperationKnnVector, ValueFrom: interfaces.ValueFrom_Const, Value: "query text"}
	models.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil)
	models.EXPECT().GetVector(gomock.Any(), model, []string{"query text"}).Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2, 0.3}}}, nil)
	// Use the manager's generated metadata, including its physical dimension.
	indexes.EXPECT().GeneratedFields(resource.SchemaDefinition).Return(map[string]*interfaces.Property{
		"title_vector": {Name: "title_vector", Type: interfaces.DataType_Vector, Filterable: new(true), Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 1024}}}},
	}, nil)
	err := (&resourceDataService{mfs: models, lim: indexes}).resolveVectorConditions(context.Background(), resource, cfg, false)
	require.Error(t, err)
	_, invalid := interfaces.RequestSideQueryError(err)
	assert.False(t, invalid)
	assert.Contains(t, err.Error(), "1024")
	assert.Equal(t, "title", cfg.Field)
	assert.Equal(t, "query text", cfg.Value)
}
