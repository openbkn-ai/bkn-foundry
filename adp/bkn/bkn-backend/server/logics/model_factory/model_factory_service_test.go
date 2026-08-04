// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package model_factory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	"bkn-backend/interfaces"
	mock_interfaces "bkn-backend/interfaces/mock"
)

func TestGetDefaultModelPrefersConfiguredModel(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	access.EXPECT().GetModelByName(gomock.Any(), "configured").Return(&interfaces.SmallModel{ModelName: "configured"}, nil)
	service := &modelFactoryService{appSetting: &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true, DefaultSmallModelName: "configured"}}, mfa: access}
	model, err := service.GetDefaultModel(context.Background())
	require.NoError(t, err)
	require.Equal(t, "configured", model.ModelName)
}

func TestGetDefaultModelFallsBackToSystemDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	access.EXPECT().GetDefaultModel(gomock.Any(), interfaces.SMALL_MODEL_TYPE_EMBEDDING).Return(&interfaces.SmallModel{ModelName: "system"}, nil)
	service := &modelFactoryService{appSetting: &common.AppSetting{ServerSetting: common.ServerSetting{DefaultSmallModelEnabled: true}}, mfa: access}
	model, err := service.GetDefaultModel(context.Background())
	require.NoError(t, err)
	require.Equal(t, "system", model.ModelName)
}

func TestGetVectorBatchesAndTruncatesWithoutMutatingInput(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	service := &modelFactoryService{mfa: access}
	model := &interfaces.SmallModel{ModelID: "model-id", BatchSize: 2, MaxTokens: 3}
	words := []string{"abcd", "中文测试", "xyz"}

	access.EXPECT().GetVector(gomock.Any(), "model-id", []string{"abc", "中文测"}).Return([]*cond.VectorResp{{}, {}}, nil)
	access.EXPECT().GetVector(gomock.Any(), "model-id", []string{"xyz"}).Return([]*cond.VectorResp{{}}, nil)

	vectors, err := service.GetVector(context.Background(), model, words)
	require.NoError(t, err)
	require.Len(t, vectors, 3)
	require.Equal(t, []string{"abcd", "中文测试", "xyz"}, words)
}

func TestGetVectorRejectsMismatchedResponseCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	service := &modelFactoryService{mfa: access}

	access.EXPECT().GetVector(gomock.Any(), "model-id", []string{"one", "two"}).Return([]*cond.VectorResp{{}}, nil)
	vectors, err := service.GetVector(context.Background(), &interfaces.SmallModel{ModelID: "model-id", BatchSize: 2}, []string{"one", "two"})
	require.Nil(t, vectors)
	require.EqualError(t, err, "vector count mismatch: expected 2, got 1")
}

func TestGetVectorUsesWholeInputWhenBatchSizeIsNotPositive(t *testing.T) {
	ctrl := gomock.NewController(t)
	access := mock_interfaces.NewMockModelFactoryAccess(ctrl)
	service := &modelFactoryService{mfa: access}
	words := []string{"one", "two"}

	access.EXPECT().GetVector(gomock.Any(), "model-id", words).Return([]*cond.VectorResp{{}, {}}, nil)
	vectors, err := service.GetVector(context.Background(), &interfaces.SmallModel{ModelID: "model-id"}, words)
	require.NoError(t, err)
	require.Len(t, vectors, 2)

	_, err = service.GetVector(context.Background(), nil, words)
	require.EqualError(t, err, "model is nil or model id is empty")
	_, err = service.GetVector(context.Background(), &interfaces.SmallModel{}, words)
	require.EqualError(t, err, "model is nil or model id is empty")
	_, err = service.GetVector(context.Background(), &interfaces.SmallModel{ModelID: "model-id"}, nil)
	require.NoError(t, err)

}
