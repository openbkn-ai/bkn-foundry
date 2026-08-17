// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"errors"
)

var ErrModelNotFound = errors.New("model not found")

// ModelFactoryAccess 定义模型工厂相关的访问接口
//
//go:generate mockgen -source ../interfaces/model_factory_access.go -destination ../interfaces/mock/mock_model_factory_access.go
type ModelFactoryAccess interface {
	GetModelByID(ctx context.Context, modelID string) (*SmallModel, error)

	GetVector(ctx context.Context, modelID string, words []string) ([]*VectorResp, error)
}
