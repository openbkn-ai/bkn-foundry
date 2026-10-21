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

// CompileSQLQuery 通过正式服务端口解析、授权、绑定和规划，不创建连接或执行 SQL。
// HTTP 执行入口在 Connector Prepare/Execute 接入后切换，当前不替换既有执行链路。
func CompileSQLQuery(ctx context.Context, sql string, resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*qm.ExecutionPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tree, err := compiler.ParseSQL(sql)
	if err != nil {
		return nil, err
	}
	input, err := compiler.ConvertToIR(tree)
	if err != nil {
		return nil, err
	}
	return compileQueryPlan(ctx, input, qm.ExecutionOptions{QuerySource: qm.QuerySourceSource}, resources, catalogs)
}

// CompileResourceDataQuery 将已通过接口形状校验的 DTO 接入相同的绑定和规划流程。
// 返回的选项独立承载 API 分页；原生 DSL/文件查询不经过此 SQL 目标解析链路。
func CompileResourceDataQuery(ctx context.Context, id string, params *interfaces.ResourceDataQueryParams,
	resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*qm.ExecutionPlan, qm.ExecutionOptions, error) {
	if err := ctx.Err(); err != nil {
		return nil, qm.ExecutionOptions{}, err
	}
	input, options, err := binding.BuildResourceDataQuery(params, id)
	if err != nil {
		return nil, qm.ExecutionOptions{}, err
	}
	plan, err := compileQueryPlan(ctx, input, options, resources, catalogs)
	return plan, options, err
}

func compileQueryPlan(ctx context.Context, input *qm.UnboundQuery, options qm.ExecutionOptions,
	resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*qm.ExecutionPlan, error) {
	resolver, err := newSQLQueryResolver(ctx, resources, catalogs)
	if err != nil {
		return nil, err
	}
	semantics, err := binding.NewSemanticContext(common.ServerTimeZone())
	if err != nil {
		return nil, interfaces.NewServerTimeZoneError()
	}
	bound, err := binding.Bind(ctx, input, semantics, resolver.identity, resolver, resolver)
	if err != nil {
		return nil, err
	}
	// 目录配置是绑定原生类型及选择目标的共同依赖；不从连接凭据生成指纹。
	for _, id := range resolver.catalogOrder {
		catalog := resolver.catalogs[id]
		bound.Dependencies = append(bound.Dependencies, qm.Dependency{Kind: qm.DependencyCatalog, ID: id,
			Version: catalogRevision(catalog), ConfigRevision: catalogRevision(catalog)})
	}
	return planning.Plan(ctx, bound, resolver.identity, options, resolver)
}
