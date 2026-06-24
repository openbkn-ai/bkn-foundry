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
	"time"

	"github.com/bytedance/sonic"
	"github.com/kweaver-ai/kweaver-go-lib/logger"
	"github.com/kweaver-ai/kweaver-go-lib/otel/otellog"
	"github.com/kweaver-ai/kweaver-go-lib/otel/oteltrace"
	"github.com/kweaver-ai/kweaver-go-lib/rest"
	"go.opentelemetry.io/otel/codes"

	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	"bkn-backend/interfaces"
)

var (
	mfAccessOnce sync.Once
	mfAccess     interfaces.ModelFactoryAccess
)

// defaultModelCacheTTL 系统默认模型缓存有效期：避免向量化热路径每次都打 mf-model-manager
const defaultModelCacheTTL = 60 * time.Second

// cachedDefault 缓存某 model_type 下的系统默认模型(含 nil，表示未配置)
type cachedDefault struct {
	model  *interfaces.SmallModel
	expiry time.Time
}

type modelFactoryAccess struct {
	appSetting   *common.AppSetting
	httpClient   rest.HTTPClient
	mfManagerUrl string
	mfAPIUrl     string
	knAccess     interfaces.KNAccess // 用于按 KN 读回建时锁定的 embedding 模型

	defaultCacheMu sync.RWMutex
	defaultCache   map[string]*cachedDefault
}

// NewModelFactoryAccess 创建模型工厂访问实例。knAccess 用于 GetModelByKNID 读回 KN 锁定模型。
func NewModelFactoryAccess(appSetting *common.AppSetting, knAccess interfaces.KNAccess) interfaces.ModelFactoryAccess {
	mfAccessOnce.Do(func() {
		mfAccess = &modelFactoryAccess{
			appSetting:   appSetting,
			httpClient:   common.NewHTTPClient(),
			mfManagerUrl: appSetting.ModelFactoryManagerUrl,
			mfAPIUrl:     appSetting.ModelFactoryAPIUrl,
			knAccess:     knAccess,
			defaultCache: make(map[string]*cachedDefault),
		}
	})

	return mfAccess
}

// GetModelByKNID 取 KN 建时锁定的 embedding 模型；KN 无锁定模型(老 KN)或 knID 为空时回退系统默认。
func (mfa *modelFactoryAccess) GetModelByKNID(ctx context.Context, knID string, branch string) (*interfaces.SmallModel, error) {
	if knID != "" && mfa.knAccess != nil {
		kn, err := mfa.knAccess.GetKNByID(ctx, knID, branch)
		if err != nil {
			logger.Warnf("GetKNByID for model resolution failed, knID=%s branch=%s: %v, fallback to default", knID, branch, err)
		} else if kn != nil && kn.EmbeddingModelID != "" {
			model, err := mfa.GetModelByID(ctx, kn.EmbeddingModelID)
			if err == nil && model != nil {
				return model, nil
			}
			logger.Warnf("GetModelByID(%s) for KN %s failed/empty, fallback to default", kn.EmbeddingModelID, knID)
		}
	}
	// 老 KN(未锁定模型) / knID 为空 / 解析失败 → 系统默认(兼容改造前行为)
	return mfa.GetDefaultModel(ctx)
}

func (mfa *modelFactoryAccess) GetDefaultModel(ctx context.Context) (*interfaces.SmallModel, error) {
	// DefaultSmallModelEnabled 仍作为部署级开关：是否启用 embedding(KNN/向量化)
	if !mfa.appSetting.ServerSetting.DefaultSmallModelEnabled {
		return nil, nil
	}
	// 优先取接口式系统默认(运行时可配，mf-model-manager 单一真相源，带 TTL 缓存)
	model, err := mfa.getDefaultModelFromAPI(ctx, interfaces.SMALL_MODEL_TYPE_EMBEDDING)
	if err != nil {
		logger.Errorf("Get default embedding model from mf-model-manager failed: %v", err)
		return nil, fmt.Errorf("get default embedding model failed: %w", err)
	}
	if model != nil {
		return model, nil
	}
	// 兜底：接口未配置默认时，回退到本地配置的默认模型名(兼容旧部署/迁移期)
	if defaultModelName := mfa.appSetting.ServerSetting.DefaultSmallModelName; defaultModelName != "" {
		return mfa.GetModelByName(ctx, defaultModelName)
	}
	return nil, nil
}

// getDefaultModelFromAPI 调 mf-model-manager 取某 model_type 下的系统默认小模型，带进程内 TTL 缓存。
// 返回 nil 表示未配置默认(接口返回空对象)。
func (mfa *modelFactoryAccess) getDefaultModelFromAPI(ctx context.Context, modelType string) (*interfaces.SmallModel, error) {
	// 命中缓存(含已缓存的 nil)
	mfa.defaultCacheMu.RLock()
	if c, ok := mfa.defaultCache[modelType]; ok && time.Now().Before(c.expiry) {
		mfa.defaultCacheMu.RUnlock()
		return c.model, nil
	}
	mfa.defaultCacheMu.RUnlock()

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetDefaultModel")
	defer span.End()

	httpUrl := fmt.Sprintf("%s/small-model/get_default?model_type=%s", mfa.mfManagerUrl, modelType)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	respCode, result, err := mfa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("get [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get default model failed")
		otellog.LogError(ctx, "Get default model request failed", err)
		return nil, fmt.Errorf("get default model request failed: %w", err)
	}
	if respCode == http.StatusNotFound {
		// 兼容 mf-model-manager 尚未升级(无 get_default 端点)：当作未配置默认，由 GetDefaultModel 回退 DefaultSmallModelName。
		// 缓存 nil 避免版本错配窗口期反复打 404。
		logger.Warnf("get_default endpoint returned 404 (mf-model-manager not upgraded?), fallback to configured default")
		mfa.defaultCacheMu.Lock()
		if mfa.defaultCache == nil {
			mfa.defaultCache = make(map[string]*cachedDefault)
		}
		mfa.defaultCache[modelType] = &cachedDefault{model: nil, expiry: time.Now().Add(defaultModelCacheTTL)}
		mfa.defaultCacheMu.Unlock()
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		err := fmt.Errorf("get default model request failed with status code: %d, %s", respCode, result)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		otellog.LogError(ctx, "Get default model request failed", err)
		return nil, err
	}

	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal default model response failed")
		otellog.LogError(ctx, "Unmarshal default model response failed", err)
		return nil, fmt.Errorf("unmarshal default model response failed: %w", err)
	}

	var model *interfaces.SmallModel
	if smallModel.ModelID != "" { // 空对象 {} 表示未配置默认
		model = &smallModel
	}

	mfa.defaultCacheMu.Lock()
	if mfa.defaultCache == nil {
		mfa.defaultCache = make(map[string]*cachedDefault)
	}
	mfa.defaultCache[modelType] = &cachedDefault{model: model, expiry: time.Now().Add(defaultModelCacheTTL)}
	mfa.defaultCacheMu.Unlock()

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return model, nil
}

func (mfa *modelFactoryAccess) GetModelByID(ctx context.Context, modelID string) (*interfaces.SmallModel, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetModelByID")
	defer span.End()

	// 构建请求URL
	httpUrl := fmt.Sprintf("%s/small-model/get?model_id=%s", mfa.mfManagerUrl, modelID)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// 设置请求头
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	// 发送GET请求获取模型
	respCode, result, err := mfa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("get [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get model failed")
		otellog.LogError(ctx, "Get model request failed", err)
		return nil, fmt.Errorf("get model request failed: %w", err)
	}

	if respCode == http.StatusNotFound {
		logger.Warnf("Get model request failed with status code: %d, %s", respCode, result)
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		err := fmt.Errorf("get model request failed with status code: %d, %s", respCode, result)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		otellog.LogError(ctx, "Get model request failed", err)
		return nil, err
	}

	// 解析响应数据
	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal model response failed")
		otellog.LogError(ctx, "Unmarshal model response failed", err)
		return nil, fmt.Errorf("unmarshal model response failed: %w", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &smallModel, nil
}

func (mfa *modelFactoryAccess) GetModelByName(ctx context.Context, modelName string) (*interfaces.SmallModel, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetModelByName")
	defer span.End()

	// 构建请求URL
	httpUrl := fmt.Sprintf("%s/small-model/get_by_name?model_name=%s", mfa.mfManagerUrl, modelName)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// 设置请求头
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	// 发送GET请求获取模型
	respCode, result, err := mfa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("get [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get model by name failed")
		otellog.LogError(ctx, "Get model request failed", err)
		return nil, fmt.Errorf("get model request failed: %w", err)
	}

	if respCode == http.StatusNotFound {
		logger.Warnf("Get model request failed with status code: %d, %s", respCode, result)
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		err := fmt.Errorf("get model request failed with status code: %d, %s", respCode, result)
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		otellog.LogError(ctx, "Get model request failed", err)
		return nil, err
	}

	// 解析响应数据
	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal model response failed")
		otellog.LogError(ctx, "Unmarshal model response failed", err)
		return nil, fmt.Errorf("unmarshal model response failed: %w", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &smallModel, nil
}

func (mfa *modelFactoryAccess) GetVector(ctx context.Context,
	model *interfaces.SmallModel, words []string) ([]*cond.VectorResp, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetVector")
	defer span.End()

	if model == nil {
		span.SetStatus(codes.Error, "Model is nil")
		return []*cond.VectorResp{}, fmt.Errorf("model is nil")
	}
	if len(words) == 0 {
		span.SetStatus(codes.Ok, "")
		return []*cond.VectorResp{}, nil
	}

	// 构建请求URL
	httpUrl := fmt.Sprintf("%s/small-model/embeddings", mfa.mfAPIUrl)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// 设置请求头
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	modelID := model.ModelID
	maxTokens := model.MaxTokens
	batchSize := model.BatchSize

	allVectorResps := make([]*cond.VectorResp, 0, len(words))
	for i := 0; i < len(words); i += batchSize {
		end := i + batchSize
		if end > len(words) {
			end = len(words)
		}
		currentWords := words[i:end]
		for j := 0; j < len(currentWords); j++ {
			// 计算utf8字符长度
			runes := []rune(currentWords[j])
			if len(runes) > maxTokens {
				currentWords[j] = string(runes[:maxTokens])
			}
		}

		// 构建请求体
		requestBody := map[string]any{
			"model":    "",
			"model_id": modelID,
			"input":    currentWords,
		}

		// 发送POST请求获取向量
		respCode, result, err := mfa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, requestBody)

		// 打印日志
		modelInfo, _ := sonic.Marshal(model)
		logger.Debugf("post [%s] finished, small model info: [%s], response code is [%d], error is [%v]",
			httpUrl, modelInfo, respCode, err)

		if err != nil {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get vector failed")
			otellog.LogError(ctx, "Get vector request failed", err)
			return nil, fmt.Errorf("get vector request failed: %w", err)
		}

		if respCode != 200 {
			err := fmt.Errorf("get vector request failed with status code: %d, %s", respCode, result)
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
			otellog.LogError(ctx, "Get vector request failed", err)
			return nil, err
		}

		// 解析响应数据
		var response struct {
			Data []*cond.VectorResp `json:"data"`
		}

		if err := sonic.Unmarshal(result, &response); err != nil {
			oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal vector response failed")
			otellog.LogError(ctx, "Unmarshal vector response failed", err)
			return nil, fmt.Errorf("unmarshal vector response failed: %w", err)
		}
		logger.Debugf("vectorized result length is [%d]", len(response.Data))

		// 检查返回的向量数量是否与输入文本数量一致
		if len(response.Data) != len(currentWords) {
			err := fmt.Errorf("vector count mismatch: expected %d, got %d", len(currentWords), len(response.Data))
			otellog.LogError(ctx, "Vector count mismatch", err)
			return nil, err
		}

		allVectorResps = append(allVectorResps, response.Data...)
	}

	span.SetStatus(codes.Ok, "")
	return allVectorResps, nil
}
