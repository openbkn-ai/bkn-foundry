// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package model_factory

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

func newTestModelFactoryAccess(appSetting *common.AppSetting, httpClient rest.HTTPClient) *modelFactoryAccess {
	return &modelFactoryAccess{
		appSetting:   appSetting,
		httpClient:   httpClient,
		mfManagerUrl: appSetting.ModelFactoryManagerUrl,
		mfAPIUrl:     appSetting.ModelFactoryAPIUrl,
	}
}

func TestModelFactoryAccessGetModelByID(t *testing.T) {
	ctx := context.Background()
	modelID := "model-1"

	setup := func(t *testing.T) (*modelFactoryAccess, *rmock.MockHTTPClient) {
		t.Helper()

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		appSetting := &common.AppSetting{
			ModelFactoryManagerUrl: "http://test-mf-manager",
			ModelFactoryAPIUrl:     "http://test-mf-api",
		}
		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
		return newTestModelFactoryAccess(appSetting, mockHTTPClient), mockHTTPClient
	}

	t.Run("success getting model by ID", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		model := interfaces.SmallModel{
			ModelID:   modelID,
			ModelName: "test-model",
		}
		respData, err := sonic.Marshal(model)
		require.NoError(t, err)

		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), "http://test-mf-manager/small-model/get?model_id=model-1", gomock.Any(), gomock.Any()).
			Return(http.StatusOK, respData, nil)

		result, err := mfa.GetModelByID(ctx, modelID)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, modelID, result.ModelID)
	})

	t.Run("model not found", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusNotFound, []byte(""), nil)

		result, err := mfa.GetModelByID(ctx, modelID)

		require.Error(t, err)
		assert.ErrorIs(t, err, interfaces.ErrModelNotFound)
		assert.Nil(t, result)
	})

	t.Run("HTTP request error", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, []byte(""), errors.New("network error"))

		result, err := mfa.GetModelByID(ctx, modelID)

		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("HTTP status not OK and not NotFound", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusInternalServerError, []byte("internal error"), nil)

		result, err := mfa.GetModelByID(ctx, modelID)

		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("unmarshal response failed", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusOK, []byte("invalid json"), nil)

		result, err := mfa.GetModelByID(ctx, modelID)

		require.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestModelFactoryAccessGetVector(t *testing.T) {
	ctx := context.Background()
	model := &interfaces.SmallModel{
		ModelID:   "model1",
		BatchSize: 10,
		MaxTokens: 100,
	}
	words := []string{"word1", "word2", "word3"}

	setup := func(t *testing.T) (*modelFactoryAccess, *rmock.MockHTTPClient) {
		t.Helper()

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		appSetting := &common.AppSetting{
			ModelFactoryManagerUrl: "http://test-mf-manager",
			ModelFactoryAPIUrl:     "http://test-mf-api",
		}
		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
		return newTestModelFactoryAccess(appSetting, mockHTTPClient), mockHTTPClient
	}

	for _, tc := range []struct {
		name, data  string
		wantIndices []int
		wantError   bool
	}{
		{"missing indices preserve positions", `[{"embedding":[1]},{"embedding":[2]},{"embedding":[3]}]`, []int{0, 1, 2}, false},
		{"explicit indices are preserved", `[{"index":2,"embedding":[3]},{"index":0,"embedding":[1]},{"index":1,"embedding":[2]}]`, []int{2, 0, 1}, false},
		{"mixed indices are rejected", `[{"embedding":[1]},{"index":1,"embedding":[2]},{"index":2,"embedding":[3]}]`, nil, true},
		{"nil response item is rejected", `[null,{"index":1,"embedding":[2]}]`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mfa, client := setup(t)
			client.EXPECT().PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(http.StatusOK, []byte(`{"data":`+tc.data+`}`), nil)
			vectors, err := mfa.GetVector(ctx, model.ModelID, words)
			if tc.wantError {
				require.Error(t, err)
				assert.Nil(t, vectors)
				return
			}
			require.NoError(t, err)
			require.Len(t, vectors, len(tc.wantIndices))
			for i, index := range tc.wantIndices {
				assert.Equal(t, index, vectors[i].Index)
				assert.Equal(t, []float32{float32(index + 1)}, vectors[i].Vector)
			}
		})
	}

	t.Run("success getting vectors", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		response := map[string]any{
			"data": []*interfaces.VectorResp{
				{Vector: []float32{0.1, 0.2}},
				{Vector: []float32{0.3, 0.4}},
				{Vector: []float32{0.5, 0.6}},
			},
		}
		respData, err := sonic.Marshal(response)
		require.NoError(t, err)

		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, rawURL string, _ map[string]string, body map[string]any) (int, []byte, error) {
				assert.Equal(t, "http://test-mf-api/small-model/embeddings", rawURL)
				assert.Equal(t, model.ModelID, body["model_id"])
				assert.Equal(t, "", body["model"])
				assert.Equal(t, words, body["input"])
				return http.StatusOK, respData, nil
			})

		result, err := mfa.GetVector(ctx, model.ModelID, words)

		require.NoError(t, err)
		require.Len(t, result, 3)
	})

	t.Run("forwards an empty model ID to model factory", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		respData, err := sonic.Marshal(map[string]any{"data": []*interfaces.VectorResp{}})
		require.NoError(t, err)
		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, body map[string]any) (int, []byte, error) {
				assert.Equal(t, "", body["model_id"])
				return http.StatusOK, respData, nil
			})

		result, err := mfa.GetVector(ctx, "", words)

		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("forwards empty input to model factory", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		respData, err := sonic.Marshal(map[string]any{"data": []*interfaces.VectorResp{}})
		require.NoError(t, err)
		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ map[string]string, body map[string]any) (int, []byte, error) {
				assert.Equal(t, []string{}, body["input"])
				return http.StatusOK, respData, nil
			})

		result, err := mfa.GetVector(ctx, model.ModelID, []string{})

		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("HTTP request error", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, []byte(""), errors.New("network error"))

		result, err := mfa.GetVector(ctx, model.ModelID, words)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "get vector request failed")
	})

	t.Run("HTTP status not OK", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusInternalServerError, []byte("internal error"), nil)

		result, err := mfa.GetVector(ctx, model.ModelID, words)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "status code: 500")
	})

	t.Run("unmarshal response failed", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusOK, []byte("invalid json"), nil)

		result, err := mfa.GetVector(ctx, model.ModelID, words)

		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "unmarshal vector response failed")
	})
}
