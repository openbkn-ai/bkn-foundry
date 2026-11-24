// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import cond "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common/condition"

// Action query request body.
type ActionQuery struct {
	RelationTarget     *ActionRelationTarget `json:"relation_target,omitempty"`
	ObjectInput        map[string]any        `json:"object_input,omitempty"`
	InstanceIdentities []map[string]any      `json:"_instance_identities,omitempty"`
	DynamicParams      map[string]any        `json:"dynamic_params,omitempty"`

	KNID         string `json:"-"`
	Branch       string `json:"-"`
	ActionTypeID string `json:"-"`
	CommonQueryParameters
}

// Action query response body.
type Actions struct {
	ExecutionSchema map[string]any `json:"execution_schema"`
	ActionType      *ActionType    `json:"action_type,omitempty"`
	ActionSource    ActionSource   `json:"action_source"`
	Actions         []ActionParam  `json:"actions"`
	TotalCount      int            `json:"total_count,omitempty"`
	OverallMs       int64          `json:"overall_ms"`
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
	OperationConfig *ActionOperationConfig `json:"operation_config"`
	ATID            string                 `json:"id"`
	ATName          string                 `json:"name"`
	Comment         string                 `json:"comment,omitempty"`
	ActionType      string                 `json:"action_type"`
	ActionIntent    string                 `json:"action_intent,omitempty"`
	ObjectTypeID    string                 `json:"object_type_id"`
	ImpactContracts []ImpactContractItem   `json:"impact_contracts,omitempty"`
	Condition       *cond.CondCfg          `json:"condition,omitempty"`
	Affect          *ActionAffect          `json:"affect"`
	ActionSource    ActionSource           `json:"action_source"`
	Parameters      []Parameter            `json:"parameters"`
	Schedule        Schedule               `json:"schedule"`

	// RuleVersion and ExecutionSubject are kept by bkn-backend for a record-event action: the
	// version of its rule, and the person it checks as. Both are read-only here.
	RuleVersion      string       `json:"rule_version,omitempty"`
	ExecutionSubject *AccountInfo `json:"execution_subject,omitempty"`
}

// Operation categories of an action type.
const (
	ActionOperationObject   = "object"
	ActionOperationRelation = "relation"
	ActionOperationTool     = "tool"
	// ActionOperationEvent records events: the action selects the objects that meet its rule at
	// each check and records them in the event ledger of its event class.
	ActionOperationEvent = "event"
)

// Value sources only a record-event action uses, in its condition and in the parameters of its
// targets: the check time, and the check day in the schedule's time zone. The value is an optional
// ISO 8601 duration added to either, such as -PT48H or -P3D.
const (
	ActionValueFromCheckTime = "check_time"
	ActionValueFromCheckDate = "check_date"
)

// ActionOperationConfig is the single-operation model published by bkn-backend.
type ActionOperationConfig struct {
	Type                 string        `json:"type"`
	RelationTypeID       string        `json:"relation_type_id,omitempty"`
	RelationOperation    string        `json:"relation_operation,omitempty"`
	AnchorRole           string        `json:"anchor_role,omitempty"`
	OtherObjectCondition *cond.CondCfg `json:"other_object_condition,omitempty"`
	// EventClass is the event class a record-event action records events of.
	EventClass string `json:"event_class,omitempty"`
	// Targets is the basis a record-event action selects its objects by when it is not its
	// condition: exactly one of a Cypher query, a function or a metric.
	Targets *ActionEventTargets `json:"targets,omitempty"`
}

// ActionEventTargets is the basis of a record-event action other than its condition.
type ActionEventTargets struct {
	Cypher   *ActionCypherTargets   `json:"cypher,omitempty"`
	Function *ActionFunctionTargets `json:"function,omitempty"`
	Metric   *ActionMetricTargets   `json:"metric,omitempty"`
}

// ActionTargetParameter is one parameter of a Cypher query or a function: a constant, or the
// check time or day shifted by the duration in Value.
type ActionTargetParameter struct {
	ValueFrom string `json:"value_from"`
	Value     any    `json:"value,omitempty"`
}

// ActionCypherTargets selects the objects a Cypher query returns.
type ActionCypherTargets struct {
	Query      string                           `json:"query"`
	Parameters map[string]ActionTargetParameter `json:"parameters,omitempty"`
}

// ActionFunctionTargets selects the objects a Cypher function returns as a table.
type ActionFunctionTargets struct {
	BoxID      string                           `json:"box_id"`
	ToolID     string                           `json:"tool_id"`
	Parameters map[string]ActionTargetParameter `json:"parameters,omitempty"`
}

// ActionMetricTargets selects the groups of a published metric version whose value compares
// with the threshold by the operator. SubjectKey is the object an ungrouped metric is about.
type ActionMetricTargets struct {
	MetricID   string  `json:"metric_id"`
	Version    int     `json:"version"`
	Operator   string  `json:"operator"`
	Value      float64 `json:"value"`
	SubjectKey string  `json:"subject_key,omitempty"`
}

// ActionRelationTarget specifies one directed pair, never two lists to cross join.
type ActionRelationTarget struct {
	SourceIdentity map[string]any `json:"source_identity"`
	TargetIdentity map[string]any `json:"target_identity"`
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
	// Timezone is the IANA zone the schedule, and a record-event action's check day, follow.
	Timezone string `json:"timezone,omitempty"`
}
