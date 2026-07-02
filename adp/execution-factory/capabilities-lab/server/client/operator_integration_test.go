// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateToolFailureMessageUsesErrorMsgDescription(t *testing.T) {
	var resp createToolResponse
	raw := []byte(`{
		"failure_count": 1,
		"failures": [{
			"tool_name": "test",
			"error_msg": {
				"code": "AgentOperatorIntegration.BadRequest.ToolExists",
				"description": "工具 “test” 已存在",
				"details": "tool name test exist"
			}
		}]
	}`)

	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if got := resp.Failures[0].Message(); got != "工具 “test” 已存在" {
		t.Fatalf("Message() = %q", got)
	}
}

func TestExecuteFunctionForwardsSandboxRuntimeContext(t *testing.T) {
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent-operator-integration/v1/function/execute" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("x-business-domain"); got != "bd_public" {
			t.Fatalf("x-business-domain = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"result":{"ok":true}}}`))
	}))
	defer server.Close()

	client := &OperatorIntegrationClient{
		BaseURL: server.URL,
		HTTP:    server.Client(),
	}

	_, err := client.ExecuteFunction(
		context.Background(),
		"bd_public",
		"user_001",
		ExecuteFunctionRequest{
			Code:           "def handler(event):\n    return event\n",
			Event:          map[string]interface{}{"city": "beijing"},
			Language:       "python",
			Timeout:        30,
			Source:         "function_debug",
			TaskID:         "task_e2e_001",
			CapabilityID:   "cap_function_weather",
			CapabilityName: "weather_normalizer",
			UserID:         "user_001",
			UserName:       "alice",
		},
	)
	if err != nil {
		t.Fatalf("ExecuteFunction() error = %v", err)
	}

	assertPayloadString(t, payload, "source", "function_debug")
	assertPayloadString(t, payload, "task_id", "task_e2e_001")
	assertPayloadString(t, payload, "capability_id", "cap_function_weather")
	assertPayloadString(t, payload, "capability_name", "weather_normalizer")
	assertPayloadString(t, payload, "user_id", "user_001")
	assertPayloadString(t, payload, "user_name", "alice")
}

func assertPayloadString(t *testing.T, payload map[string]interface{}, key, want string) {
	t.Helper()
	if got, _ := payload[key].(string); got != want {
		t.Fatalf("payload[%q] = %q, want %q; full payload: %+v", key, got, want, payload)
	}
}
