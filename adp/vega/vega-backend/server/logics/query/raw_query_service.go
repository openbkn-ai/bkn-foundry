// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/logger"
	"github.com/openbkn-ai/bkn-comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-comm-go/rest"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/logics/catalog"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/connector/local/table/mariadb"
	"vega-backend/logics/connector/local/table/postgresql"
	"vega-backend/logics/query/querypolicy"
	"vega-backend/logics/query/sqlglot"
	resourcelogic "vega-backend/logics/resource"
)

var (
	rawQueryServiceOnce     sync.Once
	rawQueryServiceInstance interfaces.RawQueryService
)

type rawQueryService struct {
	cs interfaces.CatalogService
	rs interfaces.ResourceService
}

// NewRawQueryService 创建SQL查询服务（单例模式）
func NewRawQueryService(appSetting *common.AppSetting) interfaces.RawQueryService {
	if appSetting != nil {
		rawQueryCursorSessions.configure(appSetting.QuerySetting.CursorMaxSessions)
	}
	rawQueryServiceOnce.Do(func() {
		rawQueryServiceInstance = &rawQueryService{
			cs: catalog.NewCatalogService(appSetting),
			rs: resourcelogic.NewResourceService(appSetting),
		}
	})
	return rawQueryServiceInstance
}

// Execute 执行SQL查询
func (rqs *rawQueryService) Execute(ctx context.Context, req *interfaces.RawQueryRequest) (resp *interfaces.RawQueryResponse, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SQLQueryExecute")
	defer span.End()

	// 收集资源状态告警（deprecated 命中），在每个成功返回路径上附加到响应，
	// 同时落到当前 span 关联的日志里，便于事后审计。
	var warnings []string
	defer func() {
		if resp != nil && resp.Paging == nil {
			resp.Paging = &interfaces.PagingResponse{}
		}
		if len(warnings) == 0 {
			return
		}
		for _, w := range warnings {
			otellog.LogWarn(ctx, "Query hit deprecated resource: "+w)
		}
		if resp != nil {
			resp.Warnings = append(resp.Warnings, warnings...)
		}
	}()

	// 记录请求参数
	logger.Infof("RawQueryRequest - query_format: %s, paging_mode: %s, %s", req.QueryFormat, req.Paging.Mode, SafeQuerySummary(req.Query))

	// 1. 校验请求
	if err := rqs.validateRequest(ctx, req); err != nil {
		otellog.LogError(ctx, "Validate request failed", err)
		return nil, err
	}
	req.NormalizePaging()
	if req.IsContinuation() {
		return rqs.executeSQLCursorContinuation(ctx, req)
	}
	if req.Paging.Mode == interfaces.PagingModeCursor {
		switch req.QueryFormat {
		case interfaces.QueryFormatSQL:
			return rqs.executeInitialSQLCursor(ctx, req)
		case interfaces.QueryFormatDSL:
			return rqs.executeInitialOpenSearchCursor(ctx, req)
		}
	}
	switch req.QueryFormat {
	case interfaces.QueryFormatSQL:
		return rqs.executeInitialSQLQuery(ctx, req)
	case interfaces.QueryFormatDSL:
		return rqs.executeInitialDSLQuery(ctx, req)
	}
	return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("unsupported raw query request")
}

func (rqs *rawQueryService) validateRequest(ctx context.Context, req *interfaces.RawQueryRequest) error {
	if err := req.ValidateContract(); err != nil {
		return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(err.Error())
	}
	return nil
}

func (rqs *rawQueryService) executeInitialSQLQuery(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	prepared, err := rqs.prepareSQLQuery(ctx, req)
	if err != nil {
		return nil, err
	}
	finalSQL := applySingleQueryPaging(prepared.sql, req.Paging.Offset, req.Paging.Size)

	result, err := rqs.executeSQL(ctx, prepared.catalog, finalSQL, interfaces.PagingModeSingle)
	if err != nil {
		return nil, err
	}
	result.Warnings = append(result.Warnings, prepared.warnings...)
	return result, nil
}

func (rqs *rawQueryService) executeInitialSQLCursor(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	prepared, err := rqs.prepareSQLQuery(ctx, req)
	if err != nil {
		return nil, err
	}
	session, err := rawQueryCursorSessions.create(
		accountIDFromContext(ctx),
		prepared.catalog.ID,
		prepared.resourceIDs,
		prepared.sql,
		req.Paging.Size,
		req.Paging.KeepAliveSec,
		req.QueryTimeoutSec,
	)
	if err != nil {
		return nil, cursorSessionLimitError(ctx)
	}
	session.Offset = req.Paging.Offset
	result, err := rqs.executeSQLCursorPage(ctx, session, prepared.catalog, prepared.warnings)
	if err != nil {
		// The client has not received this token, so it cannot retry this
		// session. Do not retain it until idle expiry.
		rawQueryCursorSessions.remove(session.ID)
	}
	return result, err
}

func (rqs *rawQueryService) executeSQLCursorContinuation(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	session, ok := rawQueryCursorSessions.get(req.Paging.Cursor)
	if !ok || session.ResourceDataParams != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor not found or expired")
	}
	if session.AccountID != accountIDFromContext(ctx) {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor does not belong to the current account")
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	if time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
		rawQueryCursorSessions.expire(session.ID)
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor not found or expired")
	}
	catalog, warnings, err := rqs.checkSameDataSource(ctx, session.ResourceIDs)
	if err != nil {
		return nil, err
	}
	if catalog.ID != session.CatalogID {
		return nil, rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("cursor catalog changed")
	}
	switch session.QueryFormat {
	case interfaces.QueryFormatSQL:
		return rqs.executeSQLCursorPage(ctx, session, catalog, warnings)
	case interfaces.QueryFormatDSL:
		return rqs.executeOpenSearchCursorPage(ctx, session, catalog, warnings)
	default:
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("cursor has unsupported query format")
	}
}

type preparedSQLQuery struct {
	catalog     *interfaces.Catalog
	resourceIDs []string
	sql         string
	warnings    []string
}

// prepareSQLQuery is the shared SQL preparation pipeline for both single and
// cursor execution. Policy validation deliberately happens after controlled
// resource binding and before compilation or connector execution.
func (rqs *rawQueryService) prepareSQLQuery(ctx context.Context, req *interfaces.RawQueryRequest) (*preparedSQLQuery, error) {
	resourceIDs, err := rqs.extractResourceIDs(req.Query)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("extract resource ids failed: %v", err))
	}
	if len(resourceIDs) == 0 {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("at least one resource_id is required for SQL queries")
	}
	catalog, warnings, err := rqs.checkSameDataSource(ctx, resourceIDs)
	if err != nil {
		return nil, err
	}
	replacedSQL, err := rqs.replaceResourceIDWithSchemaTable(ctx, req.Query, resourceIDs, catalog)
	if err != nil {
		return nil, err
	}
	inputDialect := req.EffectiveInputDialect()
	if err := rawQueryPolicy.ValidateSQL(replacedSQL, inputDialect); err != nil {
		if httpErr := rawQueryValidationError(ctx, err); httpErr != nil {
			return nil, httpErr
		}
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("failed to validate SQL query")
	}
	targetDialect, err := targetDialectForCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	finalSQL := replacedSQL
	if inputDialect != targetDialect {
		result, err := sqlglot.TranspileSQL(replacedSQL, inputDialect, targetDialect)
		if err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails("failed to compile SQL query")
		}
		finalSQL = result.SQL
	}
	return &preparedSQLQuery{catalog: catalog, resourceIDs: resourceIDs, sql: trimSQLTerminator(finalSQL), warnings: warnings}, nil
}

func trimSQLTerminator(sql string) string {
	return strings.TrimSuffix(strings.TrimSpace(sql), ";")
}

func (rqs *rawQueryService) executeSQLCursorPage(ctx context.Context, session *cursorSession, catalog *interfaces.Catalog, warnings []string) (*interfaces.RawQueryResponse, error) {
	pageSQL := fmt.Sprintf("SELECT * FROM (%s) AS _raw_query_cursor LIMIT %d OFFSET %d", session.CompiledSQL, session.PageSize+1, session.Offset)
	pageCtx := ctx
	if session.QueryTimeoutSec > 0 {
		var cancel context.CancelFunc
		pageCtx, cancel = context.WithTimeout(ctx, time.Duration(session.QueryTimeoutSec)*time.Second)
		defer cancel()
	}
	result, err := rqs.executeSQL(pageCtx, catalog, pageSQL, interfaces.PagingModeCursor)
	if err != nil {
		return nil, err
	}
	hasNext := len(result.Entries) > session.PageSize
	if hasNext {
		result.Entries = result.Entries[:session.PageSize]
		result.TotalCount = int64(len(result.Entries))
		session.Offset += session.PageSize
		rawQueryCursorSessions.markPageSuccess(session)
		result.Paging = cursorPagingResponse(session)
	} else {
		result.TotalCount = int64(len(result.Entries))
		result.Paging = &interfaces.PagingResponse{}
		rawQueryCursorSessions.closeSession(session.ID)
	}
	result.Warnings = append(result.Warnings, warnings...)
	return result, nil
}

func accountIDFromContext(ctx context.Context) string {
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok {
		return ""
	}
	return account.ID
}

func (rqs *rawQueryService) executeInitialOpenSearchCursor(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	query, indexName, catalog, warning, err := rqs.prepareOpenSearchCursorQuery(ctx, req)
	if err != nil {
		return nil, err
	}
	session, err := rawQueryCursorSessions.create(
		accountIDFromContext(ctx), catalog.ID, []string{queryResourceID(req.Query)}, "", req.Paging.Size, req.Paging.KeepAliveSec, req.QueryTimeoutSec,
	)
	if err != nil {
		return nil, cursorSessionLimitError(ctx)
	}
	session.QueryFormat = interfaces.QueryFormatDSL
	session.OpenSearchQuery = query
	session.OpenSearchIndex = indexName
	warnings := make([]string, 0, 1)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	result, err := rqs.executeOpenSearchCursorPage(ctx, session, catalog, warnings)
	if err != nil {
		rawQueryCursorSessions.remove(session.ID)
	}
	return result, err
}

func queryResourceID(query any) string {
	queryMap, _ := query.(map[string]any)
	resourceID, _ := queryMap["resource_id"].(string)
	return resourceID
}

func cursorSessionLimitError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusTooManyRequests, verrors.VegaBackend_Query_CursorSessionLimitExceeded).
		WithErrorDetails("cursor session limit reached, please retry later")
}

func (rqs *rawQueryService) prepareOpenSearchCursorQuery(ctx context.Context, req *interfaces.RawQueryRequest) (map[string]any, string, *interfaces.Catalog, string, error) {
	queryMap := req.Query.(map[string]any)
	resourceID := queryResourceID(queryMap)
	if resourceID == "" {
		return nil, "", nil, "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("resource_id is required for DSL queries")
	}
	if sort, ok := queryMap["sort"].([]any); !ok || len(sort) == 0 {
		return nil, "", nil, "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("sort is required for OpenSearch cursor paging")
	}

	resource, err := rqs.rs.GetByID(ctx, resourceID)
	if err != nil {
		return nil, "", nil, "", err.(*rest.HTTPError)
	}
	if resource == nil {
		return nil, "", nil, "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
			WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
	}
	warning, err := resourcelogic.EnsureResourceQueryable(ctx, resource)
	if err != nil {
		return nil, "", nil, "", err
	}
	catalog, err := rqs.cs.GetByID(ctx, resource.CatalogID, true)
	if err != nil {
		return nil, "", nil, "", err.(*rest.HTTPError)
	}
	if catalog == nil {
		return nil, "", nil, "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", resource.CatalogID))
	}
	if err := ensureCatalogEnabled(ctx, catalog); err != nil {
		return nil, "", nil, "", err
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeOpenSearch {
		return nil, "", nil, "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("DSL input requires an opensearch catalog, got: %s", catalog.ConnectorType))
	}

	prepared := make(map[string]any, len(queryMap))
	for key, value := range queryMap {
		if key != "resource_id" && key != "size" && key != "search_after" {
			prepared[key] = value
		}
	}
	prepared["size"] = req.Paging.Size
	if req.Paging.Offset > 0 {
		prepared["from"] = req.Paging.Offset
	}
	return prepared, resource.SourceIdentifier, catalog, warning, nil
}

func (rqs *rawQueryService) executeOpenSearchCursorPage(ctx context.Context, session *cursorSession, catalog *interfaces.Catalog, warnings []string) (*interfaces.RawQueryResponse, error) {
	query := make(map[string]any, len(session.OpenSearchQuery)+1)
	for key, value := range session.OpenSearchQuery {
		query[key] = value
	}
	if len(session.SearchAfter) > 0 {
		delete(query, "from")
		query["search_after"] = session.SearchAfter
	}
	pageCtx := ctx
	if session.QueryTimeoutSec > 0 {
		var cancel context.CancelFunc
		pageCtx, cancel = context.WithTimeout(ctx, time.Duration(session.QueryTimeoutSec)*time.Second)
		defer cancel()
	}
	connector, err := factory.GetFactory().CreateConnectorInstance(pageCtx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails(err.Error())
	}
	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("opensearch connector does not implement IndexConnector")
	}
	result, err := indexConnector.ExecuteRawQuery(pageCtx, session.OpenSearchIndex, query)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails(err.Error())
	}

	hasNext := len(result.Entries) == session.PageSize && len(result.SearchAfter) > 0
	if hasNext {
		session.SearchAfter = append([]any(nil), result.SearchAfter...)
		session.Offset += len(result.Entries)
		rawQueryCursorSessions.markPageSuccess(session)
		result.Paging = cursorPagingResponse(session)
	} else {
		result.Paging = &interfaces.PagingResponse{}
		rawQueryCursorSessions.closeSession(session.ID)
	}
	result.Warnings = append(result.Warnings, warnings...)
	return result, nil
}

func (rqs *rawQueryService) executeInitialDSLQuery(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	queryMap := req.Query.(map[string]any)
	// search_after is cursor-internal state. A client-supplied value is never
	// forwarded; cursor continuation supplies the server-owned value instead.
	delete(queryMap, "search_after")
	queryMap["size"] = req.Paging.Size
	queryMap["from"] = req.Paging.Offset
	resourceID, _ := queryMap["resource_id"].(string)
	if resourceID == "" {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("resource_id is required for DSL queries")
	}

	resource, err := rqs.rs.GetByID(ctx, resourceID)
	if err != nil {
		return nil, err.(*rest.HTTPError)
	}
	if resource == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
			WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
	}
	warning, err := resourcelogic.EnsureResourceQueryable(ctx, resource)
	if err != nil {
		return nil, err
	}

	catalog, err := rqs.cs.GetByID(ctx, resource.CatalogID, true)
	if err != nil {
		return nil, err.(*rest.HTTPError)
	}
	if catalog == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", resource.CatalogID))
	}
	if err := ensureCatalogEnabled(ctx, catalog); err != nil {
		return nil, err
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeOpenSearch {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("DSL input requires an opensearch catalog, got: %s", catalog.ConnectorType))
	}

	delete(queryMap, "resource_id")
	connector, err := factory.GetFactory().CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("connector initialization failed")
	}
	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("opensearch connector does not implement IndexConnector")
	}
	result, err := indexConnector.ExecuteRawQuery(ctx, resource.SourceIdentifier, queryMap)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("query execution failed")
	}
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	return result, nil
}

func targetDialectForCatalog(ctx context.Context, catalog *interfaces.Catalog) (string, error) {
	switch catalog.ConnectorType {
	case interfaces.ConnectorTypeMariaDB, interfaces.ConnectorTypeMySQL:
		return "mysql", nil
	case interfaces.ConnectorTypePostgreSQL:
		return "postgres", nil
	default:
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("unsupported connector type: %s", catalog.ConnectorType))
	}
}

func applySingleQueryPaging(sql string, offset, size int) string {
	return fmt.Sprintf("SELECT * FROM (%s) AS _raw_query_single LIMIT %d OFFSET %d", sql, size, offset)
}

// extractResourceIDs 从SQL中提取所有{{.resource_id}}占位符
func (rqs *rawQueryService) extractResourceIDs(query any) ([]string, error) {
	// 如果query是字符串类型，使用正则表达式提取resource_id
	if queryStr, ok := query.(string); ok {
		re := regexp.MustCompile(`\{\{\.?(\w+)\}\}`)
		matches := re.FindAllStringSubmatch(queryStr, -1)

		resourceIDs := make([]string, 0, len(matches))
		seen := make(map[string]bool)

		for _, match := range matches {
			if len(match) > 1 {
				resourceID := match[1]
				if !seen[resourceID] {
					seen[resourceID] = true
					resourceIDs = append(resourceIDs, resourceID)
				}
			}
		}

		return resourceIDs, nil
	}

	// 如果query是map类型（OpenSearch DSL），返回空数组
	// OpenSearch查询通过resource_id参数指定索引
	return []string{}, nil
}

// checkSameDataSource 检查所有resource_id是否来自同一个数据源，
// 同时校验每个资源的状态。返回 catalog、deprecated 资源的告警列表与错误。
func (rqs *rawQueryService) checkSameDataSource(ctx context.Context, resourceIDs []string) (*interfaces.Catalog, []string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource ids provided")
	}

	// 获取所有资源
	resources, err := rqs.rs.GetByIDs(ctx, resourceIDs)
	if err != nil {
		return nil, nil, err.(*rest.HTTPError)
	}
	if len(resources) != len(resourceIDs) {
		resourceMap := make(map[string]bool)
		for _, r := range resources {
			resourceMap[r.ID] = true
		}
		for _, id := range resourceIDs {
			if !resourceMap[id] {
				return nil, nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
					WithErrorDetails(fmt.Sprintf("resource %s not found", id))
			}
		}
	}

	// 校验每个 resource 的状态：disabled/stale 拒绝，deprecated 仅告警
	warnings, err := resourcelogic.EnsureResourcesQueryable(ctx, resources)
	if err != nil {
		return nil, nil, err
	}

	// 检查是否来自同一个catalog
	catalogIDs := make(map[string]bool)
	for _, r := range resources {
		catalogIDs[r.CatalogID] = true
	}
	if len(catalogIDs) > 1 {
		return nil, nil, rest.NewHTTPError(ctx, http.StatusNotImplemented, verrors.VegaBackend_Query_MultiCatalogNotSupported).
			WithErrorDetails("暂不支持多数据源 JOIN，计划使用 Trino/DuckDB 实现。")
	}

	// 获取catalog
	var catalogID string
	for id := range catalogIDs {
		catalogID = id
		break
	}

	catalog, err := rqs.cs.GetByID(ctx, catalogID, true)
	if err != nil {
		return nil, nil, err.(*rest.HTTPError)
	}
	if catalog == nil {
		return nil, nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", catalogID))
	}
	if err := ensureCatalogEnabled(ctx, catalog); err != nil {
		return nil, nil, err
	}

	return catalog, warnings, nil
}

func ensureCatalogEnabled(ctx context.Context, catalog *interfaces.Catalog) error {
	if catalog != nil && !catalog.Enabled {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
	}
	return nil
}

// replaceResourceIDWithSchemaTable 将resource_id替换为schema.table格式
func (rqs *rawQueryService) replaceResourceIDWithSchemaTable(ctx context.Context, sql any, resourceIDs []string, catalog *interfaces.Catalog) (string, error) {
	replacedSQL := sql.(string)
	logger.Infof("Before replace - %s, resource_ids: %v", SafeQuerySummary(replacedSQL), resourceIDs)

	for _, resourceID := range resourceIDs {
		// 获取资源信息
		resource, err := rqs.rs.GetByID(ctx, resourceID)
		if err != nil {
			return "", err.(*rest.HTTPError)
		}
		if resource == nil {
			return "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
				WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
		}

		// 构建schema.table格式, 使用resource.SourceIdentifier
		// schemaTable := fmt.Sprintf(`%s.%s`, catalog.Name, resource.SourceIdentifier)

		// 替换{{.resource_id}}和{{resource_id}}为schema.table
		placeholder1 := fmt.Sprintf("{{.%s}}", resourceID)
		placeholder2 := fmt.Sprintf("{{%s}}", resourceID)
		replacedSQL = regexp.MustCompile(regexp.QuoteMeta(placeholder1)).ReplaceAllString(replacedSQL, resource.SourceIdentifier)
		replacedSQL = regexp.MustCompile(regexp.QuoteMeta(placeholder2)).ReplaceAllString(replacedSQL, resource.SourceIdentifier)
	}

	logger.Infof("After replace - %s", SafeQuerySummary(replacedSQL))
	return replacedSQL, nil
}

// executeSQL 执行 SQL 查询并记录分页模式。
func (rqs *rawQueryService) executeSQL(ctx context.Context, catalog *interfaces.Catalog, sql string, pagingMode interfaces.PagingMode) (*interfaces.RawQueryResponse, error) {
	logger.Infof("Executing query - %s, paging_mode: %s, catalog: %s", SafeQuerySummary(sql), pagingMode, catalog.Name)

	// 创建connector
	connector, err := factory.GetFactory().CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Create connector failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("connector initialization failed")
	}

	// 根据connector类型执行SQL
	var result *interfaces.RawQueryResponse
	switch catalog.ConnectorType {
	case interfaces.ConnectorTypeMariaDB, interfaces.ConnectorTypeMySQL:
		mariadbConnector := connector.(*mariadb.MariaDBConnector)
		result, err = mariadbConnector.ExecuteRawSQL(ctx, sql)
	case interfaces.ConnectorTypePostgreSQL:
		postgresqlConnector := connector.(*postgresql.PostgresqlConnector)
		result, err = postgresqlConnector.ExecuteRawSQL(ctx, sql)
	default:
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("unsupported connector type: %s", catalog.ConnectorType))
	}

	if err != nil {
		otellog.LogError(ctx, "Execute SQL failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("query execution failed")
	}

	logger.Infof("SQL query executed successfully: paging_mode=%s, returned_rows=%d", pagingMode, len(result.Entries))
	return result, nil
}

func rawQueryValidationError(ctx context.Context, err error) error {
	var validationErr *querypolicy.ReadOnlySQLValidationError
	if !errors.As(err, &validationErr) {
		return nil
	}
	return rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("raw query rejected by read-only policy")
}

var rawQueryPolicy querypolicy.Adapter = querypolicy.NewSQLGlotAdapter()
