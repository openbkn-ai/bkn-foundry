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
		return nil, mysqlQueryForbidden(ctx)
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
				return nil, mysqlQueryForbidden(ctx)
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
		return nil, sqlBuilderExecutionError(ctx, err)
	}
	return result, nil
}

// openSQLBuilderCursor 合并首次 API 偏移后只执行一次查询，不将页大小变成 SQL LIMIT。
func (rqs *rawQueryService) openSQLBuilderCursor(ctx context.Context, session *interfaces.CursorSession, catalog *interfaces.Catalog) error {
	query := session.SQLQuery
	if session.PageOffset < 0 {
		return mysqlQueryInvalid(ctx, "分页偏移不能为负数")
	}
	offset := uint64(session.PageOffset)
	if query.OffsetPart > math.MaxUint64-offset {
		return mysqlQueryInvalid(ctx, "分页偏移超出源端范围")
	}
	read := *query
	read.OffsetPart += offset
	if query.LimitPart != nil {
		remaining := *query.LimitPart - min(offset, *query.LimitPart)
		read.LimitPart = &remaining
	}
	connector, err := rqs.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return connectorInitializationError(ctx, err)
	}
	session.SQLConnector = connector
	executor, ok := connector.(interfaces.SQLBuilderRowsExecutor)
	if !ok {
		return mysqlQueryInvalid(ctx, "连接器尚未支持 SQL Rows 执行")
	}
	// 保留请求身份与观测信息，Rows 生命周期由会话控制，不能随首屏 HTTP 请求结束。
	cursorCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	session.SQLCancel = cancel
	stop := watchSQLCursorRequest(ctx, cancel)
	rows, columns, err := executor.OpenSQLBuilder(cursorCtx, &read) //nolint:rowserrcheck // Rows 转交会话，scanSQLCursorRow 在读取结束时检查 Err。
	stop()
	session.SQLRows, session.SQLColumns = rows, columns
	if ctx.Err() != nil {
		return sqlBuilderExecutionError(ctx, ctx.Err())
	}
	if err != nil {
		return sqlBuilderExecutionError(ctx, err)
	}
	return nil
}

// watchSQLCursorRequest 仅在当前请求打开或读取 Rows 时传播取消，并等待已启动的回调结束。
func watchSQLCursorRequest(ctx context.Context, cancel context.CancelFunc) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { cancel(); close(done) })
	return func() {
		if !stop() {
			<-done
		}
	}
}

// readSQLCursorPage 使用同一 Rows，最多缓存一行预读结果。读取失败后无法回退，必须终止会话。
func readSQLCursorPage(ctx context.Context, session *interfaces.CursorSession) (*interfaces.RawQueryResponse, error) {
	stop := watchSQLCursorRequest(ctx, session.SQLCancel)
	result := &interfaces.RawQueryResponse{Columns: session.SQLColumns, Entries: make([]map[string]any, 0, session.PageLimit)}
	var err error
	if session.SQLPendingRow != nil {
		result.Entries = append(result.Entries, session.SQLPendingRow)
		session.SQLPendingRow = nil
	}
	for len(result.Entries) <= session.PageLimit {
		var row map[string]any
		row, err = scanSQLCursorRow(session)
		if err != nil || row == nil {
			break
		}
		if len(result.Entries) == session.PageLimit {
			session.SQLPendingRow = row
			break
		}
		result.Entries = append(result.Entries, row)
	}
	stop()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		rawQueryCursorSessions.remove(session.ID)
		return nil, sqlBuilderExecutionError(ctx, err)
	}
	return result, nil
}

func scanSQLCursorRow(session *interfaces.CursorSession) (map[string]any, error) {
	if !session.SQLRows.Next() {
		return nil, session.SQLRows.Err()
	}
	values := make([]any, len(session.SQLColumns))
	destinations := make([]any, len(values))
	for i := range values {
		destinations[i] = &values[i]
	}
	if err := session.SQLRows.Scan(destinations...); err != nil {
		return nil, err
	}
	row := make(map[string]any, len(values))
	for i, column := range session.SQLColumns {
		value := values[i]
		if data, ok := value.([]byte); ok {
			value = string(data)
		}
		row[column.Name] = value
	}
	return row, nil
}

func sqlBuilderExecutionError(ctx context.Context, err error) error {
	var forbidden *interfaces.SourceReadForbiddenError
	if errors.As(err, &forbidden) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Resource_SourceReadForbidden)
	}
	if detail, ok := interfaces.RequestSideQueryError(err); ok {
		return mysqlQueryInvalid(ctx, detail)
	}
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).WithErrorDetails("query execution failed")
}

// invalidateSQLCursor 权限或来源变化终止结果集；读取前的临时服务错误允许重试。
func invalidateSQLCursor(session *interfaces.CursorSession, err error) {
	if session.SQLQuery == nil {
		return
	}
	var failure *httperrors.HTTPError
	if errors.As(err, &failure) && (failure.HTTPCode == http.StatusForbidden || failure.HTTPCode == http.StatusNotFound || failure.HTTPCode == http.StatusConflict || failure.ErrorCode == verrors.VegaBackend_Query_MultiCatalogNotSupported) {
		rawQueryCursorSessions.remove(session.ID)
	}
}

func (rqs *rawQueryService) validateSQLBuilderCursor(ctx context.Context, session *interfaces.CursorSession, catalog *interfaces.Catalog) error {
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return mysqlQueryForbidden(ctx)
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
			return mysqlQueryForbidden(ctx)
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

func mysqlQueryForbidden(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
}
