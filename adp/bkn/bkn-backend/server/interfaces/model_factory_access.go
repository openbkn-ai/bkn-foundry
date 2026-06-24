// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"

	cond "bkn-backend/common/condition"
)

const (
	SMALL_MODEL_TYPE_EMBEDDING = "embedding"
)

type SmallModel struct {
	ModelID      string `json:"model_id"`
	ModelName    string `json:"model_name"`
	ModelType    string `json:"model_type"`
	EmbeddingDim int    `json:"embedding_dim"`
	BatchSize    int    `json:"batch_size"`
	MaxTokens    int    `json:"max_tokens"`
}

// ModelFactoryAccess 定义模型工厂相关的访问接口
//
//go:generate mockgen -source ../interfaces/model_factory_access.go -destination ../interfaces/mock/mock_model_factory_access.go
type ModelFactoryAccess interface {
	GetDefaultModel(ctx context.Context) (*SmallModel, error)

	// GetModelByKNID 取某 KN 建时锁定的 embedding 模型；KN 无锁定模型(老 KN)或 knID 为空时回退系统默认。
	// 写入与 KNN 查询统一经此读回，保证建模型==查模型。
	GetModelByKNID(ctx context.Context, knID string, branch string) (*SmallModel, error)

	GetModelByID(ctx context.Context, modelID string) (*SmallModel, error)
	GetModelByName(ctx context.Context, modelName string) (*SmallModel, error)

	GetVector(ctx context.Context, model *SmallModel, words []string) ([]*cond.VectorResp, error)
}
