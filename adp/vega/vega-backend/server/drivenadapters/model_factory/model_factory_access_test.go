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
	"github.com/openbkn-ai/bkn-comm-go/rest"
	rmock "github.com/openbkn-ai/bkn-comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"vega-backend/common"
	"vega-backend/interfaces"
)

func newTestModelFactoryAccess(appSetting *common.AppSetting, httpClient rest.HTTPClient) *modelFactoryAccess {
	return &modelFactoryAccess{
		appSetting:   appSetting,
		httpClient:   httpClient,
		mfManagerUrl: appSetting.MfModelManagerUrl,
		mfAPIUrl:     appSetting.MfModelApiUrl,
	}
}

func TestNewModelFactoryAccess(t *testing.T) {
	t.Run("returns singleton access", func(t *testing.T) {
		appSetting := &common.AppSetting{
			MfModelManagerUrl: "http://test-mf-manager",
			MfModelApiUrl:     "http://test-mf-api",
		}

		access1 := NewModelFactoryAccess(appSetting)
		access2 := NewModelFactoryAccess(appSetting)

		require.NotNil(t, access1)
		assert.Equal(t, access1, access2)
	})
}

func TestModelFactoryAccessGetModelByName(t *testing.T) {
	ctx := context.Background()
	modelName := "test-model"

	setup := func(t *testing.T) (*modelFactoryAccess, *rmock.MockHTTPClient) {
		t.Helper()

		mockCtrl := gomock.NewController(t)
		t.Cleanup(mockCtrl.Finish)

		appSetting := &common.AppSetting{
			MfModelManagerUrl: "http://test-mf-manager",
			MfModelApiUrl:     "http://test-mf-api",
		}
		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
		return newTestModelFactoryAccess(appSetting, mockHTTPClient), mockHTTPClient
	}

	t.Run("success getting model by name", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		model := interfaces.SmallModel{
			ModelID:   "model1",
			ModelName: modelName,
		}
		respData, err := sonic.Marshal(model)
		require.NoError(t, err)

		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusOK, respData, nil)

		result, err := mfa.GetModelByName(ctx, modelName)

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, modelName, result.ModelName)
	})

	t.Run("model not found", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusNotFound, []byte(""), nil)

		result, err := mfa.GetModelByName(ctx, modelName)

		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("HTTP request error", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(0, []byte(""), errors.New("network error"))

		result, err := mfa.GetModelByName(ctx, modelName)

		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("HTTP status not OK and not NotFound", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusInternalServerError, []byte("internal error"), nil)

		result, err := mfa.GetModelByName(ctx, modelName)

		require.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("unmarshal response failed", func(t *testing.T) {
		mfa, mockHTTPClient := setup(t)
		mockHTTPClient.EXPECT().
			GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(http.StatusOK, []byte("invalid json"), nil)

		result, err := mfa.GetModelByName(ctx, modelName)

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
			MfModelManagerUrl: "http://test-mf-manager",
			MfModelApiUrl:     "http://test-mf-api",
		}
		mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
		return newTestModelFactoryAccess(appSetting, mockHTTPClient), mockHTTPClient
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
			Return(http.StatusOK, respData, nil)

		result, err := mfa.GetVector(ctx, model.ModelID, words)

		require.NoError(t, err)
		require.Len(t, result, 3)
	})

	t.Run("empty model name", func(t *testing.T) {
		mfa, _ := setup(t)

		result, err := mfa.GetVector(ctx, "", words)

		require.Error(t, err)
		assert.Empty(t, result)
	})

	t.Run("empty words", func(t *testing.T) {
		mfa, _ := setup(t)

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
