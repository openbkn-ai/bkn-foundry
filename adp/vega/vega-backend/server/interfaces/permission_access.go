// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package interfaces

import (
	"context"
	"errors"
)

// ErrBulkAuthzUnsupported is returned by AccessibleResourceIDs when the
// underlying permission backend has no bulk accessible-id resolver; callers
// fall back to per-resource filtering.
var ErrBulkAuthzUnsupported = errors.New("bulk accessible-resource resolution unsupported")

const (
	// 访问者类型
	ACCESSOR_TYPE_USER = "user"
	ACCESSOR_TYPE_APP  = "app"

	// 创建时无资源id，用 * 表示
	RESOURCE_ID_ALL = "*"

	// 资源类型
	AUTH_RESOURCE_TYPE_CATALOG        = "catalog"
	AUTH_RESOURCE_TYPE_RESOURCE       = "resource"
	AUTH_RESOURCE_TYPE_CONNECTOR_TYPE = "connector_type"

	// 内部资源类型：系统内部 catalog 及其下资源按独立类型注册，
	// 业务角色的 catalog:*/resource:* 通配授权匹配不到，仅超级管理员（* 通配）可见
	AUTH_RESOURCE_TYPE_INTERNAL_CATALOG  = "internal_catalog"
	AUTH_RESOURCE_TYPE_INTERNAL_RESOURCE = "internal_resource"

	// 资源操作类型
	OPERATION_TYPE_VIEW_DETAIL = "view_detail"
	OPERATION_TYPE_CREATE      = "create"
	OPERATION_TYPE_MODIFY      = "modify"
	OPERATION_TYPE_DELETE      = "delete"
	OPERATION_TYPE_AUTHORIZE   = "authorize"
	OPERATION_TYPE_TASK_MANAGE = "task_manage"

	// 更新资源名称的topic
	AUTHORIZATION_RESOURCE_NAME_MODIFY = "authorization.resource.name.modify"
)

var (
	COMMON_OPERATIONS = []string{
		OPERATION_TYPE_VIEW_DETAIL,
		OPERATION_TYPE_CREATE,
		OPERATION_TYPE_MODIFY,
		OPERATION_TYPE_DELETE,
		OPERATION_TYPE_AUTHORIZE,
		OPERATION_TYPE_TASK_MANAGE,
	}
)

// 检查权限
type PermissionCheck struct {
	Accessor   PermissionAccessor `json:"accessor"`
	Resource   PermissionResource `json:"resource"`
	Operations []string           `json:"operation"`
	Method     string             `json:"method"`
}

// 检查权限结果
type PermissionCheckResult struct {
	Result bool `json:"result"`
}

// 访问者信息
type PermissionAccessor struct {
	Type string `json:"type,omitempty"` // 分 user: 实名， app: 应用账户
	ID   string `json:"id,omitempty"`   // 用户ID
}

// 资源信息
type PermissionResource struct {
	Type string `json:"type,omitempty"` // 资源类型
	ID   string `json:"id,omitempty"`   // 资源ID
	Name string `json:"name,omitempty"` // 资源名称
}

// 过滤/删除
type PermissionResourcesFilter struct {
	Accessor       PermissionAccessor   `json:"accessor,omitempty"`
	Resources      []PermissionResource `json:"resources,omitempty"`
	Operations     []string             `json:"operation,omitempty"`
	AllowOperation bool                 `json:"allow_operation"`
	Method         string               `json:"method,omitempty"`
}

// 设置权限
type PermissionPolicy struct {
	Accessor   PermissionAccessor  `json:"accessor"`
	Resource   PermissionResource  `json:"resource"`
	Operations PermissionPolicyOps `json:"operation"`
	Condition  string              `json:"condition"`
	ExpiresAt  string              `json:"expires_at,omitempty"`
}

type PermissionPolicyOps struct {
	Allow []PermissionOperation `json:"allow"`
	Deny  []PermissionOperation `json:"deny"`
}

type PermissionOperation struct {
	Operation string `json:"id"`
}

type PermissionResourceOps struct {
	ResourceID string   `json:"id"`
	Operations []string `json:"operation,omitempty"`
}

// OpAccess describes an accessor's grant for one operation on one resource type.
// All is true when a type-wide / wildcard grant covers every instance (in which
// case IDs is nil); otherwise IDs holds the concrete resource ids the accessor
// may perform the operation on.
type OpAccess struct {
	All bool
	IDs map[string]bool
}

// AccessibleResourceLister is an OPTIONAL PermissionAccess capability: resolve,
// per operation, the accessor's accessible resource ids (or a wildcard flag) in
// ONE bulk round-trip per op — instead of a per-resource permission fan-out.
// Callers detect support via a type assertion and fall back to FilterResources
// when it is absent. This is what lets resource listing scale to accounts that
// hold grants across the whole catalog (see issue #357).
type AccessibleResourceLister interface {
	AccessibleResourceIDs(ctx context.Context, accessorID, resourceType string, ops []string) (map[string]OpAccess, error)
}

//go:generate mockgen -source ../interfaces/permission_access.go -destination ../interfaces/mock/mock_permission_access.go
type PermissionAccess interface {
	CheckPermission(ctx context.Context, check PermissionCheck) (bool, error)
	FilterResources(ctx context.Context, filter PermissionResourcesFilter) (map[string]PermissionResourceOps, error)

	CreateResources(ctx context.Context, policies []PermissionPolicy) error
	DeleteResources(ctx context.Context, resources []PermissionResource) error
}
