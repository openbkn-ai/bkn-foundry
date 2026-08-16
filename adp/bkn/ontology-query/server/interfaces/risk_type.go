// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import cond "ontology-query/common/condition"

const (
	RISK_RULE_WHEN_TYPE_CONDITION = "condition"

	// Built-in risk assessment tool.
	BuiltinToolBoxID  = "bkn-internal_risk-assessment"
	BuiltinToolToolID = "bkn_common_risk_assessment_tool"
)

// RiskType defines a risk category used for RiskType assessment.
type RiskType struct {
	RTID               string        `json:"id"`
	RTName             string        `json:"name"`
	MaxAcceptableLevel string        `json:"max_acceptable_level"`
	Parameters         []Parameter   `json:"parameters"`
	RiskRules          []RiskRule    `json:"risk_rules"`
	RiskFunction       *RiskFunction `json:"risk_function"`
}

// RiskRule defines a risk rule.
type RiskRule struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	When        *RiskRuleWhen `json:"when"`
	Decision    string        `json:"decision"`
	Message     string        `json:"message"`
}

// RiskRuleWhen defines a match condition.
type RiskRuleWhen struct {
	Type            string        `json:"type"`
	Condition       *cond.CondCfg `json:"condition,omitempty"`
	NaturalLanguage string        `json:"natural_language,omitempty"`
}

// RiskFunction defines a risk assessment function.
type RiskFunction struct {
	Type       string      `json:"type"`
	BoxID      string      `json:"box_id,omitempty"`
	ToolID     string      `json:"tool_id,omitempty"`
	McpID      string      `json:"mcp_id,omitempty"`
	ToolName   string      `json:"tool_name,omitempty"`
	Parameters []Parameter `json:"parameters,omitempty"` // Flat list whose source marks path/query/header/body placement.
}
