// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"encoding/json"
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

// Statuses an answer on a knowledge network ends in.
const (
	BKN_AGENT_ANSWER_SUCCEEDED = "succeeded"
	BKN_AGENT_ANSWER_FAILED    = "failed"
	BKN_AGENT_ANSWER_CANCELLED = "cancelled"
)

// BknAgentAnswerRequest asks a chat Agent to answer Message on the knowledge network KNID.
// ClientRequestID makes the request idempotent for the caller: sent again, bkn-agent replays the
// answer it gave instead of answering again. It is 1 to 100 letters, digits, "_", "." or "-";
// bkn-agent refuses any other with 400 BknAgent.ParamError.FormatError.
type BknAgentAnswerRequest struct {
	AgentID         string `json:"agent_id"`
	Message         string `json:"message"`
	ClientRequestID string `json:"client_request_id"`
	KNID            string `json:"-"`
}

// BknAgentAnswer is how an answer ended. ErrorCode is bkn-agent's code for a failed one, such as
// BknAgent.Model.Unavailable or BknAgent.Chat.Timeout. The tool counts are of the calls the Agent
// made on the network, as streamed: a replayed answer has none. A tool call is unavailable when it
// did not go through, and failed otherwise when the tool returned an error. ToolUses are those
// calls in the order they started, with what they were asked.
type BknAgentAnswer struct {
	Status           string            `json:"status"`
	Answer           string            `json:"answer"`
	ErrorCode        string            `json:"error_code,omitempty"`
	ToolCalls        int               `json:"tool_calls"`
	ToolsUnavailable int               `json:"tools_unavailable"`
	ToolsFailed      int               `json:"tools_failed"`
	ToolUses         []BknAgentToolUse `json:"tool_uses,omitempty"`
}

// BknAgentToolUse is one tool call of an answer: the tool, its input as bkn-agent streamed it, and
// how it finished (succeeded or failed, with error_kind unavailable or error); Status is empty for
// a call the answer ended before finishing. An input too large to keep is left out, InputOmitted.
type BknAgentToolUse struct {
	Name         string          `json:"name"`
	Input        json.RawMessage `json:"input,omitempty"`
	InputOmitted bool            `json:"input_omitted,omitempty"`
	Status       string          `json:"status,omitempty"`
	ErrorKind    string          `json:"error_kind,omitempty"`
}

//go:generate mockgen -source ../interfaces/bkn_agent_access.go -destination ../interfaces/mock/mock_bkn_agent_access.go
type BknAgentAccess interface {
	// RunTask starts a task Agent once, in the background, as the account; the task it returns
	// is pending.
	RunTask(ctx context.Context, account BknAgentAccount, req *BknAgentRunRequest) (*BknAgentTask, error)
	// GetTask reads a task the account started. A task of another account reads as missing.
	GetTask(ctx context.Context, account BknAgentAccount, taskID string) (*BknAgentTask, error)
	// AnswerOnNetwork has a chat Agent answer a question on a knowledge network, with token, and
	// waits for the answer to end. A refusal before answering is a *BknAgentError.
	AnswerOnNetwork(ctx context.Context, token string, req *BknAgentAnswerRequest) (*BknAgentAnswer, error)
}
