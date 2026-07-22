// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import "context"

// KnConceptType Knowledge Network Concept Type
type KnConceptType string

const (
	KnConceptTypeObject   KnConceptType = "object_type"   // Object Type
	KnConceptTypeRelation KnConceptType = "relation_type" // Relation Type
	KnConceptTypeAction   KnConceptType = "action_type"   // Action Type
)

// QueryObjectInstancesReq Request object for querying detailed object instances
type QueryObjectInstancesReq struct {
	KnID               string       `form:"kn_id"`                                         // Knowledge Network ID
	OtID               string       `form:"ot_id"`                                         // Object Type ID
	IncludeTypeInfo    bool         `form:"include_type_info"`                             // Whether to include object type info
	IncludeLogicParams bool         `form:"include_logic_params"`                          // Include calculation parameters for logic properties, default false
	Cond               *KnCondition `json:"condition"`                                     // Retrieval conditions
	// Filters is a flat shortcut for the common "field op value [AND ...]" case.
	// When set and Cond is empty, the driven adapter AND-combines them into Cond
	// (value_from defaults to const). Mutually exclusive with condition; condition
	// wins if both are provided.
	Filters            []FlatFilter `json:"filters,omitempty"`
	Limit              int          `json:"limit" validate:"min=1,max=10000" default:"10"` // Quantity limit, default 10, range 1-10000
	Properties         []string     `json:"properties"`                                    // 指定返回的对象属性字段列表，默认返回所有属性
	// SearchAfter 游标分页：传入上一页响应返回的 search_after，用于顺序拉取下一页；首次查询留空。
	// 适用于对象索引 / 数据视图路径（顺翻，不跳页）。
	SearchAfter []any `json:"search_after,omitempty"`
	// Offset 偏移翻页：适用于资源（vega 表源）路径，支持跳到任意页；与 search_after 互斥。
	Offset int `json:"offset,omitempty"`
}

// FlatFilter is a single field-op-value comparison used by
// QueryObjectInstancesReq.Filters. Multiple filters are AND-combined into a
// condition by the driven adapter.
type FlatFilter struct {
	Field string          `json:"field"` // Object type property name
	Op    KnOperationType `json:"op"`    // Comparison operator
	Value any             `json:"value"` // Field value (array for in/not_in)
}

type QueryObjectInstancesResp struct {
	Data          []any          `json:"datas"`                 // List of object instances
	ObjectConcept map[string]any `json:"object_type,omitempty"` // Object type definition，由 req.include_type_info 控制是否返回
	TotalCount    int64          `json:"total_count,omitempty"` // 命中总数（need_total 时有效）
	// SearchAfter 下一页游标：非空时把它作为下次请求的 search_after 传入以取下一页；为空表示无更多数据。
	SearchAfter []any `json:"search_after,omitempty"`
}

// QueryLogicPropertiesReq Request for querying logic properties values
type QueryLogicPropertiesReq struct {
	KnID               string                   `json:"kn_id"`
	OtID               string                   `json:"ot_id"`
	InstanceIdentities []map[string]interface{} `json:"_instance_identities"`
	Properties         []string                 `json:"properties"`
	DynamicParams      map[string]interface{}   `json:"dynamic_params"`
}

// QueryLogicPropertiesResp Response for querying logic properties values
type QueryLogicPropertiesResp struct {
	Datas []map[string]interface{} `json:"datas"`
}

// QueryInstanceSubgraphReq Subgraph query request
type QueryInstanceSubgraphReq struct {
	// Path parameters
	KnID string `form:"kn_id"`

	// Query parameters
	IncludeLogicParams bool `form:"include_logic_params"`

	// Body parameters - use interface{} to avoid explicit struct definition
	// Corresponds to SubGraphQueryBaseOnTypePath struct in ontology-query interface
	RelationTypePaths interface{} `json:"relation_type_paths"`
}

// QueryInstanceSubgraphResp Subgraph query response
type QueryInstanceSubgraphResp struct {
	// Use interface{} to directly return the original structure from the underlying interface
	// Corresponds to PathEntries struct in ontology-query interface
	Entries interface{} `json:"entries"`
}

// DrivenOntologyQuery Ontology query interface
type DrivenOntologyQuery interface {
	// QueryObjectInstances retrieves detailed data of objects for a specified object class
	QueryObjectInstances(ctx context.Context, req *QueryObjectInstancesReq) (resp *QueryObjectInstancesResp, err error)
	// QueryLogicProperties queries logic property values
	QueryLogicProperties(ctx context.Context, req *QueryLogicPropertiesReq) (resp *QueryLogicPropertiesResp, err error)
	// QueryActions queries actions
	QueryActions(ctx context.Context, req *QueryActionsRequest) (resp *QueryActionsResponse, err error)
	// ExecuteActions executes an action type (async), returning an execution id
	ExecuteActions(ctx context.Context, req *ExecuteActionsRequest) (resp *ExecuteActionsResponse, err error)
	// GetActionExecution retrieves a single execution's status and results by execution id
	GetActionExecution(ctx context.Context, req *GetActionExecutionRequest) (resp map[string]any, err error)
	// ListActionExecutions lists action execution history with optional filters and pagination
	ListActionExecutions(ctx context.Context, req *ListActionExecutionsRequest) (resp map[string]any, err error)
	// QueryInstanceSubgraph queries object subgraph
	QueryInstanceSubgraph(ctx context.Context, req *QueryInstanceSubgraphReq) (resp *QueryInstanceSubgraphResp, err error)
}
