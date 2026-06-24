// Copyright 2026 openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package model_factory

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/bytedance/sonic"
	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/kweaver-ai/kweaver-go-lib/otel/oteltrace"
	"github.com/kweaver-ai/kweaver-go-lib/rest"

	"vega-backend/common"
	"vega-backend/interfaces"
)

var (
	mfAccessOnce sync.Once
	mfAccess     interfaces.ModelFactoryAccess
)

type modelFactoryAccess struct {
	appSetting   *common.AppSetting
	httpClient   rest.HTTPClient
	mfManagerUrl string
	mfAPIUrl     string
}

// NewModelFactoryAccess 创建模型工厂访问实例
func NewModelFactoryAccess(appSetting *common.AppSetting) interfaces.ModelFactoryAccess {
	mfAccessOnce.Do(func() {
		mfAccess = &modelFactoryAccess{
			appSetting:   appSetting,
			httpClient:   common.NewHTTPClient(),
			mfManagerUrl: appSetting.MfModelManagerUrl,
			mfAPIUrl:     appSetting.MfModelApiUrl,
		}
	})

	return mfAccess
}

func (mfa *modelFactoryAccess) GetModelByName(ctx context.Context, modelName string) (*interfaces.SmallModel, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetModelByName")
	defer span.End()

	httpUrl := fmt.Sprintf("%s/api/private/mf-model-manager/v1/small-model/get_by_name?model_name=%s", mfa.mfManagerUrl, modelName)
	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// 发送GET请求获取模型
	respCode, result, err := mfa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("get [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		logger.Errorf("Get model request failed: %v", err)
		return nil, fmt.Errorf("get model request failed: %w", err)
	}

	if respCode == http.StatusNotFound {
		logger.Warnf("Get model request failed with status code: %d, %s", respCode, result)
		return nil, fmt.Errorf("model not found: %s", modelName)
	}
	if respCode != http.StatusOK {
		logger.Errorf("Get model request failed with status code: %d, %s", respCode, result)
		return nil, fmt.Errorf("get model request failed with status code: %d, %s", respCode, result)
	}

	// 解析响应数据
	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		logger.Errorf("Unmarshal model response failed: %v", err)
		return nil, fmt.Errorf("unmarshal model response failed: %w", err)
	}

	return &smallModel, nil
}

func (mfa *modelFactoryAccess) GetVector(ctx context.Context, modelID string, words []string) ([]*interfaces.VectorResp, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetVector")
	defer span.End()

	if len(words) == 0 {
		return []*interfaces.VectorResp{}, nil
	}

	if modelID == "" {
		return nil, fmt.Errorf("model id cannot be empty")
	}

	httpUrl := fmt.Sprintf("%s/api/private/mf-model-api/v1/small-model/embeddings", mfa.mfAPIUrl)
	headers := map[string]string{
		"Content-Type": "application/json",
	}

	// buildTask.EmbeddingModel 存的是模型 **id**（已归一到 id），必须发 model_id 字段。
	// mf-model-api 的 embeddings 解析：model 字段只按 model_name 查、model_id 字段才按 id 查。
	// 之前把 id 塞进 model（名字）字段 → 按 name 查不到 → ModelFactory.ExternalSmallModel.Used.NameNotExist
	// （偶尔“成功”只是撞上 model_id 查询写下的同名缓存 key，并非真解析成功）。
	requestBody := map[string]any{
		"model":    "",
		"model_id": modelID,
		"input":    words,
	}

	respCode, result, err := mfa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, requestBody)

	if err != nil {
		logger.Errorf("Get vector request failed: %v", err)
		return nil, fmt.Errorf("get vector request failed: %w", err)
	}

	if respCode != http.StatusOK {
		logger.Errorf("Get vector request failed with status code: %d, %s", respCode, result)
		return nil, fmt.Errorf("get vector request failed with status code: %d, %s", respCode, result)
	}

	var response struct {
		Data []*interfaces.VectorResp `json:"data"`
	}

	if err := sonic.Unmarshal(result, &response); err != nil {
		logger.Errorf("Unmarshal vector response failed: %v", err)
		return nil, fmt.Errorf("unmarshal vector response failed: %w", err)
	}

	return response.Data, nil
}
