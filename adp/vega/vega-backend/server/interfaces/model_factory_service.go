// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// ModelFactoryService defines model factory business logic.
//
//go:generate mockgen -source ../interfaces/model_factory_service.go -destination ../interfaces/mock/mock_model_factory_service.go
type ModelFactoryService interface {
	GetModelByName(ctx context.Context, modelName string) (*SmallModel, error)

	GetVector(ctx context.Context, modelName string, words []string) ([]*VectorResp, error)
}
