// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/interfaces"
	vmock "vega-backend/interfaces/mock"
)

func TestEmbeddingPipelineEnrich(t *testing.T) {
	t.Run("delegates document batching to model factory and appends vectors", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		pipeline := &embeddingPipeline{mfs: mfs}
		documents := map[string]map[string]any{
			"doc-1": {"content": "first"},
			"doc-2": {"content": "second"},
		}
		model := &interfaces.SmallModel{ModelID: "model-1"}
		config := map[string]*interfaces.SmallModel{"content": model}

		mfs.EXPECT().GetVector(gomock.Any(), model, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *interfaces.SmallModel, words []string) ([]*interfaces.VectorResp, error) {
				vectors := make([]*interfaces.VectorResp, len(words))
				for i, word := range words {
					if word == "first" {
						vectors[i] = &interfaces.VectorResp{Vector: []float32{1}}
					} else {
						vectors[i] = &interfaces.VectorResp{Vector: []float32{2}}
					}
				}
				return vectors, nil
			})

		require.NoError(t, pipeline.enrich(context.Background(), documents, config))
		assert.Equal(t, []float32{1}, documents["doc-1"]["content_vector"])
		assert.Equal(t, []float32{2}, documents["doc-2"]["content_vector"])
	})

	t.Run("fails the whole batch after bounded retries", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mfs := vmock.NewMockModelFactoryService(ctrl)
		pipeline := &embeddingPipeline{mfs: mfs, sleep: func(time.Duration) {}}
		documents := map[string]map[string]any{"doc-1": {"content": "first"}}
		model := &interfaces.SmallModel{ModelID: "model-1"}
		config := map[string]*interfaces.SmallModel{"content": model}

		mfs.EXPECT().GetVector(gomock.Any(), model, []string{"first"}).Return(nil, errors.New("model unavailable")).Times(embeddingPipelineMaxAttempts)

		err := pipeline.enrich(context.Background(), documents, config)
		require.ErrorContains(t, err, "model unavailable")
		assert.NotContains(t, documents["doc-1"], "content_vector")
	})
}
