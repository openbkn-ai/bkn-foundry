// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package resource_data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	"vega-backend/logics/filter_condition"
	"vega-backend/logics/local_index"
)

func TestResolveVectorConditionsResolvesModelIDBeforeVectorizing(t *testing.T) {
	ctrl := gomock.NewController(t)
	mfs := mock_interfaces.NewMockModelFactoryService(ctrl)
	model := &interfaces.SmallModel{ModelID: "model-1", BatchSize: 16}
	resource := &interfaces.Resource{
		ID:               "resource1",
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
		Name:      "content",
		Operation: filter_condition.OperationKnnVector,
		ValueOptCfg: interfaces.ValueOptCfg{
			Value: "search text",
		},
	}
	mfs.EXPECT().GetModelByID(gomock.Any(), "model-1").Return(model, nil)
	mfs.EXPECT().GetVector(gomock.Any(), model, []string{"search text"}).
		Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil)

	rds := &resourceDataService{mfs: mfs}
	err := rds.resolveVectorConditions(context.Background(), resource, cfg)

	require.NoError(t, err)
	assert.Equal(t, local_index.VectorFieldName("content"), cfg.Name)
	assert.Equal(t, []float32{0.1, 0.2}, cfg.Value)
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
