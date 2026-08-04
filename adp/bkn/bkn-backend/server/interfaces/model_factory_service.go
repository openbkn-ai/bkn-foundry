// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"

	cond "bkn-backend/common/condition"
)

//go:generate mockgen -source ../interfaces/model_factory_service.go -destination ../interfaces/mock/mock_model_factory_service.go
type ModelFactoryService interface {
	GetDefaultModel(ctx context.Context) (*SmallModel, error)
	GetModelByID(ctx context.Context, modelID string) (*SmallModel, error)
	GetModelByName(ctx context.Context, modelName string) (*SmallModel, error)
	GetVector(ctx context.Context, model *SmallModel, words []string) ([]*cond.VectorResp, error)
}
