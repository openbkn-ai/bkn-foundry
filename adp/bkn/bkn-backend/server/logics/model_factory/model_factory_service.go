// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package model_factory

import (
	"context"
	"fmt"
	"sync"

	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	"bkn-backend/interfaces"
)

var (
	mfServiceOnce sync.Once
	mfService     interfaces.ModelFactoryService
)

type modelFactoryService struct {
	appSetting *common.AppSetting
	mfa        interfaces.ModelFactoryAccess
}

func NewModelFactoryService(appSetting *common.AppSetting, mfa interfaces.ModelFactoryAccess) interfaces.ModelFactoryService {
	mfServiceOnce.Do(func() {
		mfService = &modelFactoryService{
			appSetting: appSetting,
			mfa:        mfa,
		}
	})
	return mfService
}

func (mfs *modelFactoryService) GetDefaultModel(ctx context.Context) (*interfaces.SmallModel, error) {
	if !mfs.appSetting.ServerSetting.DefaultSmallModelEnabled {
		return nil, nil
	}
	if name := mfs.appSetting.ServerSetting.DefaultSmallModelName; name != "" {
		return mfs.mfa.GetModelByName(ctx, name)
	}
	return mfs.mfa.GetDefaultModel(ctx, interfaces.SMALL_MODEL_TYPE_EMBEDDING)
}

func (mfs *modelFactoryService) GetModelByID(ctx context.Context, id string) (*interfaces.SmallModel, error) {
	return mfs.mfa.GetModelByID(ctx, id)
}

func (mfs *modelFactoryService) GetModelByName(ctx context.Context, name string) (*interfaces.SmallModel, error) {
	return mfs.mfa.GetModelByName(ctx, name)
}

func (mfs *modelFactoryService) GetVector(ctx context.Context, m *interfaces.SmallModel, w []string) ([]*cond.VectorResp, error) {
	if m == nil || m.ModelID == "" {
		return nil, fmt.Errorf("model is nil or model id is empty")
	}
	if len(w) == 0 {
		return []*cond.VectorResp{}, nil
	}
	batchSize := m.BatchSize
	if batchSize <= 0 {
		batchSize = len(w)
	}
	result := make([]*cond.VectorResp, 0, len(w))
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
