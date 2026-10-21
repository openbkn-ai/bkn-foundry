// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

import "time"

// ExecutionPlan 是已通过单源检查的查询及其目标映射。
// 当前不引入片段编号、多源调度或另一份 IR/Schema 副本。
type ExecutionPlan struct {
	Query         *BoundQuery
	TargetBinding TargetBinding
}

// ExecutionTarget 的字段共同标识同一受控执行环境；host/type 相同不足以证明同源。
// ConnectionProfile 和 EffectiveSourceIdentity 仅为受控引用，不包含凭据。
type ExecutionTarget struct {
	CatalogID               string
	ConnectorType           string
	PhysicalTargetKey       string
	ConnectionProfile       string
	EffectiveSourceIdentity string
	AuthorizationNamespace  string
	SchemaRevision          string
	ConfigRevision          string
}

// PhysicalIdentifier 是分段标识符，不能存放 SQL 表达式或已拼接的查询文本。
type PhysicalIdentifier []string

type RelationBinding struct {
	Instance   RelationInstanceID
	ResourceID string
	Identifier PhysicalIdentifier
}

type MissingValuePolicy string

const (
	MissingAsNull   MissingValuePolicy = "as_null"
	MissingDistinct MissingValuePolicy = "distinct"
)

type FieldBinding struct {
	Address      FieldAddress
	Identifier   PhysicalIdentifier
	NativeType   NativeType
	MissingValue MissingValuePolicy
}

type QuerySource string

const (
	QuerySourceSource QuerySource = "source"
	QuerySourceIndex  QuerySource = "index"
	QuerySourceAuto   QuerySource = "auto"
)

type TargetFreshness struct {
	Revision string
	AsOf     *time.Time
}

type TargetBinding struct {
	Target      ExecutionTarget
	Source      QuerySource
	Relations   []RelationBinding
	Fields      []FieldBinding
	Freshness   TargetFreshness
	Fingerprint string
}
