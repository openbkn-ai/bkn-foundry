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

	"ontology-query/common"
	cond "ontology-query/common/condition"
	"ontology-query/interfaces"
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

// GetVector retrieves corresponding vector arrays for input strings.
// Parameters:
// - ctx: context object.
// - texts: input string array.
//
// Returns:
// - [][]float32: vector array with the same length, one vector per input string.
// - error: error information.
func (mfa *modelFactoryAccess) GetVector(ctx context.Context, model *interfaces.SmallModel,
	words []string) ([]cond.VectorResp, error) {

	ctx, span := oteltrace.StartNamedClientSpan(ctx, "GetVector")
	defer span.End()

	if model == nil {
		return []cond.VectorResp{}, fmt.Errorf("model is nil")
	}
	if len(words) == 0 {
		return []cond.VectorResp{}, nil
	}

	// Build the request URL.
	httpUrl := fmt.Sprintf("%s/small-model/embeddings", mfa.mfAPIUrl)

	// Set request headers.
	accountInfo := interfaces.AccountInfo{}
	if ctx.Value(interfaces.ACCOUNT_INFO_KEY) != nil {
		accountInfo = ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	}
	headers := map[string]string{
		"Content-Type":                      "application/json",
		interfaces.HTTP_HEADER_ACCOUNT_ID:   accountInfo.ID,
		interfaces.HTTP_HEADER_ACCOUNT_TYPE: accountInfo.Type,
	}

	modelID := model.ModelID
	maxTokens := model.MaxTokens
	batchSize := model.BatchSize

	allVectorResps := make([]cond.VectorResp, 0, len(words))
	for i := 0; i < len(words); i += batchSize {
		end := i + batchSize
		if end > len(words) {
			end = len(words)
		}
		currentWords := words[i:end]
		for j := 0; j < len(currentWords); j++ {
			// Calculate the UTF-8 character length.
			runes := []rune(currentWords[j])
			if len(runes) > maxTokens {
				currentWords[j] = string(runes[:maxTokens])
			}
		}

		// Build the request body.
		requestBody := map[string]interface{}{
			"model":    "",
			"model_id": modelID,
			"input":    currentWords,
		}

		// Send the POST request to retrieve vectors.
		respCode, result, err := mfa.httpClient.PostNoUnmarshal(ctx, httpUrl, headers, requestBody)
		logger.Debugf("post [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

		if err != nil {
			logger.Errorf("Get vector request failed: %v", err)
			return nil, fmt.Errorf("get vector request failed: %w", err)
		}

		if respCode != 200 {
			logger.Errorf("Get vector request failed with status code: %d, %s", respCode, result)
			return nil, fmt.Errorf("get vector request failed with status code: %d, %s", respCode, result)
		}

		// Parse the response data.
		var response struct {
			Data []cond.VectorResp `json:"data"`
		}

		if err := sonic.Unmarshal(result, &response); err != nil {
			logger.Errorf("Unmarshal vector response failed: %v", err)
			return nil, fmt.Errorf("unmarshal vector response failed: %w", err)
		}

		// Verify that the returned vector count matches the input text count.
		if len(response.Data) != len(currentWords) {
			logger.Errorf("Vector count mismatch: expected %d, got %d", len(currentWords), len(response.Data))
			return nil, fmt.Errorf("vector count mismatch: expected %d, got %d", len(currentWords), len(response.Data))
		}

		allVectorResps = append(allVectorResps, response.Data...)
	}

	return allVectorResps, nil
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
	logger.Debugf("get [%s] finished, response code is [%d], result is [%s], error is [%v]", httpUrl, respCode, result, err)

	if err != nil {
		logger.Errorf("Get model request failed: %v", err)
		return nil, fmt.Errorf("get model request failed: %w", err)
	}

	if respCode == http.StatusNotFound {
		logger.Warnf("Get model request failed with status code: %d, %s", respCode, result)
		return nil, nil
	}
	if respCode != http.StatusOK {
		logger.Errorf("Get model request failed with status code: %d, %s", respCode, result)
		return nil, fmt.Errorf("get model request failed with status code: %d, %s", respCode, result)
	}

	// Parse the response data.
	smallModel := interfaces.SmallModel{}
	if err := sonic.Unmarshal(result, &smallModel); err != nil {
		logger.Errorf("Unmarshal model response failed: %v", err)
		return nil, fmt.Errorf("unmarshal model response failed: %w", err)
	}

	return &smallModel, nil
}
