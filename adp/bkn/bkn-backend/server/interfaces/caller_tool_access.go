// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"encoding/json"
	"time"
)

// Function tool kinds this service compares against, as the execution factory
// serialises them.
const (
	EXEC_FUNCTION_SCRIPT_CYPHER = "cypher"
	EXEC_FUNCTION_RESULT_TABLE  = "table"
)

// CallerTool is a tool of the execution factory as the account in the
// request's context may use it.
type CallerTool struct {
	ToolID string
	Name   string
	Status string
	// UpdateTime is when the tool was last changed, in the execution factory's
	// own unit. It only ever compares with itself.
	UpdateTime int64
	// ScriptType, KnID and ResultShape describe a function tool. A Cypher
	// function has all three: the network it reads and the shape it returns.
	ScriptType  string
	KnID        string
	ResultShape string
	Inputs      []CallerToolInput
}

// CallerToolInput is one parameter a tool takes.
type CallerToolInput struct {
	Name     string
	Type     string
	Required bool
}

// CallerToolReply is what a tool said when it ran: its own status and body.
// A tool that refused its arguments, or whose statement failed, still ran;
// its status says so.
type CallerToolReply struct {
	StatusCode int
	Body       json.RawMessage
	Error      string
}

// CallerToolAccess uses execution-factory tools as the account in the
// request's context, with that account's own permissions and no token. A
// refusal of the execution factory itself is a *DependencyError with its
// status.
type CallerToolAccess interface {
	// GetCallerTool reads a tool of a tool box.
	GetCallerTool(ctx context.Context, boxID, toolID string) (*CallerTool, error)
	// ExecuteCallerTool runs a tool with arguments, waiting up to timeout.
	ExecuteCallerTool(ctx context.Context, boxID, toolID string, arguments map[string]any, timeout time.Duration) (*CallerToolReply, error)
}
