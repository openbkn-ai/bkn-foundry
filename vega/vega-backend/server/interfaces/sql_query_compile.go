// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
)

// SQLQueryResourceResolver 复用既有资源元数据契约，返回值由 Binder 转换为纯查询模型。
// ConnectorType 来自可信目录元数据，不能从 SQL 或请求参数推断。
// 调用方必须先授权；实现不得向未授权身份披露资源、字段或物理信息。
type SQLQueryResourceResolver interface {
	ResolveSQLQueryResource(ctx context.Context, resourceID string,
		identity querymodel.ExecutionIdentity) (resource *Resource, connectorType string, err error)
}

// SQLQueryAuthorizer 返回可复核的权限依据，不把绑定结论当作永久授权。
type SQLQueryAuthorizer interface {
	AuthorizeSQLQueryResource(ctx context.Context, resourceID string,
		identity querymodel.ExecutionIdentity, operation string) (querymodel.AuthorizationBasis, error)
}

// SQLQueryTargetResolver 仅解析受控执行候选，不执行用户查询。
// 返回单个 Scan 的候选绑定，Planner 按完整依赖闭包检查同源并合并字段映射。
type SQLQueryTargetResolver interface {
	ResolveSQLQueryTargets(ctx context.Context, scan querymodel.BoundScan,
		identity querymodel.ExecutionIdentity, options querymodel.ExecutionOptions) ([]querymodel.TargetBinding, error)
}
