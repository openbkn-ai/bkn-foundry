package dataset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestMaterializeDocument(t *testing.T) {
	resource := &interfaces.Resource{
		LocalIndexName: "vega-dataset-index",
		IndexConfig:    &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "embedding-1"},
		SchemaDefinition: []*interfaces.Property{{
			Name: "content", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": float64(2)}}},
		}},
	}

	t.Run("generates a missing derived vector before writing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		ds := &datasetService{mfs: mfs}
		// The write path uses the dimension persisted in the Resource schema,
		// rather than resolving it again from the model registry.
		model := &interfaces.SmallModel{ModelID: "embedding-1", EmbeddingDim: 99}
		mfs.EXPECT().GetModelByID(gomock.Any(), "embedding-1").Return(model, nil)
		mfs.EXPECT().GetVector(gomock.Any(), model, []string{"hello"}).Return([]*interfaces.VectorResp{{Vector: []float32{0.1, 0.2}}}, nil)

		input := map[string]any{"content": "hello"}
		document, err := ds.materializeDocument(context.Background(), resource, input)

		require.NoError(t, err)
		assert.Equal(t, "hello", document["content"])
		assert.Equal(t, []float32{0.1, 0.2}, document["content_vector"])
		assert.NotContains(t, input, "content_vector")
	})

	t.Run("uses an explicit derived vector without inference", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		ds := &datasetService{mfs: mfs}

		document, err := ds.materializeDocument(context.Background(), resource, map[string]any{"content_vector": []any{0.1, 0.2}})

		require.NoError(t, err)
		assert.Equal(t, []any{0.1, 0.2}, document["content_vector"])
	})

	t.Run("accepts an explicit derived vector decoded with precise JSON", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		ds := &datasetService{mfs: mfs}
		var input map[string]any
		require.NoError(t, common.DecodePreciseJSON(strings.NewReader(`{"content_vector":[0.1,0.2]}`), &input))

		document, err := ds.materializeDocument(context.Background(), resource, input)

		require.NoError(t, err)
		assert.Equal(t, input["content_vector"], document["content_vector"])
	})

	t.Run("accepts a vector field decoded with precise JSON", func(t *testing.T) {
		resource := &interfaces.Resource{
			LocalIndexName: "vega-dataset-index",
			SchemaDefinition: []*interfaces.Property{{
				Name: "_vector", Type: interfaces.DataType_Vector,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": float64(2)}}},
			}},
		}
		var input map[string]any
		require.NoError(t, common.DecodePreciseJSON(strings.NewReader(`{"_vector":[0.1,0.2]}`), &input))

		document, err := (&datasetService{}).materializeDocument(context.Background(), resource, input)

		require.NoError(t, err)
		assert.Equal(t, input["_vector"], document["_vector"])
	})

	t.Run("does not write when inference fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		ds := &datasetService{mfs: mfs}
		model := &interfaces.SmallModel{ModelID: "embedding-1", EmbeddingDim: 2}
		mfs.EXPECT().GetModelByID(gomock.Any(), "embedding-1").Return(model, nil)
		mfs.EXPECT().GetVector(gomock.Any(), model, []string{"hello"}).Return(nil, errors.New("inference unavailable"))

		input := map[string]any{"content": "hello"}
		_, err := ds.materializeDocument(context.Background(), resource, input)

		require.Error(t, err)
		assert.ErrorContains(t, err, "inference unavailable")
		assert.NotContains(t, input, "content_vector")
	})

	t.Run("rejects a legacy vector feature without a persisted dimension", func(t *testing.T) {
		legacy := &interfaces.Resource{
			LocalIndexName: "vega-dataset-index",
			SchemaDefinition: []*interfaces.Property{{
				Name: "content", Type: interfaces.DataType_Text,
				Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector}},
			}},
		}

		_, err := (&datasetService{}).materializeDocument(context.Background(), legacy, map[string]any{"content": "hello"})

		require.ErrorContains(t, err, "no valid dimension")
	})
}
