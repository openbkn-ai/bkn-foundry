// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

import "time"

const (
	DependencyResource DependencyKind = "resource"
	DependencyCatalog  DependencyKind = "catalog"
	DependencyView     DependencyKind = "view"
	DependencyConfig   DependencyKind = "config"
	DependencySchema   DependencyKind = "schema"
)

const CurrentSemanticsVersion = "vega-query-semantics-v1"

// ExecutionIdentity 保存非敏感身份引用，不携带 token 或连接凭据，也不是授权凭证。
type ExecutionIdentity struct {
	SubjectID              string
	SubjectType            string
	TenantID               string
	AuthorizationNamespace string
	AuthorizationRevision  string
}

// AuthorizationBasis 是绑定时的权限依据；执行和续页仍需检查当前权限。
type AuthorizationBasis struct {
	ResourceID string
	Operation  string
	Namespace  string
	Revision   string
}

type DependencyKind string

type Dependency struct {
	Kind              DependencyKind
	ID                string
	Version           string
	SchemaFingerprint string
	ConfigRevision    string
	Authorization     []AuthorizationBasis
}

// SemanticContext 在绑定前确定，同次准备、count、读页及重试都必须复用。
// TimeZone 来自服务端 TZ（空值回退 UTC），不接受客户端时区覆盖。
type SemanticContext struct {
	Version           string
	TimeZone          string
	Location          *time.Location
	Comparison        ComparisonPolicy
	ConfigFingerprint string
}

type ComparisonPolicy struct {
	StringRule               string
	CaseSensitive            bool
	TrailingSpaceSignificant bool
}
