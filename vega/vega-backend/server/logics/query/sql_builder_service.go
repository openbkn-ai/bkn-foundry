// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"errors"
	"math"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

// prepareMySQLBuilder 使用已授权的资源集选择目标，严格解析后只补齐资源的源端标识。
func (rqs *rawQueryService) prepareMySQLBuilder(ctx context.Context, query *sb.SelectBuilder, catalog *interfaces.Catalog, ids []string, warnings []string) (*preparedSQLQuery, error) {
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, queryResolverForbidden(ctx)
	}
	resources := make(map[string]*interfaces.Resource)
	versions := make(map[string]int64)
	for _, table := range sqlBuilderTables(query) {
		resource := resources[table.ResourceID]
		if resource == nil {
			loaded, err := rqs.rs.InternalGetByID(ctx, nil, table.ResourceID)
			resource = loaded
			if err != nil {
				return nil, err
			}
			if resource == nil {
				return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound)
			}
			if resource.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
				return nil, queryResolverForbidden(ctx)
			}
			if _, err := resourcelogic.EnsureResourceQueryable(ctx, resource); err != nil {
				return nil, err
			}
			if resource.Category != interfaces.ResourceCategoryTable {
				return nil, mysqlQueryInvalid(ctx, "当前 SQL 查询仅支持物理表资源")
			}
			if resource.CatalogID != catalog.ID {
				return nil, mysqlCursorChanged(ctx)
			}
			resources[table.ResourceID] = resource
			versions[table.ResourceID] = resource.UpdateTime
		}
		table.SourceIdentifier = resource.SourceIdentifier
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &preparedSQLQuery{catalog: catalog, resourceIDs: ids, warnings: warnings, builder: query, resourceVersions: versions}, nil
}

// executeSQLBuilder 将读取窗口合并到临时查询根节点，保留基础 Builder 的逻辑 LIMIT 和排序。
func (rqs *rawQueryService) executeSQLBuilder(ctx context.Context, catalog *interfaces.Catalog, query *sb.SelectBuilder, options *rawSQLBuildOptions) (*interfaces.RawQueryResponse, error) {
	read := query
	if options != nil {
		if options.count {
			read = sb.Select(&sb.Alias{Expr: &sb.Function{Name: "COUNT", Args: []sb.SqlNode{&sb.Star{}}}, Name: rawQueryTotalCountColumn}).FromSelect(query, "_raw_query_total")
		} else {
			if options.offset < 0 || options.limit < 0 {
				return nil, mysqlQueryInvalid(ctx, "分页偏移和数量不能为负数")
			}
			offset, limit := uint64(options.offset), uint64(options.limit)
			if query.OffsetPart > math.MaxUint64-offset {
				return nil, mysqlQueryInvalid(ctx, "分页偏移超出源端范围")
			}
			if query.LimitPart != nil {
				if offset >= *query.LimitPart {
					limit = 0
				} else {
					limit = min(limit, *query.LimitPart-offset)
				}
			}
			page := *query
			page.OffsetPart = query.OffsetPart + offset
			page.LimitPart = &limit
			read = &page
		}
	}
	connector, err := rqs.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, connectorInitializationError(ctx, err)
	}
	defer func() { _ = connector.Close(ctx) }()
	executor, ok := connector.(interfaces.SQLBuilderExecutor)
	if !ok {
		return nil, mysqlQueryInvalid(ctx, "连接器尚未支持 SQL Builder 执行")
	}
	result, err := executor.ExecuteSQLBuilder(ctx, read)
	if err != nil {
		var forbidden *interfaces.SourceReadForbiddenError
		if errors.As(err, &forbidden) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Resource_SourceReadForbidden)
		}
		if detail, ok := interfaces.RequestSideQueryError(err); ok {
			return nil, mysqlQueryInvalid(ctx, detail)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).WithErrorDetails("query execution failed")
	}
	return result, nil
}

func (rqs *rawQueryService) validateSQLBuilderCursor(ctx context.Context, session *interfaces.CursorSession, catalog *interfaces.Catalog) error {
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return queryResolverForbidden(ctx)
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeMySQL || catalog.UpdateTime != session.SQLCatalogUpdateTime {
		return mysqlCursorChanged(ctx)
	}
	for _, table := range sqlBuilderTables(session.SQLQuery) {
		resource, err := rqs.rs.InternalGetByID(ctx, nil, table.ResourceID)
		if err != nil {
			return err
		}
		if resource == nil || resource.CatalogID != catalog.ID || resource.Category != interfaces.ResourceCategoryTable || resource.UpdateTime != session.SQLResourceVersions[table.ResourceID] || resource.SourceIdentifier != table.SourceIdentifier {
			return mysqlCursorChanged(ctx)
		}
		if resource.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
			return queryResolverForbidden(ctx)
		}
	}
	return ctx.Err()
}

func mysqlCursorChanged(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_ExecuteFailed).WithErrorDetails("cursor source metadata changed; start a new query")
}
func mysqlQueryInvalid(ctx context.Context, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).WithErrorDetails(detail)
}
