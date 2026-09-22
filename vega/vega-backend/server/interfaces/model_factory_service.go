// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// ModelFactoryService defines model factory business logic.
//
//go:generate mockgen -source ../interfaces/model_factory_service.go -destination ../interfaces/mock/mock_model_factory_service.go
type ModelFactoryService interface {
	GetModelByID(ctx context.Context, modelID string) (*SmallModel, error)

	GetVector(ctx context.Context, model *SmallModel, words []string) ([]*VectorResp, error)
}
