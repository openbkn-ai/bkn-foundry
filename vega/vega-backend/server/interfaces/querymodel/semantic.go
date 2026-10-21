// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

import "time"

const (
	CurrentIRVersion        = 1
	CurrentProtocolVersion  = 1
	CurrentSemanticsVersion = "vega-query-semantics-v1"
)

// SemanticContext 在绑定前确定，同次准备、count、读页及重试都必须复用。
// TimeZone 来自服务端 TZ（空值回退 UTC），不接受客户端时区覆盖。
type SemanticContext struct {
	Version           string
	TimeZone          string
	TimeZoneRevision  string
	TimeZoneRules     string
	Location          *time.Location
	Comparison        ComparisonPolicy
	TypeRulesVersion  string
	ConfigFingerprint string
}

type ComparisonPolicy struct {
	StringRule               string
	CaseSensitive            bool
	TrailingSpaceSignificant bool
}

type EvaluationKind string

const (
	EvaluationPure      EvaluationKind = "pure"
	EvaluationStable    EvaluationKind = "stable"
	EvaluationAggregate EvaluationKind = "aggregate"
	EvaluationVolatile  EvaluationKind = "volatile"
)

type FunctionID string

type RuntimeSlotID uint64

type RuntimeValueSource string

const (
	RuntimeQueryStart RuntimeValueSource = "query_start"
	RuntimeIdentity   RuntimeValueSource = "identity"
)

// RuntimeSlot 是声明，不含本次执行值；不得把 NOW 的结果写入可复用制品。
type RuntimeSlot struct {
	ID                RuntimeSlotID
	Type              LogicalType
	Nullable          bool
	Evaluation        EvaluationKind
	Function          FunctionID
	Source            RuntimeValueSource
	IdentityAttribute string
	Origin            Origin
}

type RuntimeValue struct {
	Slot  RuntimeSlotID
	Value Value
}
