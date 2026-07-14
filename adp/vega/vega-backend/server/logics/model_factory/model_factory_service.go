// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package model_factory provides model factory business logic.
package model_factory

import (
	"context"
	"fmt"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
)

type modelFactoryService struct {
	mfa interfaces.ModelFactoryAccess
}

// NewModelFactoryService creates a new ModelFactoryService.
func NewModelFactoryService(appSetting *common.AppSetting) interfaces.ModelFactoryService {
	return &modelFactoryService{mfa: logics.MFA}
}

func (mfs *modelFactoryService) GetModelByName(ctx context.Context, modelName string) (*interfaces.SmallModel, error) {
	if mfs.mfa == nil {
		return nil, fmt.Errorf("model factory access is not initialized")
	}
	return mfs.mfa.GetModelByName(ctx, modelName)
}

func (mfs *modelFactoryService) GetVector(ctx context.Context, modelName string, words []string) ([]*interfaces.VectorResp, error) {
	if mfs.mfa == nil {
		return nil, fmt.Errorf("model factory access is not initialized")
	}
	return mfs.mfa.GetVector(ctx, modelName, words)
}
