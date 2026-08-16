// Package aigeneration implements AI-assisted function generation.
// @file aigeneration.go
// @description: AI-assisted function generation
package aigeneration

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
)

// aiGenerationService implements AI-assisted generation.
type aiGenerationService struct {
	Logger           interfaces.Logger
	MFModelAPIClient interfaces.MFModelAPIClient
	PromptLoader     *PromptLoader
	LLMConfig        config.LLMConfig
}

var (
	agOnce     sync.Once
	agInstance interfaces.AIGenerationService
)

// NewAIGenerationService returns the shared AI generation service.
func NewAIGenerationService() interfaces.AIGenerationService {
	agOnce.Do(func() {
		promptLoader, err := NewPromptLoader()
		if err != nil {
			log.Printf("failed to create prompt loader: %v", err)
			panic(err)
		}
		conf := config.NewConfigLoader()
		agInstance = &aiGenerationService{
			Logger:           conf.GetLogger(),
			MFModelAPIClient: drivenadapters.NewMFModelAPIClient(),
			LLMConfig:        conf.AIGenerationConfig.LLMConfig,
			PromptLoader:     promptLoader,
		}
	})
	return agInstance
}

func (ag *aiGenerationService) generateChatCompletionParams(ctx context.Context, req *interfaces.FunctionAIGenerateReq) (*interfaces.ChatCompletionReq, error) {
	promptTemplate, err := ag.PromptLoader.GetTemplate(ctx, req.Type)
	if err != nil {
		ag.Logger.WithContext(ctx).Errorf("failed to get prompt template: %v", err)
		err = errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
		return nil, err
	}
	chatCompletionReq := &interfaces.ChatCompletionReq{
		Model:            ag.LLMConfig.Model, // An empty model name selects the default model.
		MaxTokens:        ag.LLMConfig.MaxTokens,
		Temperature:      ag.LLMConfig.Temperature,
		TopK:             ag.LLMConfig.TopK,
		TopP:             ag.LLMConfig.TopP,
		FrequencyPenalty: ag.LLMConfig.FrequencyPenalty,
		PresencePenalty:  ag.LLMConfig.PresencePenalty,
		Messages: []interfaces.ChatCompletionMessage{
			{
				Role:    "system",
				Content: promptTemplate.SystemPrompt,
			},
		},
	}
	var userPrompt string
	switch req.Type {
	case interfaces.PythonFunctionGenerator:
		// Supply a default function request when the caller omits one.
		if req.Query == "" {
			tr := localize.NewI18nTranslator(common.GetLanguageFromCtx(ctx))
			req.Query = tr.Trans("prompt.default_function_request")
		}
		userPrompt = promptTemplate.FormatUserPrompt(req.Query, req.Inputs, req.Outputs)
	case interfaces.MetadataParamGenerator:
		userPrompt = promptTemplate.FormatUserPrompt(req.Code, req.Inputs, req.Outputs)
	}
	chatCompletionReq.Messages = append(chatCompletionReq.Messages, interfaces.ChatCompletionMessage{
		Role:    "user",
		Content: userPrompt,
	})
	return chatCompletionReq, nil
}

// FunctionAIGenerate generates a complete response.
func (ag *aiGenerationService) FunctionAIGenerate(ctx context.Context, req *interfaces.FunctionAIGenerateReq) (resp *interfaces.FunctionAIGeneratResp, err error) {
	chatCompletionReq, err := ag.generateChatCompletionParams(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := ag.MFModelAPIClient.ChatCompletion(ctx, chatCompletionReq)
	if err != nil {
		return nil, err
	}
	var apiGenContent string
	if len(result.Choices) > 0 {
		apiGenContent = result.Choices[0].Message.Content
	}
	if apiGenContent == "" {
		err = errors.NewHTTPError(ctx, http.StatusServiceUnavailable, errors.ErrExtFunctionAIGenerateFailed, fmt.Sprintf("ai response %v", result))
		return nil, err
	}
	resp = &interfaces.FunctionAIGeneratResp{}
	switch req.Type {
	case interfaces.PythonFunctionGenerator:
		resp.Content = apiGenContent
	case interfaces.MetadataParamGenerator:
		content := &interfaces.AIGeneratMetadataContent{}
		err = utils.StringToObject(apiGenContent, content)
		if err != nil {
			err = errors.NewHTTPError(ctx, http.StatusServiceUnavailable, errors.ErrExtFunctionAIGenerateFailed, fmt.Sprintf("ai response %v format unmarshal err: %s", result, err.Error()))
			return nil, err
		}
		resp.Content = content
	}
	return resp, nil
}

// FunctionAIGenerateStream streams generated response chunks.
func (ag *aiGenerationService) FunctionAIGenerateStream(ctx context.Context, req *interfaces.FunctionAIGenerateReq) (respChan chan string, errChan chan error, err error) {
	chatCompletionReq, err := ag.generateChatCompletionParams(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	respChan, errChan, err = ag.MFModelAPIClient.StreamChatCompletion(ctx, chatCompletionReq)
	if err != nil {
		return nil, nil, err
	}
	return respChan, errChan, nil
}

// GetPromptTemplate returns the active prompt template.
func (ag *aiGenerationService) GetPromptTemplate(ctx context.Context, tempType interfaces.PromptTemplateType) (*interfaces.PromptTemplate, error) {
	return ag.PromptLoader.GetTemplate(ctx, tempType)
}
