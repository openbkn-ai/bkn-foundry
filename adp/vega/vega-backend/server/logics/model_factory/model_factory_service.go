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
	"sync"

	"vega-backend/common"
	"vega-backend/interfaces"
	"vega-backend/logics"
)

var (
	mfServiceOnce sync.Once
	mfService     interfaces.ModelFactoryService
)

type modelFactoryService struct {
	appSetting *common.AppSetting
	mfa        interfaces.ModelFactoryAccess
}

// NewModelFactoryService creates a new ModelFactoryService.
func NewModelFactoryService(appSetting *common.AppSetting) interfaces.ModelFactoryService {
	mfServiceOnce.Do(func() {
		mfService = &modelFactoryService{
			appSetting: appSetting,
			mfa:        logics.MFA,
		}
	})
	return mfService
}

func (mfs *modelFactoryService) GetModelByID(ctx context.Context, modelID string) (*interfaces.SmallModel, error) {
	return mfs.mfa.GetModelByID(ctx, modelID)
}

func (mfs *modelFactoryService) GetVector(ctx context.Context, m *interfaces.SmallModel, w []string) ([]*interfaces.VectorResp, error) {
	if m == nil || m.ModelID == "" {
		return nil, fmt.Errorf("model is nil or model id is empty")
	}
	if len(w) == 0 {
		return []*interfaces.VectorResp{}, nil
	}
	batchSize := m.BatchSize
	if batchSize <= 0 {
		batchSize = len(w)
	}
	result := make([]*interfaces.VectorResp, 0, len(w))
	for start := 0; start < len(w); start += batchSize {
		end := start + batchSize
		if end > len(w) {
			end = len(w)
		}
		batch := append([]string(nil), w[start:end]...)
		for i, word := range batch {
			if m.MaxTokens > 0 && len([]rune(word)) > m.MaxTokens {
				batch[i] = string([]rune(word)[:m.MaxTokens])
			}
		}
		vectors, err := mfs.mfa.GetVector(ctx, m.ModelID, batch)
		if err != nil {
			return nil, err
		}
		if len(vectors) != len(batch) {
			return nil, fmt.Errorf("vector count mismatch: expected %d, got %d", len(batch), len(vectors))
		}
		result = append(result, vectors...)
	}
	return result, nil
}
