// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package interfaces defines interfaces for business knowledge network action recall
package interfaces

import "context"

// ==================== Constant Definitions ====================

const (
	// ResultProcessStrategyKnActionRecall Result processing strategy for knowledge network action recall
	ResultProcessStrategyKnActionRecall = "kn_action_recall"
)

// ActionSource Type Constants
const (
	// ActionSourceTypeTool Tool type action source
	ActionSourceTypeTool = "tool"
	// ActionSourceTypeMCP MCP type action source (supported in next version)
	ActionSourceTypeMCP = "mcp"
)

// ==================== Request and Response Structures ====================

// KnActionRecallRequest Knowledge Network Action Recall Request
type KnActionRecallRequest struct {
	// Query Parameters
	KnID string `json:"kn_id" validate:"required"` // Knowledge Network ID
	AtID string `json:"at_id" validate:"required"` // Action Type ID

	// Request Body
	InstanceIdentity   map[string]any   `json:"_instance_identity" validate:"omitempty"`   // (legacy) Single instance identity; empty map treated as omitted
	InstanceIdentities []map[string]any `json:"_instance_identities" validate:"omitempty"` // Multiple instance identities; takes priority over InstanceIdentity

	// Header Fields
	AccountID   string `json:"-" header:"x-account-id"`
	AccountType string `json:"-" header:"x-account-type"`
}

// KnActionRecallResponse Knowledge Network Action Recall Response
type KnActionRecallResponse struct {
	Headers      map[string]string `json:"headers"` // HTTP Header Parameters
	DynamicTools []KnDynamicTool   `json:"_dynamic_tools"`
}

// KnDynamicTool Dynamic Tool Definition
type KnDynamicTool struct {
	Name            string         `json:"name"`                      // Tool Name
	Description     string         `json:"description"`               // Tool Description
	Parameters      map[string]any `json:"parameters"`                // OpenAI Function Call Schema
	APIURL          string         `json:"api_url"`                   // Tool Execution Proxy URL
	OriginalSchema  map[string]any `json:"original_schema,omitempty"` // Original OpenAPI Definition
	FixedParams     any            `json:"fixed_params"`              // Fixed Parameters (KnFixedParams or map[string]any)
	APICallStrategy string         `json:"api_call_strategy"`         // Result Processing Strategy, fixed value: kn_action_recall
}

// KnFixedParams Fixed Parameters Structure (legacy, kept for compatibility)
type KnFixedParams struct {
	Header map[string]any `json:"header"` // HTTP Header Parameters
	Path   map[string]any `json:"path"`   // URL Path Parameters
	Query  map[string]any `json:"query"`  // URL Query Parameters
	Body   map[string]any `json:"body"`   // Request Body Parameters
}

// ActionDriverFixedParams 行动驱动请求默认值
type ActionDriverFixedParams struct {
	DynamicParams      map[string]any   `json:"dynamic_params"`       // 行动实例化后已确定的固定参数
	InstanceIdentities []map[string]any `json:"_instance_identities"` // 默认填入当前 get_action_info 的 _instance_identity
}

// ==================== Action Query Related Structures ====================

// QueryActionsRequest Action Query Request
type QueryActionsRequest struct {
	KnID                string           `json:"kn_id"`
	AtID                string           `json:"at_id"`
	InstanceIdentities  []map[string]any `json:"_instance_identities"`
	IncludeTypeInfo     bool             `json:"include_type_info"`
	XHTTPMethodOverride string           `json:"-"` // Fixed to GET
}

// QueryActionsResponse Action Query Response
type QueryActionsResponse struct {
	ActionType   *ActionTypeInfo `json:"action_type,omitempty"` // Action Type Info
	ActionSource *ActionSource   `json:"action_source"`         // Action Source
	Actions      []ActionParams  `json:"actions"`               // Action Parameters List
	TotalCount   int             `json:"total_count"`
	OverallMs    int             `json:"overall_ms"`
}

// ActionTypeInfo Action Type Info
type ActionTypeInfo struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	ActionType   string            `json:"action_type"` // add/modify/delete
	ObjectTypeID string            `json:"object_type_id"`
	Parameters   []ActionTypeParam `json:"parameters"`
	Condition    map[string]any    `json:"condition"`
	Affect       map[string]any    `json:"affect"`
	Schedule     map[string]any    `json:"schedule"`
}

// ActionTypeParam Action Type Parameter
type ActionTypeParam struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Source    string `json:"source"`
	ValueFrom string `json:"value_from"` // property/input/const
	Value     string `json:"value,omitempty"`
}

// ActionSource Action Source
type ActionSource struct {
	Type     string `json:"type"`                // tool/mcp
	BoxID    string `json:"box_id"`              // Tool Box ID
	ToolID   string `json:"tool_id"`             // Tool ID
	McpID    string `json:"mcp_id,omitempty"`    // MCP ID
	ToolName string `json:"tool_name,omitempty"` // Tool Name
}

// ActionParams Action Parameters
type ActionParams struct {
	Parameters    map[string]any `json:"parameters"`     // Instantiated Parameters
	DynamicParams map[string]any `json:"dynamic_params"` // Dynamic Parameters (value is null)
}

// ==================== Service Interfaces ====================

// KnActionExecuteRequest Knowledge Network Action Execution Request
type KnActionExecuteRequest struct {
	// Query Parameters
	KnID string `json:"kn_id" validate:"required"` // Knowledge Network ID
	AtID string `json:"at_id" validate:"required"` // Action Type ID

	// Request Body
	InstanceIdentities []map[string]any `json:"_instance_identities" validate:"omitempty"` // Target instances; empty means scan-by-condition
	DynamicParams      map[string]any   `json:"dynamic_params" validate:"omitempty"`       // Dynamic parameter values (value_from=input)

	// Header Fields
	AccountID   string `json:"-" header:"x-account-id"`
	AccountType string `json:"-" header:"x-account-type"`
}

// KnActionExecuteResponse Knowledge Network Action Execution Response (async)
type KnActionExecuteResponse struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	CreatedAt   int64  `json:"created_at"`
}

// ExecuteActionsRequest Action Execution Request to ontology-query
type ExecuteActionsRequest struct {
	KnID               string           `json:"-"`
	AtID               string           `json:"-"`
	InstanceIdentities []map[string]any `json:"_instance_identities"`
	DynamicParams      map[string]any   `json:"dynamic_params,omitempty"`
}

// ExecuteActionsResponse Action Execution Response from ontology-query
type ExecuteActionsResponse struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	CreatedAt   int64  `json:"created_at"`
}

// KnGetActionExecutionRequest 查询单次行动执行的状态与结果
type KnGetActionExecutionRequest struct {
	KnID        string `json:"kn_id" validate:"required"`       // Knowledge Network ID
	ExecutionID string `json:"execution_id" validate:"required"` // 由 execute_action 返回的执行 ID

	AccountID   string `json:"-" header:"x-account-id"`
	AccountType string `json:"-" header:"x-account-type"`
}

// KnListActionExecutionsRequest 列出行动执行历史（可按行动类型/状态/触发方式过滤，分页）
type KnListActionExecutionsRequest struct {
	KnID          string `json:"kn_id" validate:"required"` // Knowledge Network ID
	ActionTypeID  string `json:"action_type_id,omitempty"`  // 按行动类型过滤（可选）
	Status        string `json:"status,omitempty"`          // 按状态过滤：pending/running/completed/failed/cancelled（可选）
	TriggerType   string `json:"trigger_type,omitempty"`    // 按触发方式过滤：manual/scheduled（可选）
	StartTimeFrom int64  `json:"start_time_from,omitempty"` // 起始时间下界（Unix 毫秒，可选）
	StartTimeTo   int64  `json:"start_time_to,omitempty"`   // 起始时间上界（Unix 毫秒，可选）
	Offset        int    `json:"offset,omitempty"`          // 分页偏移（可选）
	Limit         int    `json:"limit,omitempty"`           // 分页条数，默认 20，最大 1000（可选）
	SearchAfter   []any  `json:"search_after,omitempty"`    // 游标分页：上一页响应的 search_after 原样回传（可选）

	AccountID   string `json:"-" header:"x-account-id"`
	AccountType string `json:"-" header:"x-account-type"`
}

// GetActionExecutionRequest 转发到 ontology-query 的单次执行查询请求
type GetActionExecutionRequest struct {
	KnID        string `json:"-"`
	ExecutionID string `json:"-"`
}

// ListActionExecutionsRequest 转发到 ontology-query 的执行历史查询请求
type ListActionExecutionsRequest struct {
	KnID          string
	ActionTypeID  string
	Status        string
	TriggerType   string
	StartTimeFrom int64
	StartTimeTo   int64
	Offset        int
	Limit         int
	SearchAfter   []any
}

// IKnActionRecallService Knowledge Network Action Recall Service Interface
type IKnActionRecallService interface {
	// GetActionInfo gets action information (action recall)
	GetActionInfo(ctx context.Context, req *KnActionRecallRequest) (*KnActionRecallResponse, error)
	// ExecuteAction executes an action type (async), returning an execution id
	ExecuteAction(ctx context.Context, req *KnActionExecuteRequest) (*KnActionExecuteResponse, error)
	// GetActionExecution retrieves a single execution's status and results by execution id
	GetActionExecution(ctx context.Context, req *KnGetActionExecutionRequest) (map[string]any, error)
	// ListActionExecutions lists action execution history with optional filters and pagination
	ListActionExecutions(ctx context.Context, req *KnListActionExecutionsRequest) (map[string]any, error)
}
