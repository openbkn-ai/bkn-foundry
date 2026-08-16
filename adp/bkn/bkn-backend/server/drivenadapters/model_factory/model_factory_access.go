// Copyright openbkn.ai
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
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"bkn-backend/common"
	cond "bkn-backend/common/condition"
	"bkn-backend/interfaces"
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

// NewModelFactoryAccess creates a model factory access instance.
func NewModelFactoryAccess(appSetting *common.AppSetting) interfaces.ModelFactoryAccess {
	mfAccessOnce.Do(func() {
		mfAccess = &modelFactoryAccess{
			appSetting:   appSetting,
			httpClient:   common.NewHTTPClient(),
			mfManagerUrl: appSetting.ModelFactoryManagerUrl,
			mfAPIUrl:     appSetting.ModelFactoryAPIUrl,
		}
	})

	return mfAccess
}

// GetDefaultModel retrieves the system default small model for a model_type from mf-model-manager.
// A nil result means that no default is configured and the API returned an empty object.
func (mfa *modelFactoryAccess) GetDefaultModel(ctx context.Context, modelType string) (*interfaces.SmallModel, error) {

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
	logger.Debugf("GetDefaultModel finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))
	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get default model failed")
		common.LogSafeError(ctx, "Get default model request failed", err)
		return nil, fmt.Errorf("get default model request failed: %w", err)
	}
	if respCode == http.StatusNotFound {
		// Treat an mf-model-manager without get_default as having no system default.
		logger.Warnf("get_default endpoint returned 404 (mf-model-manager not upgraded?), no system default available")
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		err := fmt.Errorf("get default model request failed with status code: %d", respCode)
		logger.Debugf("GetDefaultModel response: %s", common.SafeTextSummary("response", string(result)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		common.LogSafeError(ctx, "Get default model request failed", err)
		return nil, err
	}

	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal default model response failed")
		common.LogSafeError(ctx, "Unmarshal default model response failed", err)
		return nil, fmt.Errorf("unmarshal default model response failed: %w", err)
	}

	var model *interfaces.SmallModel
	if smallModel.ModelID != "" { // An empty object means no default is configured.
		model = &smallModel
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return model, nil
}

func (mfa *modelFactoryAccess) GetModelByID(ctx context.Context, modelID string) (*interfaces.SmallModel, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetModelByID")
	defer span.End()

	// Build the request URL.
	httpUrl := fmt.Sprintf("%s/small-model/get?model_id=%s", mfa.mfManagerUrl, modelID)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// Set request headers.
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	// Send the GET request to retrieve the model.
	respCode, result, err := mfa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("GetModelByID finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get model failed")
		common.LogSafeError(ctx, "Get model request failed", err)
		return nil, fmt.Errorf("get model request failed: %w", err)
	}

	if respCode == http.StatusNotFound {
		logger.Warnf("Get model request failed with status code: %d", respCode)
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		err := fmt.Errorf("get model request failed with status code: %d", respCode)
		logger.Debugf("GetModelByID response: %s", common.SafeTextSummary("response", string(result)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		common.LogSafeError(ctx, "Get model request failed", err)
		return nil, err
	}

	// Parse the response data.
	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal model response failed")
		common.LogSafeError(ctx, "Unmarshal model response failed", err)
		return nil, fmt.Errorf("unmarshal model response failed: %w", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &smallModel, nil
}

func (mfa *modelFactoryAccess) GetModelByName(ctx context.Context, modelName string) (*interfaces.SmallModel, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetModelByName")
	defer span.End()

	// Build the request URL.
	httpUrl := fmt.Sprintf("%s/small-model/get_by_name?model_name=%s", mfa.mfManagerUrl, modelName)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// Set request headers.
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	// Send the GET request to retrieve the model.
	respCode, result, err := mfa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("GetModelByName finished, response code is [%d], %s", respCode, common.SafeErrorSummary(err))

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get model by name failed")
		common.LogSafeError(ctx, "Get model request failed", err)
		return nil, fmt.Errorf("get model request failed: %w", err)
	}

	if respCode == http.StatusNotFound {
		logger.Warnf("Get model request failed with status code: %d", respCode)
		oteltrace.AddHttpAttrs4Ok(span, respCode)
		return nil, nil
	}
	if respCode != http.StatusOK {
		err := fmt.Errorf("get model request failed with status code: %d", respCode)
		logger.Debugf("GetModelByName response: %s", common.SafeTextSummary("response", string(result)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		common.LogSafeError(ctx, "Get model request failed", err)
		return nil, err
	}

	// Parse the response data.
	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal model response failed")
		common.LogSafeError(ctx, "Unmarshal model response failed", err)
		return nil, fmt.Errorf("unmarshal model response failed: %w", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &smallModel, nil
}

func (mfa *modelFactoryAccess) GetVector(ctx context.Context, modelID string, words []string) ([]*cond.VectorResp, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetVector")
	defer span.End()

	// Build the request URL.
	httpUrl := fmt.Sprintf("%s/small-model/embeddings", mfa.mfAPIUrl)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// Set request headers.
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	requestBody := map[string]any{"model": "", "model_id": modelID, "input": words}

	// Send the POST request to retrieve vectors.
	respCode, result, err := mfa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, requestBody)

	logger.Debugf("GetVector finished, batch_size=[%d], response code is [%d], %s", len(words), respCode, common.SafeErrorSummary(err))

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get vector failed")
		common.LogSafeError(ctx, "Get vector request failed", err)
		return nil, fmt.Errorf("get vector request failed: %w", err)
	}

	if respCode != 200 {
		err := fmt.Errorf("get vector request failed with status code: %d", respCode)
		logger.Debugf("GetVector response: %s", common.SafeTextSummary("response", string(result)))
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		common.LogSafeError(ctx, "Get vector request failed", err)
		return nil, err
	}

	// Parse the response data.
	var response struct {
		Data []*cond.VectorResp `json:"data"`
	}

	if err := sonic.Unmarshal(result, &response); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal vector response failed")
		common.LogSafeError(ctx, "Unmarshal vector response failed", err)
		return nil, fmt.Errorf("unmarshal vector response failed: %w", err)
	}
	logger.Debugf("vectorized result length is [%d]", len(response.Data))

	// Verify that the returned vector count matches the input text count.
	return response.Data, nil
}
