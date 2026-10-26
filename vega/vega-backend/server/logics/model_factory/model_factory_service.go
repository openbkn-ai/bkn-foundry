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

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics"
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
		// 每个模型子批次的 index 从零开始；先校验并重排，再拼接输入顺序。
		ordered := make([]*interfaces.VectorResp, len(batch))
		for _, vector := range vectors {
			if vector == nil || vector.Index < 0 || vector.Index >= len(batch) {
				return nil, fmt.Errorf("vector response contains an invalid index or nil item")
			}
			if ordered[vector.Index] != nil {
				return nil, fmt.Errorf("vector response contains duplicate index %d", vector.Index)
			}
			ordered[vector.Index] = vector
		}
		result = append(result, ordered...)
	}
	return result, nil
}
