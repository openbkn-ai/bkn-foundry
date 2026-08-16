// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import cond "ontology-query/common/condition"

// Action query request body.
type ActionQuery struct {
	InstanceIdentities []map[string]any `json:"_instance_identities,omitempty"`
	DynamicParams      map[string]any   `json:"dynamic_params,omitempty"`

	KNID         string `json:"-"`
	Branch       string `json:"-"`
	ActionTypeID string `json:"-"`
	CommonQueryParameters
}

// Action query response body.
type Actions struct {
	ActionType   *ActionType   `json:"action_type,omitempty"`
	ActionSource ActionSource  `json:"action_source"`
	Actions      []ActionParam `json:"actions"`
	TotalCount   int           `json:"total_count,omitempty"`
	OverallMs    int64         `json:"overall_ms"`
}

// Instantiated action parameters.
type ActionParam struct {
	InstanceID       any            `json:"_instance_id,omitempty"`       // Instance ID.
	InstanceIdentity any            `json:"_instance_identity,omitempty"` // Unique instance identity.
	Display          any            `json:"display,omitempty"`            // Display value.
	Parameters       map[string]any `json:"parameters"`                   // Parameters populated with actual arguments.
	DynamicParams    map[string]any `json:"dynamic_params"`               // Dynamic parameter map.
}

// ExpectedOperation describes the operation semantics expected by the contract.
// Its values align with action_type/action_intent and the corresponding bkn-backend type.
type ExpectedOperation string

const (
	ExpectedOperationAdd    string = "add"
	ExpectedOperationModify string = "modify"
	ExpectedOperationDelete string = "delete"
)

// ImpactContractItem corresponds to a bkn-backend action-impact contract item and aligns with action-type rebuilding.
type ImpactContractItem struct {
	ObjectTypeID      string   `json:"object_type_id,omitempty"`
	ExpectedOperation string   `json:"expected_operation,omitempty"`
	Description       string   `json:"description,omitempty"`
	AffectedFields    []string `json:"affected_fields,omitempty"`
}

type ActionType struct {
	ATID            string               `json:"id"`
	ATName          string               `json:"name"`
	ActionType      string               `json:"action_type"`
	ActionIntent    string               `json:"action_intent,omitempty"`
	ObjectTypeID    string               `json:"object_type_id"`
	ImpactContracts []ImpactContractItem `json:"impact_contracts,omitempty"`
	Condition       *cond.CondCfg        `json:"condition,omitempty"`
	Affect          *ActionAffect        `json:"affect"`
	ActionSource    ActionSource         `json:"action_source"`
	Parameters      []Parameter          `json:"parameters"`
	Schedule        Schedule             `json:"schedule"`
}

type ActionAffect struct {
	ObjectTypeID string `json:"object_type_id,omitempty"`
	Comment      string `json:"comment,omitempty"`
}

type ActionSource struct {
	Type string `json:"type" mapstructure:"type"`
	// Mutually exclusive fields selected by Type.
	// type is tool.
	BoxID  string `json:"box_id,omitempty"`
	ToolID string `json:"tool_id,omitempty"`
	// type is mcp.
	McpID    string `json:"mcp_id,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
}

type Schedule struct {
	Type       string `json:"type"`
	Expression string `json:"expression"`
}
