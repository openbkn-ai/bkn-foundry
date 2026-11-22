// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// APIResourceService 由扩展实现 API Resource 的保存准备和单页查询；Core 保留通用持久化。
//
//go:generate mockgen -source ../interfaces/api_resource_service.go -destination ../interfaces/mock/mock_api_resource_service.go
type APIResourceService interface {
	// PrepareCreate 接受 Core 已分配标识和审计字段的候选对象，返回验证通过的存储副本。
	PrepareCreate(ctx context.Context, candidate *Resource, req *ResourceRequest) (*Resource, error)
	// PrepareUpdate 接受已加载的存储对象，完成 API 权限、验证及保护后返回存储副本。
	PrepareUpdate(ctx context.Context, current *Resource, req *ResourceRequest) (*Resource, error)
	// Query 使用独立的私有定义执行一次无会话查询，不改变存储对象。
	Query(ctx context.Context, resource *Resource, params *ResourceDataQueryParams) (*ResourceDataQueryResult, error)
}
