// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "encoding/json"

const (
	BknAgentTaskStatusPending   string = "pending"
	BknAgentTaskStatusRunning   string = "running"
	BknAgentTaskStatusSucceeded string = "succeeded"
	BknAgentTaskStatusFailed    string = "failed"
)

type BknAgentRunRequest struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

type BknAgentRunResponse struct {
	TaskID string `json:"task_id"`
}

type BknAgentTask struct {
	ID            string          `json:"id,omitempty"`
	TaskID        string          `json:"task_id,omitempty"`
	Status        string          `json:"status"`
	Output        string          `json:"output,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	ResultJSON    json.RawMessage `json:"result_json,omitempty"`
	FailureDetail string          `json:"failure_detail,omitempty"`
	Error         string          `json:"error,omitempty"`
}
