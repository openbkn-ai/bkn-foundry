// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package drivenadapters

import (
	"context"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/context-loader/agent-retrieval/server/interfaces"
)

// chatOKResp 构造一个能被 chatCompletionsResp 解析的成功响应
func chatOKResp(content string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{"content": content},
			},
		},
	}
}

// newTestMFModelAPIClient 直接构造客户端，绕开读配置的单例构造器
func newTestMFModelAPIClient(capture *map[string]interface{}) *mfModelAPIClient {
	return &mfModelAPIClient{
		logger:  &mockLogger{},
		baseURL: "http://mf-model-api",
		httpClient: &mockHTTPClient{
			handlerFunc: func(_ context.Context, _, _ string, _ map[string]string, body interface{}) (int, interface{}, error) {
				if m, ok := body.(map[string]interface{}); ok {
					*capture = m
				}
				return 200, chatOKResp("ok"), nil
			},
		},
	}
}

// TestChat_ZeroSamplingParamsOmitted 复现 issue #450：调用方未设置 TopP/TopK 时，
// Go 零值不得作为业务参数发给 mf-model-api（其校验为 0 < top_p ≤ 1、top_k ≥ 1，
// 收到 0 直接 400，错误在上层被误包装成「缺参」）。
func TestChat_ZeroSamplingParamsOmitted(t *testing.T) {
	var got map[string]interface{}
	client := newTestMFModelAPIClient(&got)

	// 仅设置 MaxTokens，与 dynamic_params_llm.chatJSON 的调用方式一致
	_, err := client.Chat(context.Background(), &interfaces.LLMChatReq{
		Messages:  []interfaces.LLMMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 2000,
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	for _, key := range []string{"top_p", "top_k"} {
		if v, ok := got[key]; ok {
			t.Errorf("zero-valued %s must be omitted from request body, got %v", key, v)
		}
	}
	if got["max_tokens"] != 2000 {
		t.Errorf("max_tokens = %v, want 2000", got["max_tokens"])
	}
	// temperature / penalty 的 0 是合法业务取值，必须原样发送
	if v, ok := got["temperature"]; !ok || v != float64(0) {
		t.Errorf("temperature = %v (present=%v), want 0", v, ok)
	}
}

// TestChat_ExplicitSamplingParamsKept 显式设置的采样参数必须原样透传
func TestChat_ExplicitSamplingParamsKept(t *testing.T) {
	var got map[string]interface{}
	client := newTestMFModelAPIClient(&got)

	_, err := client.Chat(context.Background(), &interfaces.LLMChatReq{
		Messages:         []interfaces.LLMMessage{{Role: "user", Content: "hi"}},
		Temperature:      0.7,
		TopK:             2,
		TopP:             0.5,
		FrequencyPenalty: 0.5,
		PresencePenalty:  0.5,
		MaxTokens:        5000,
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	want := map[string]interface{}{
		"temperature":       0.7,
		"top_k":             2,
		"top_p":             0.5,
		"frequency_penalty": 0.5,
		"presence_penalty":  0.5,
		"max_tokens":        5000,
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("%s = %v, want %v", key, got[key], expected)
		}
	}
}

// TestChat_ZeroMaxTokensOmitted mf-model-api 要求 max_tokens ≥ 10，零值同样不得发送
func TestChat_ZeroMaxTokensOmitted(t *testing.T) {
	var got map[string]interface{}
	client := newTestMFModelAPIClient(&got)

	_, err := client.Chat(context.Background(), &interfaces.LLMChatReq{
		Messages: []interfaces.LLMMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if v, ok := got["max_tokens"]; ok {
		t.Errorf("zero-valued max_tokens must be omitted, got %v", v)
	}
}
