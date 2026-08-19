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

// chatOKResp builds a successful response that chatCompletionsResp can parse.
func chatOKResp(content string) map[string]interface{} {
	return map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{"content": content},
			},
		},
	}
}

// newTestMFModelAPIClient constructs the client directly and bypasses the config-reading singleton constructor.
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

// TestChat_ZeroSamplingParamsOmitted reproduces issue #450: When the caller does not set TopP/TopK,
// Go zero value must not be sent to mf-model-api as a business parameter (its validation is 0 < top_p <= 1, top_k ≥ 1,
// Receiving 0 directly results in 400, and the error is mistakenly packaged as "missing parameter" in the upper layer).
func TestChat_ZeroSamplingParamsOmitted(t *testing.T) {
	var got map[string]interface{}
	client := newTestMFModelAPIClient(&got)

	// Set only MaxTokens, consistent with how dynamic_params_llm.chatJSON calls it.
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
	// A value of 0 for temperature/penalty is a valid business value and must be sent as-is.
	if v, ok := got["temperature"]; !ok || v != float64(0) {
		t.Errorf("temperature = %v (present=%v), want 0", v, ok)
	}
}

// TestChat_ExplicitSamplingParamsKept must pass explicitly set sampling parameters through as-is.
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

// TestChat_ZeroMaxTokensOmitted mf-model-api requires max_tokens >= 10, so zero values must also be omitted.
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
