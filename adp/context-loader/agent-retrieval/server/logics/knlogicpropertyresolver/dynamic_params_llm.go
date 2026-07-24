// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package knlogicpropertyresolver
// file: dynamic_params_llm.go
// desc: metric/tool dynamic parameter generation is handled by mf-model-api.
//
//	prompt 从原 agent 定义提取并内置（见 prompts/ 目录），随服务发布，不再依赖
//	agent-factory 中手动导入的 agent。
package knlogicpropertyresolver

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/interfaces"
	"github.com/openbkn-ai/adp/context-loader/agent-retrieval/server/utils"
)

//go:embed prompts/metric_dynamic_params.md
var metricDynamicParamsPrompt string

//go:embed prompts/tool_dynamic_params.md
var toolDynamicParamsPrompt string

// dynamicParamsMaxTokens is sufficient for dynamic parameter generation.
const dynamicParamsMaxTokens = 2000

// dynamicParamsLLM generates metric and ToolBox-tool dynamic parameters.
// 不指定模型：mf-model-api 在 model 为空时解析系统默认大模型（t_llm_model.f_default=1，
// 由管理员经 mf-model-manager /llm/default/edit 接口全局设置），与小模型同款机制，服务侧零配置。
type dynamicParamsLLM struct {
	logger        interfaces.Logger
	mfModelClient interfaces.DrivenMFModelAPIClient
	toolClient    interfaces.DrivenOperatorIntegration
}

// newDynamicParamsLLM 构建直连 LLM 动态参数生成器。
func newDynamicParamsLLM(
	logger interfaces.Logger,
	mfModelClient interfaces.DrivenMFModelAPIClient,
	toolClient interfaces.DrivenOperatorIntegration,
) *dynamicParamsLLM {
	return &dynamicParamsLLM{logger: logger, mfModelClient: mfModelClient, toolClient: toolClient}
}

// GenerateMetricParams 直连 LLM 生成 metric 类型动态参数。
// 返回值语义对齐原 agent：成功返回 dynamicParams（顶层键为 logic_property.name），
// 模型判定缺参时返回 missingParams；二者互斥。
func (d *dynamicParamsLLM) GenerateMetricParams(
	ctx context.Context,
	req *interfaces.MetricDynamicParamsGeneratorReq,
	llmModel string,
) (dynamicParams map[string]any, missingParams *interfaces.MissingPropertyParams, err error) {
	userJSON := utils.ObjectToJSON(req)
	d.logger.WithContext(ctx).Infof("  ├─ [直连LLM] Metric 入参: query=%s", userJSON)

	resultStr, err := d.chatJSON(ctx, metricDynamicParamsPrompt, userJSON, llmModel)
	if err != nil {
		d.logger.WithContext(ctx).Errorf("  ├─ [直连LLM] ❌ Metric Chat 失败: %v", err)
		return nil, nil, err
	}

	var rawResult map[string]any
	if err = json.Unmarshal([]byte(resultStr), &rawResult); err != nil {
		d.logger.WithContext(ctx).Errorf("  ├─ [直连LLM] ❌ Metric JSON 解析失败: %v, raw=%s", err, resultStr)
		return nil, nil, fmt.Errorf("unmarshal metric llm result failed: %w", err)
	}

	if errorMsg, ok := rawResult["_error"].(string); ok {
		missingParams = newMissingParams(req.LogicProperty.Name, errorMsg)
		d.logger.WithContext(ctx).Warnf("  └─ [直连LLM] ⚠️ Metric 缺参: %s", errorMsg)
		return nil, missingParams, nil
	}

	d.logger.WithContext(ctx).Debugf("  └─ [直连LLM] ✅ Metric 成功: %+v", rawResult)
	return rawResult, nil, nil
}

// GenerateToolParams generates dynamic parameters from the selected ToolBox tool schema.
func (d *dynamicParamsLLM) GenerateToolParams(
	ctx context.Context,
	req *interfaces.ToolDynamicParamsGeneratorReq,
	llmModel string,
) (dynamicParams map[string]any, missingParams *interfaces.MissingPropertyParams, err error) {
	var toolSchema string
	if req.BoxID != "" && req.ToolID != "" {
		tool, toolErr := d.toolClient.GetToolDetail(ctx, &interfaces.GetToolDetailRequest{
			BoxID: req.BoxID, ToolID: req.ToolID,
		})
		if toolErr != nil {
			d.logger.WithContext(ctx).Warnf("  ├─ [LLM] tool schema lookup failed(box_id=%s, tool_id=%s): %v",
				req.BoxID, req.ToolID, toolErr)
		} else if tool != nil {
			toolSchema = utils.ObjectToJSON(tool.Metadata.APISpec)
		}
	}

	userMsg := fmt.Sprintf("【输入】\n%s\n\n【工具的 Schema 信息】\n%s", utils.ObjectToJSON(req), toolSchema)
	d.logger.WithContext(ctx).Infof("  ├─ [LLM] Tool input: property=%s, box_id=%s, tool_id=%s",
		req.LogicProperty.Name, req.BoxID, req.ToolID)

	resultStr, err := d.chatJSON(ctx, toolDynamicParamsPrompt, userMsg, llmModel)
	if err != nil {
		d.logger.WithContext(ctx).Errorf("  ├─ [LLM] Tool chat failed: %v", err)
		return nil, nil, err
	}

	var rawResult map[string]any
	if err = json.Unmarshal([]byte(resultStr), &rawResult); err != nil {
		d.logger.WithContext(ctx).Errorf("  ├─ [LLM] Tool JSON parsing failed: %v, raw=%s", err, resultStr)
		return nil, nil, fmt.Errorf("unmarshal tool llm result failed: %w", err)
	}

	if errorMsg, ok := rawResult["_error"].(string); ok {
		missingParams = newMissingParams(req.LogicProperty.Name, errorMsg)
		d.logger.WithContext(ctx).Warnf("  └─ [LLM] Tool missing parameters: %s", errorMsg)
		return nil, missingParams, nil
	}

	d.logger.WithContext(ctx).Debugf("  └─ [LLM] Tool generated parameters: %+v", rawResult)
	return rawResult, nil, nil
}

// chatJSON 通用对话：system=prompt，user=输入JSON；返回从模型输出中抽取的首个 JSON 对象。
// model 为 per-request 覆盖（仅测试/验证指定）；为空 => mf-model-api 解析系统默认大模型。
func (d *dynamicParamsLLM) chatJSON(ctx context.Context, systemPrompt, userJSON, model string) (string, error) {
	req := &interfaces.LLMChatReq{
		Model: model, // 空 => mf-model-api 使用系统默认大模型
		Messages: []interfaces.LLMMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userJSON},
		},
		MaxTokens: dynamicParamsMaxTokens,
	}
	content, err := d.mfModelClient.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	return extractJSONObject(content)
}

// extractJSONObject 从模型输出中抽取首个 {...} JSON 片段，剥离 ```json 围栏与多余文本。
// 对齐原 agent 的 parseResultFromAgentV1Answer 行为。
func extractJSONObject(s string) (string, error) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return "", fmt.Errorf("no JSON object found in LLM output: %s", s)
	}
	jsonStr := s[start : end+1]
	// 处理被转义的换行/引号（部分模型会回传转义串）
	if strings.Contains(jsonStr, "\\n") || strings.Contains(jsonStr, "\\\"") {
		jsonStr = strings.ReplaceAll(jsonStr, "\\n", "\n")
		jsonStr = strings.ReplaceAll(jsonStr, "\\\"", "\"")
	}
	return jsonStr, nil
}

// newMissingParams 构建缺参结果，直接透传模型生成的错误消息。
func newMissingParams(propertyName, errorMsg string) *interfaces.MissingPropertyParams {
	return &interfaces.MissingPropertyParams{
		Property: propertyName,
		ErrorMsg: errorMsg,
	}
}
