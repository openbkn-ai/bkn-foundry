// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	cond "bkn-backend/common/condition"
)

const (
	// Action resource types.
	ACTION_SOURCE_TYPE_TOOL = "tool"
	ACTION_SOURCE_TYPE_MCP  = "mcp"

	// Action types.
	ACTION_TYPE_ADD    = "add"
	ACTION_TYPE_MODIFY = "modify"
	ACTION_TYPE_DELETE = "delete"
)

var (
	ACTION_TYPE_SORT = map[string]string{
		"name":        "f_name",
		"update_time": "f_update_time",
	}

	// Action types.
	ActionTypeMap = map[string]bool{
		ACTION_TYPE_ADD:    true,
		ACTION_TYPE_MODIFY: true,
		ACTION_TYPE_DELETE: true,
	}

	// Action impact operation types.
	ExpectedOperationMap = map[string]bool{
		ExpectedOperationAdd:    true,
		ExpectedOperationModify: true,
		ExpectedOperationDelete: true,
	}

	// Action condition operators.
	ActionCondOperationMap = map[string]struct{}{
		cond.OperationAnd:        {},
		cond.OperationOr:         {},
		cond.OperationEq:         {},
		cond.OperationNotEq:      {},
		cond.OperationGt:         {},
		cond.OperationGte:        {},
		cond.OperationLt:         {},
		cond.OperationLte:        {},
		cond.OperationIn:         {},
		cond.OperationNotIn:      {},
		cond.OperationEmpty:      {},
		cond.OperationNotEmpty:   {},
		cond.OperationTrue:       {},
		cond.OperationFalse:      {},
		cond.OperationRange:      {},
		cond.OperationOutRange:   {},
		cond.OperationBefore:     {},
		cond.OperationBetween:    {},
		cond.OperationExist:      {},
		cond.OperationNotExist:   {},
		cond.OperationLike:       {},
		cond.OperationNotLike:    {},
		cond.OperationPrefix:     {},
		cond.OperationNotPrefix:  {},
		cond.OperationNull:       {},
		cond.OperationNotNull:    {},
		cond.OperationRegex:      {},
		cond.OperationContain:    {},
		cond.OperationNotContain: {},
		cond.OperationCurrent:    {},
	}
)

const (
	ExpectedOperationAdd    string = "add"
	ExpectedOperationModify string = "modify"
	ExpectedOperationDelete string = "delete"
)

// ImpactContractItem represents one contract in impact_contracts.
type ImpactContractItem struct {
	ObjectTypeID      string   `json:"object_type_id,omitempty" mapstructure:"object_type_id"`
	ExpectedOperation string   `json:"expected_operation,omitempty" mapstructure:"expected_operation"`
	Description       string   `json:"description,omitempty" mapstructure:"description"`
	AffectedFields    []string `json:"affected_fields,omitempty" mapstructure:"affected_fields"`
}

type ActionTypeWithKeyField struct {
	ATID         string           `json:"id" mapstructure:"id"`
	ATName       string           `json:"name" mapstructure:"name"`
	ActionType   string           `json:"action_type" mapstructure:"action_type"`
	ActionIntent string           `json:"action_intent,omitempty" mapstructure:"action_intent"`
	ObjectTypeID string           `json:"object_type_id" mapstructure:"object_type_id"`
	ObjectType   SimpleObjectType `json:"object_type,omitempty" mapstructure:"object_type"` // Bound object type for display
	Condition    *ActionCondCfg   `json:"condition,omitempty" mapstructure:"condition"`
	Affect       *ActionAffect    `json:"affect" mapstructure:"affect"`
	// ImpactContracts and the legacy affect field are mutually exclusive. When only affect is supplied,
	// validation adds one contract with expected_operation from action_type and retains affect.
	ImpactContracts []ImpactContractItem `json:"impact_contracts,omitempty" mapstructure:"impact_contracts"`
	ActionSource    ActionSource         `json:"action_source" mapstructure:"action_source"`
	Parameters      []Parameter          `json:"parameters" mapstructure:"parameters"`
	Schedule        Schedule             `json:"schedule" mapstructure:"schedule"`
}

// knowledge_network
type ActionType struct {
	ActionTypeWithKeyField `mapstructure:",squash"`
	CommonInfo             `mapstructure:",squash"`
	KNID                   string `json:"kn_id" mapstructure:"kn_id"`
	Branch                 string `json:"branch" mapstructure:"branch"`

	Creator    AccountInfo `json:"creator" mapstructure:"creator"`
	CreateTime int64       `json:"create_time" mapstructure:"create_time"`
	Updater    AccountInfo `json:"updater" mapstructure:"updater"`
	UpdateTime int64       `json:"update_time" mapstructure:"update_time"`

	ModuleType string `json:"module_type" mapstructure:"module_type"`

	IfNameModify bool `json:"-"`
	// Vector.
	Vector []float32 `json:"_vector,omitempty"`
	Score  *float64  `json:"_score,omitempty"` // OpenSearch score used by concept search
}

type ActionSource struct {
	Type string `json:"type" mapstructure:"type"`
	// Mutually exclusive fields selected by Type.
	// Type is tool.
	BoxID  string `json:"box_id,omitempty" mapstructure:"box_id"`
	ToolID string `json:"tool_id,omitempty" mapstructure:"tool_id"`
	// Type is mcp.
	McpID    string `json:"mcp_id,omitempty" mapstructure:"mcp_id"`
	ToolName string `json:"tool_name,omitempty" mapstructure:"tool_name"`
}

type ActionCondCfg struct {
	ObjectTypeID string `json:"object_type_id,omitempty" mapstructure:"object_type_id"` // Object type identified by the action condition

	Field            string           `json:"field,omitempty" mapstructure:"field"`
	Operation        string           `json:"operation,omitempty" mapstructure:"operation"`
	SubConds         []*ActionCondCfg `json:"sub_conditions,omitempty" mapstructure:"sub_conditions"`
	cond.ValueOptCfg `mapstructure:",squash"`

	RemainCfg map[string]any `json:",omitempty" mapstructure:",remain,squash"`

	NameField *ViewField `json:"-" mapstructure:"-"`
}

type ActionAffect struct {
	ObjectTypeID string           `json:"object_type_id,omitempty" mapstructure:"object_type_id"` // Object type affected by the action
	ObjectType   SimpleObjectType `json:"object_type,omitempty" mapstructure:"object_type"`
	Comment      string           `json:"comment,omitempty" mapstructure:"comment"`
	// Aligns with the single-entry semantics of ImpactContractItem during the affect transition.
	ExpectedOperation string   `json:"expected_operation,omitempty" mapstructure:"expected_operation"`
	AffectedFields    []string `json:"affected_fields,omitempty" mapstructure:"affected_fields"`
}

type Schedule struct {
	Type       string `json:"type" mapstructure:"type"`
	Expression string `json:"expression" mapstructure:"expression"`
}

// Object type pagination query.
type ActionTypesQueryParams struct {
	PaginationQueryParameters
	NamePattern   string
	Tag           string
	Branch        string
	KNID          string
	ObjectTypeIDs []string
	ActionType    string
}

// Action type search list.
type ActionTypes struct {
	Entries    []*ActionType `json:"entries"`
	TotalCount int64         `json:"total_count,omitempty"`
	NextCursor *string       `json:"next_cursor,omitempty"`
	OverallMs  int64         `json:"overall_ms"`
}

func IsValidActionSourceType(m string) bool {
	return m == ACTION_SOURCE_TYPE_TOOL || m == ACTION_SOURCE_TYPE_MCP
}

// IsValidActionTypeIntentValue reports whether s is a valid action_intent aligned with action_type.
func IsValidActionTypeIntentValue(s string) bool {
	return ActionTypeMap[s]
}

// IsValidExpectedOperation reports whether s is a valid enum value.
func IsValidExpectedOperation(s string) bool {
	return ExpectedOperationMap[s]
}
