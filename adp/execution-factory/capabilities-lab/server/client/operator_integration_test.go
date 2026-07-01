// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package client

import (
	"encoding/json"
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
