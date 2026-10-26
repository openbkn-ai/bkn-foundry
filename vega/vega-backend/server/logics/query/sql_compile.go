// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/binding"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/planning"
)

// PrepareSQLQuery 通过正式服务端口解析、授权、绑定和规划，不创建连接或执行 SQL。
// 返回查询及独立的依赖快照，由调用层会话持有；条件节点及视图在生成 SQL 前统一绑定。当前不切换 HTTP 入口。
func PrepareSQLQuery(ctx context.Context, sql string, resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*qm.SelectBuilder, []qm.Dependency, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	tree, err := compiler.ParseSQL(sql)
	if err != nil {
		return nil, nil, err
	}
	input, err := compiler.ConvertToIR(tree)
	if err != nil {
		return nil, nil, err
	}
	return prepareQuery(ctx, input, true, resources, catalogs)
}

// PrepareResourceDataQuery 将已通过接口形状校验的 DTO 接入相同的绑定和规划流程。
// API 分页及读取选项沿用原请求，由既有查询会话管理。
func PrepareResourceDataQuery(ctx context.Context, id string, params *interfaces.ResourceDataQueryParams,
	resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*qm.SelectBuilder, []qm.Dependency, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	input, err := binding.BuildResourceDataQuery(params, id)
	if err != nil {
		return nil, nil, err
	}
	return prepareQuery(ctx, input, params.IgnoreLocalIndex != nil && *params.IgnoreLocalIndex, resources, catalogs)
}

func prepareQuery(ctx context.Context, input *qm.SelectBuilder, ignoreLocalIndex bool,
	resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*qm.SelectBuilder, []qm.Dependency, error) {
	resolver, err := newSQLQueryResolver(ctx, resources, catalogs)
	if err != nil {
		return nil, nil, err
	}
	resolver.ignoreLocalIndex = ignoreLocalIndex
	semantics, err := binding.NewSemanticContext(common.ServerTimeZone())
	if err != nil {
		return nil, nil, interfaces.NewServerTimeZoneError()
	}
	bound, err := binding.Bind(ctx, input, semantics, resolver.identity, resolver, resolver)
	if err != nil {
		return nil, nil, err
	}
	// 目录配置是绑定原生类型及选择目标的共同依赖；不从连接凭据生成指纹。
	for _, id := range resolver.catalogOrder {
		catalog := resolver.catalogs[id]
		resolver.dependencies = append(resolver.dependencies, qm.Dependency{Kind: qm.DependencyCatalog, ID: id,
			Version: catalogRevision(catalog), ConfigRevision: catalogRevision(catalog)})
	}
	planned, err := planning.Plan(ctx, bound, resolver.identity, resolver)
	if err != nil {
		return nil, nil, err
	}
	resolver.dependencies = append(resolver.dependencies, qm.Dependency{Kind: qm.DependencyConfig, ID: "query_semantics", Version: semantics.Version, ConfigRevision: semantics.ConfigFingerprint})
	return planned, resolver.dependencies, nil
}
