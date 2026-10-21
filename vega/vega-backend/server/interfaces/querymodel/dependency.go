// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package querymodel

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

const (
	DependencyResource DependencyKind = "resource"
	DependencyCatalog  DependencyKind = "catalog"
	DependencyView     DependencyKind = "view"
	DependencyConfig   DependencyKind = "config"
	DependencySchema   DependencyKind = "schema"
)

type Dependency struct {
	Kind              DependencyKind
	ID                string
	Version           string
	SchemaFingerprint string
	ConfigRevision    string
	Authorization     []AuthorizationBasis
}

type ViewAuthorizationMode string

const (
	ViewAuthorizationInvoker   ViewAuthorizationMode = "invoker"
	ViewAuthorizationDelegated ViewAuthorizationMode = "delegated"
)

// v1 无受支持的委托配置时只能采用 invoker；DelegationRef 不包含凭据。
type ViewAuthorization struct {
	Mode          ViewAuthorizationMode
	DelegationRef string
}
