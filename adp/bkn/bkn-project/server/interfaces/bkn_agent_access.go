// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"fmt"
)

// Statuses of a bkn-agent task; succeeded and failed are terminal.
const (
	BKN_AGENT_TASK_PENDING   = "pending"
	BKN_AGENT_TASK_RUNNING   = "running"
	BKN_AGENT_TASK_SUCCEEDED = "succeeded"
	BKN_AGENT_TASK_FAILED    = "failed"
)

// BknAgentAccount is the account bkn-agent runs and reads tasks as: only the account that started
// a task may read it.
type BknAgentAccount = AccountInfo

// BknAgentRunRequest starts a task Agent once. ResponseFormat is a JSON Schema whose root is an
// object; when set, the task's output is JSON matching it.
type BknAgentRunRequest struct {
	AgentID        string         `json:"agent_id"`
	Message        string         `json:"message"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
}

// BknAgentTask is a task as bkn-agent reports it. Output is set once it succeeded, FailureDetail
// once it failed.
type BknAgentTask struct {
	TaskID        string `json:"task_id"`
	AgentID       string `json:"agent_id"`
	Status        string `json:"status"`
	Output        string `json:"output"`
	FailureDetail string `json:"failure_detail"`
	CreateTime    int64  `json:"create_time"`
	UpdateTime    int64  `json:"update_time"`
}

// Terminal tells whether the task has finished.
func (t *BknAgentTask) Terminal() bool {
	return t.Status == BKN_AGENT_TASK_SUCCEEDED || t.Status == BKN_AGENT_TASK_FAILED
}

// BknAgentError is a reply of bkn-agent that is not a success, with its status and error code
// (such as BknAgent.NotFound or BknAgent.Task.ModeMismatch), so callers can tell refusals apart.
type BknAgentError struct {
	HTTPCode    int    `json:"-"`
	Code        string `json:"code"`
	Description string `json:"description"`
}

func (e *BknAgentError) Error() string {
	return fmt.Sprintf("bkn-agent returned HTTP %d: %s %s", e.HTTPCode, e.Code, e.Description)
}

//go:generate mockgen -source ../interfaces/bkn_agent_access.go -destination ../interfaces/mock/mock_bkn_agent_access.go
type BknAgentAccess interface {
	// RunTask starts a task Agent once, in the background, as the account; the task it returns
	// is pending.
	RunTask(ctx context.Context, account BknAgentAccount, req *BknAgentRunRequest) (*BknAgentTask, error)
	// GetTask reads a task the account started. A task of another account reads as missing.
	GetTask(ctx context.Context, account BknAgentAccount, taskID string) (*BknAgentTask, error)
}
