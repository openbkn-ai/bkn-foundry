// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package dataset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

func TestMaterializeDocument(t *testing.T) {
	resource := &interfaces.Resource{
		LocalIndexName: "vega-dataset-index",
		IndexConfig:    &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "embedding-1"},
		SchemaDefinition: []*interfaces.Property{{
			Name: "content", Type: interfaces.DataType_Text,
			Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": json.Number("2")}}},
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
		assert.Equal(t, []any{json.Number("0.1"), json.Number("0.2")}, document["content_vector"])
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
		assert.Equal(t, []any{json.Number("0.1"), json.Number("0.2")}, document["_vector"])
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

func TestMaterializeDocuments(t *testing.T) {

	for i, id := range []any{nil, 123, "", " \t", strings.Repeat("a", 513), strings.Repeat("界", 171), string([]byte{0xff})} {
		t.Run(fmt.Sprintf("invalid ID %d", i), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mfs := vmock.NewMockModelFactoryService(ctrl)
			resource := &interfaces.Resource{LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}}}
			results, err := (&datasetService{mfs: mfs}).materializeDocuments(context.Background(), resource, []map[string]any{{"_id": "valid", "content": "hello"}, {"_id": id}})
			require.Error(t, err)
			assert.Nil(t, results)
		})
	}
	for _, tc := range []struct {
		name                string
		modelErr, vectorErr error
	}{
		{name: "batches inference"}, {name: "registry failure", modelErr: errors.New("secret registry detail")}, {name: "inference failure", vectorErr: errors.New("secret model detail")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mfs := vmock.NewMockModelFactoryService(ctrl)
			resource := &interfaces.Resource{LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 2}}}}}}
			model := &interfaces.SmallModel{ModelID: "model"}
			mfs.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, tc.modelErr).Times(1)
			if tc.modelErr == nil {
				mfs.EXPECT().GetVector(gomock.Any(), model, []string{"one", "two"}).Return([]*interfaces.VectorResp{{Vector: []float32{1, 2}}, {Vector: []float32{3, 4}}}, tc.vectorErr).Times(1)
			}
			inputs := []map[string]any{{"_id": "first", "content": "one"}, {"_id": "second", "content": "two"}, {"_id": "explicit", "content_vector": []float32{5, 6}}}

			results, err := (&datasetService{mfs: mfs}).materializeDocuments(context.Background(), resource, inputs)
			if tc.modelErr != nil || tc.vectorErr != nil {
				require.Error(t, err)
				assert.Nil(t, results)
				if tc.modelErr != nil {
					assert.ErrorIs(t, err, tc.modelErr)
				} else {
					assert.ErrorIs(t, err, tc.vectorErr)
				}
			} else {
				require.NoError(t, err)
				require.Len(t, results, 3)
				assert.Equal(t, "first", results[0].ID)
				assert.Equal(t, "second", results[1].ID)
				assert.Equal(t, "explicit", results[2].ID)
				for i, result := range results {
					assert.NotContains(t, result.Document, "_id")
					assert.Equal(t, result.ID, inputs[i]["_id"])
				}
				assert.Equal(t, []float32{1, 2}, results[0].Document["content_vector"])
				assert.Equal(t, []float32{3, 4}, results[1].Document["content_vector"])
				assert.Equal(t, []float32{5, 6}, results[2].Document["content_vector"])
			}
			assert.NotContains(t, inputs[0], "content_vector")
		})
	}
	t.Run("bounds inference calls and keeps document association", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		model := &interfaces.SmallModel{ModelID: "model"}
		resource := &interfaces.Resource{LocalIndexName: "index", IndexConfig: &interfaces.ResourceIndexConfig{DefaultEmbeddingModel: "model"}, SchemaDefinition: []*interfaces.Property{{Name: "content", Type: interfaces.DataType_Text, Features: []interfaces.PropertyFeature{{FeatureType: interfaces.PropertyFeatureType_Vector, Config: map[string]any{"dimension": 1}}}}}}
		mfs.EXPECT().GetModelByID(gomock.Any(), "model").Return(model, nil).Times(1)
		calls := 0
		mfs.EXPECT().GetVector(gomock.Any(), model, gomock.Any()).DoAndReturn(func(_ context.Context, _ *interfaces.SmallModel, words []string) ([]*interfaces.VectorResp, error) {
			calls++
			assert.LessOrEqual(t, len(words), 128)
			result := make([]*interfaces.VectorResp, len(words))
			for i, word := range words {
				value, err := strconv.Atoi(word)
				require.NoError(t, err)
				result[i] = &interfaces.VectorResp{Vector: []float32{float32(value)}}
			}
			return result, nil
		}).Times(2)
		inputs := make([]map[string]any, 129)
		for i := range inputs {
			inputs[i] = map[string]any{"_id": strconv.Itoa(i), "content": strconv.Itoa(i)}
		}
		results, err := (&datasetService{mfs: mfs}).materializeDocuments(context.Background(), resource, inputs)
		assert.Equal(t, 2, calls)
		require.NoError(t, err)
		for i := range inputs {
			assert.Equal(t, strconv.Itoa(i), results[i].ID)
			assert.NotContains(t, results[i].Document, "_id")
			assert.Equal(t, []float32{float32(i)}, results[i].Document["content_vector"])
		}
	})

}
