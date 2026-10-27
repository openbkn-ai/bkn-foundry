// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler"
)

// PrepareSQLBuilder 完成资源授权、物理表名解析及单源检查，不接入执行入口。
// 物理表的字段、投影、别名和通配符保持输入含义；View 字段映射由其展开逻辑负责。
// 仅支持 MySQL 物理表；类型、函数和目标能力检查仍需在执行接入前完成。
func PrepareSQLBuilder(ctx context.Context, sql string, resources interfaces.ResourceService, catalogs interfaces.CatalogService) (*sb.SelectBuilder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query, err := compiler.ParseSQL(sql)
	if err != nil {
		return nil, err
	}
	resolver, err := newSQLQueryResolver(ctx, resources, catalogs)
	if err != nil {
		return nil, err
	}
	catalogID := ""
	for _, table := range sqlBuilderTables(query) {
		id := table.ResourceID
		if resolver.authorized[id].ResourceID == "" {
			if _, err := resolver.AuthorizeSQLQueryResource(ctx, id, resolver.identity, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
				return nil, err
			}
		}
		resource, connector, err := resolver.ResolveSQLQueryResource(ctx, id, resolver.identity)
		if err != nil {
			return nil, err
		}
		if connector != interfaces.ConnectorTypeMySQL || resource.Category != interfaces.ResourceCategoryTable {
			return nil, queryResolverError(qm.ErrorPlanningUnsupported, qm.Origin{}, "当前资源准备仅支持 MySQL 物理表")
		}
		if catalogID != "" && catalogID != resource.CatalogID {
			return nil, queryResolverError(qm.ErrorPlanningUnsupported, qm.Origin{}, "当前仅支持单数据源查询")
		}
		catalogID = resource.CatalogID
		table.SourceIdentifier = resource.SourceIdentifier
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return query, nil
}

// sqlBuilderTables 按来源出现顺序遍历 ParseSQL 产出的表，包含 JOIN 和子查询。
func sqlBuilderTables(node sb.SqlNode) []*sb.Table {
	switch n := node.(type) {
	case *sb.Table:
		return []*sb.Table{n}
	case *sb.Subquery:
		return sqlBuilderTables(n.Query)
	case *sb.SelectBuilder:
		tables := sqlBuilderTables(n.FromPart)
		for _, join := range n.JoinsPart {
			tables = append(tables, sqlBuilderTables(join.Source)...)
		}
		return tables
	default:
		return nil
	}
}
