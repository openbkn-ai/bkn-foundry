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
)

func TestResolveVectorConditionsResolvesModelIDBeforeVectorizing(t *testing.T) {
	ctrl := gomock.NewController(t)
	mfs := mock_interfaces.NewMockModelFactoryService(ctrl)
	bta := mock_interfaces.NewMockBuildTaskAccess(ctrl)
	model := &interfaces.SmallModel{ModelID: "model-1", BatchSize: 16}
	resource := &interfaces.Resource{
		ID:             "resource1",
		Name:           "orders",
		LocalIndexName: interfaces.BuildIndexName("resource1", "task1"),
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
	bta.EXPECT().GetByID(gomock.Any(), "task1").Return(&interfaces.BuildTask{
		IndexConfig: &interfaces.BuildTaskIndexConfig{Features: map[string]interfaces.BuildTaskFieldIndexFeature{
			"content": {Vector: &interfaces.SmallModel{ModelID: "model-1", EmbeddingDim: 1024}},
		}},
	}, nil)
	mfs.EXPECT().GetModelByID(gomock.Any(), "model-1").Return(model, nil)
	mfs.EXPECT().GetVector(gomock.Any(), model, []string{"search text"}).
		Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil)

	rds := &resourceDataService{mfs: mfs, bta: bta}
	err := rds.resolveVectorConditions(context.Background(), resource, cfg)

	require.NoError(t, err)
	assert.Equal(t, interfaces.LocalIndexVectorFieldName("content"), cfg.Name)
	assert.Equal(t, []float32{0.1, 0.2}, cfg.Value)
}
