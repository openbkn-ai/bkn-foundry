// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
)

// ModelFactoryAccess defines the access interfaces related to the model factory
//
//go:generate mockgen -source ../interfaces/model_factory_access.go -destination ../interfaces/mock/mock_model_factory_access.go
type ModelFactoryAccess interface {
	GetModelByName(ctx context.Context, modelName string) (*SmallModel, error)

	GetVector(ctx context.Context, modelName string, words []string) ([]*VectorResp, error)
}
