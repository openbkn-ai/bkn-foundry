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

// NewModelFactoryAccess creates a model factory access instance
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
	headers := common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}, "model_factory.get", 1)

	// Send a GET request to obtain the model
	respCode, result, err := mfa.httpClient.GetNoUnmarshal(ctx, httpUrl, nil, headers)
	logger.Debugf("GetModelByID finished, response code is [%d], result is [%s], error is [%v]", respCode, result, err)

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get model by id failed")
		logger.Errorf("Get model request failed: %v", err)
		return nil, fmt.Errorf("get model request failed: %w", err)
	}

	if respCode != http.StatusOK {
		if respCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", interfaces.ErrModelNotFound, modelID)
		}
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		logger.Errorf("Get model request failed with status code: %d, %s", respCode, result)
		return nil, fmt.Errorf("get model request failed with status code: %d, %s", respCode, result)
	}

	// Parse the response data
	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal model response failed")
		logger.Errorf("Unmarshal model response failed: %v", err)
		return nil, fmt.Errorf("unmarshal model response failed: %w", err)
	}

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return &smallModel, nil
}

func (mfa *modelFactoryAccess) GetVector(ctx context.Context, modelID string, words []string) ([]*interfaces.VectorResp, error) {
	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetVector")
	defer span.End()

	// Build the request URL.
	httpUrl := fmt.Sprintf("%s/small-model/embeddings", mfa.mfAPIUrl)

	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	// Set request headers.
	headers := common.MergeTraceHeadersForChildOperation(ctx, map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}, "model_factory.get", 1)

	// The caller supplies a normalized model ID, so the request must use model_id.
	// mf-model-api resolves model by name and model_id by ID; sending an ID in model
	// can hit an unrelated name cache entry without resolving the intended model.
	requestBody := map[string]any{
		"model":    "",
		"model_id": modelID,
		"input":    words,
	}

	// Send the POST request to obtain vectors.
	respCode, result, err := mfa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, requestBody)

	logger.Debugf("GetVector finished, batch_size=[%d], response code is [%d], %v", len(words), respCode, err)

	if err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http get vector failed")
		logger.Errorf("Get vector request failed: %v", err)
		return nil, fmt.Errorf("get vector request failed: %w", err)
	}

	if respCode != http.StatusOK {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Http status is not 200")
		logger.Errorf("Get vector request failed with status code: %d, %s", respCode, result)
		return nil, fmt.Errorf("get vector request failed with status code: %d, %s", respCode, result)
	}

	// Parse the response data.
	var response struct {
		Data []*interfaces.VectorResp `json:"data"`
	}

	if err := sonic.Unmarshal(result, &response); err != nil {
		oteltrace.AddHttpAttrs4Error(span, respCode, "InternalError", "Unmarshal vector response failed")
		logger.Errorf("Unmarshal vector response failed: %v", err)
		return nil, fmt.Errorf("unmarshal vector response failed: %w", err)
	}
	logger.Debugf("vectorized result length is [%d]", len(response.Data))

	oteltrace.AddHttpAttrs4Ok(span, respCode)
	return response.Data, nil
}
