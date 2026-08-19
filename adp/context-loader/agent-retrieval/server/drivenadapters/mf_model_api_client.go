// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/config"
	infraErr "github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/utils"
)

// API path constants.
const (
	chatCompletionsURI = "/v1/chat/completions"
	rerankURI          = "/v1/small-model/reranker"
)

// MfModelAPIClient MF-Model APIunified client.
// Provides LLM chat and vector reranking capabilities, uniformly uses mf-model-api service.
type mfModelAPIClient struct {
	logger     interfaces.Logger
	baseURL    string
	httpClient interfaces.HTTPClient
}

var (
	mfModelAPIClientOnce sync.Once
	mfModelAPIClientInst *mfModelAPIClient
)

// NewMFModelAPIClient createMF-Model APIunified clientsingleton.
// Implements DrivenMFModelAPIClient API.
func NewMFModelAPIClient() *mfModelAPIClient {
	mfModelAPIClientOnce.Do(func() {
		conf := config.NewConfigLoader()
		mfModelAPIClientInst = &mfModelAPIClient{
			logger:     conf.GetLogger(),
			baseURL:    conf.MFModelAPI.BuildURL("/api/private/mf-model-api"),
			httpClient: rest.NewHTTPClient(),
		}
	})
	return mfModelAPIClientInst
}

// ============================================================
// DrivenLLMClient interface implementation.
// ============================================================

// chatCompletionsResp is the Chat API response structure.
type chatCompletionsResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Chat non-streaming chat, returncomplete response content.
func (c *mfModelAPIClient) Chat(ctx context.Context, req *interfaces.LLMChatReq) (string, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, chatCompletionsURI)

	// Buildrequest body.
	// 0 in temperature / frequency_penalty / presence_penalty is a legal service value (the range is respectively.
	// [0,2] and [-2,2]), always send; the legal range of top_p / top_k / max_tokens does not contain 0.
	// (0 < top_p <= 1, top_k ≥ 1, max_tokens ≥ 10), the Go zero value when not set by the caller cannot be regarded as a business.
	// The parameters are sent out, otherwise mf-model-api will directly 400 (issue #450). Use map here to assemble, on struct.
	// Omitempty does not take effect, mustexplicitly check for zero.
	reqBody := map[string]interface{}{
		"model":             req.Model,
		"messages":          req.Messages,
		"stream":            false, // Non-streaming.
		"temperature":       req.Temperature,
		"frequency_penalty": req.FrequencyPenalty,
		"presence_penalty":  req.PresencePenalty,
	}
	if req.TopP > 0 {
		reqBody["top_p"] = req.TopP
	}
	if req.TopK > 0 {
		reqBody["top_k"] = req.TopK
	}
	if req.MaxTokens > 0 {
		reqBody["max_tokens"] = req.MaxTokens
	}

	// Get headers in a unified way.
	header := common.GetHeaderForChildOperation(ctx, "model.chat", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	c.logger.WithContext(ctx).Debugf("[MFModelAPIClient#Chat] URL: %s", url)

	// Call the HTTP client.
	respCode, respBody, err := c.httpClient.Post(ctx, url, header, reqBody)
	if err != nil {
		c.logger.WithContext(ctx).Errorf("[MFModelAPIClient#Chat] Request failed: %v", err)
		return "", fmt.Errorf("request failed: %w", err)
	}

	if respCode != http.StatusOK {
		c.logger.WithContext(ctx).Errorf("[MFModelAPIClient#Chat] Request failed with code %d", respCode)
		return "", infraErr.DefaultHTTPError(ctx, respCode, fmt.Sprintf("chat request failed with code %d", respCode))
	}

	// Parse the response.
	var resp chatCompletionsResp
	resultBytes := utils.ObjectToByte(respBody)
	if err := json.Unmarshal(resultBytes, &resp); err != nil {
		c.logger.WithContext(ctx).Errorf("[MFModelAPIClient#Chat] Unmarshal failed: %v", err)
		return "", fmt.Errorf("unmarshal response failed: %w", err)
	}

	// Extract content.
	if len(resp.Choices) > 0 && resp.Choices[0].Message.Content != "" {
		content := resp.Choices[0].Message.Content
		c.logger.WithContext(ctx).Debugf("[MFModelAPIClient#Chat] Response length: %d", len(content))
		return content, nil
	}

	return "", fmt.Errorf("unexpected response format: no content found")
}

// ============================================================
// DrivenRerankClient interface implementation.
// ============================================================

// Rerank reorders documents.
//
// If the model is empty, it means "use the default reranker checked in the model management", which is parsed by mf-model-api by type.
// (t_small_model.f_default=1). **No more taking the word "reranker"** literally: That's just a guess.
// The registered name is to try your luck. As long as the registered name is not called reranker, it will be downgraded across the board by NameNotExist, and the administrator is.
// The default checkbox in model management is not read by anyone (#842). To specify a specific model, the caller can still pass model.
func (c *mfModelAPIClient) Rerank(ctx context.Context, query string, documents []string, model string) (*interfaces.RerankResp, error) {
	url := fmt.Sprintf("%s%s", c.baseURL, rerankURI)
	// Build the request body.
	reqBody := map[string]interface{}{
		"query":     query,
		"documents": documents,
		"model":     model,
	}

	// Get headers in a unified way.
	header := common.GetHeaderForChildOperation(ctx, "model.rerank", 1)
	header[rest.ContentTypeKey] = rest.ContentTypeJSON

	c.logger.WithContext(ctx).Debugf("[MFModelAPIClient#Rerank] URL: %s, query: %s, docs count: %d",
		url, query, len(documents))

	// Call the HTTP client.
	_, respBody, err := c.httpClient.Post(ctx, url, header, reqBody)
	if err != nil {
		c.logger.WithContext(ctx).Errorf("[MFModelAPIClient#Rerank] Request failed: %v", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Parse the response.
	var result interfaces.RerankResp
	resultBytes := utils.ObjectToByte(respBody)
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		c.logger.WithContext(ctx).Errorf("[MFModelAPIClient#Rerank] Unmarshal failed: %v", err)
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	c.logger.WithContext(ctx).Debugf("[MFModelAPIClient#Rerank] Results count: %d", len(result.Results))

	return &result, nil
}
