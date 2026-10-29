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
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/common"
	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/catalog"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/factory"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/dataset"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/querypolicy"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/sqlglot"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler"
)

const rawQueryTotalCountColumn = "_raw_query_total_count"

var (
	rqServiceOnce  sync.Once
	rqService      interfaces.RawQueryService
	rawQueryPolicy querypolicy.Adapter = querypolicy.NewSQLGlotAdapter()
)

type rawQueryService struct {
	cf interfaces.ConnectorFactory
	cs interfaces.CatalogService
	rs interfaces.ResourceService
}

type preparedSQLQuery struct {
	catalog          *interfaces.Catalog
	resourceIDs      []string
	sql              string
	builder          *sb.SelectBuilder
	resourceVersions map[string]int64
	warnings         []string
}

type rawSQLBuildOptions struct {
	offset int
	limit  int
	count  bool
}

func connectorInitializationError(ctx context.Context, err error) error {
	if errors.Is(err, factory.ErrConnectorEntitlementDenied) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Connector_EntitlementDenied).WithErrorDetails(err.Error())
	}
	if errors.Is(err, factory.ErrConnectorDisabled) {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Connector_Disabled).WithErrorDetails(err.Error())
	}
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
		WithErrorDetails("connector initialization failed")
}

// NewRawQueryService creates SQL query services (singleton pattern)
func NewRawQueryService(appSetting *common.AppSetting) interfaces.RawQueryService {
	ConfigureCursorSessions(appSetting.QuerySetting.CursorMaxSessions, appSetting.QuerySetting.CursorMaxSessionsPerAccount)
	rqServiceOnce.Do(func() {
		rqService = &rawQueryService{
			cf: factory.NewConnectorFactory(appSetting),
			cs: catalog.NewCatalogService(appSetting),
			rs: resourcelogic.NewResourceService(appSetting, dataset.NewDatasetService(appSetting)),
		}
	})
	return rqService
}

// Execute executes SQL queries
func (rqs *rawQueryService) Execute(ctx context.Context, req *interfaces.RawQueryRequest) (resp *interfaces.RawQueryResponse, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SQLQueryExecute")
	defer span.End()

	// Collect resource status alerts (deprecated hits) and attach them to the response on each successful return path.
	// Also write this to the log associated with the current span for later auditing.
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

	// Record the request parameters
	logger.Infof("RawQueryRequest - query_format: %s, paging_mode: %s, %s", req.QueryFormat, req.Paging.Mode, SafeQuerySummary(req.Query))

	// 1. Verify the request
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
	return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("unsupported raw query request")
}

func (rqs *rawQueryService) validateRequest(ctx context.Context, req *interfaces.RawQueryRequest) error {
	if err := req.ValidateContract(); err != nil {
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(err.Error())
	}
	return nil
}

func (rqs *rawQueryService) executeInitialSQLQuery(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	queryCtx, cancel := queryExecutionContext(ctx, req.QueryTimeoutSec)
	defer cancel()

	prepared, err := rqs.prepareSQLQuery(queryCtx, req)
	if err != nil {
		return nil, err
	}

	var totalCount int64
	if req.NeedTotal {
		if prepared.builder != nil {
			var counted *interfaces.RawQueryResponse
			counted, err = rqs.executeSQLBuilder(queryCtx, prepared.catalog, prepared.builder, &rawSQLBuildOptions{count: true})
			if err == nil {
				totalCount, err = rawQueryTotalCount(counted)
			}
		} else {
			totalCount, err = rqs.executeSQLTotalCount(queryCtx, prepared.catalog, prepared.sql)
		}
		if err != nil {
			return nil, err
		}
	}
	options := &rawSQLBuildOptions{offset: req.Paging.Offset, limit: req.Paging.Limit}
	var result *interfaces.RawQueryResponse
	if prepared.builder != nil {
		result, err = rqs.executeSQLBuilder(queryCtx, prepared.catalog, prepared.builder, options)
	} else {
		result, err = rqs.executeSQL(queryCtx, prepared.catalog, prepared.sql, interfaces.PagingModeSingle, options)
	}
	if err != nil {
		return nil, err
	}
	if req.NeedTotal {
		result.TotalCount = &totalCount
	} else {
		result.TotalCount = nil
	}
	result.Warnings = append(result.Warnings, prepared.warnings...)
	return withExecutedResourceIDs(result, prepared.resourceIDs), nil
}

func (rqs *rawQueryService) executeInitialSQLCursor(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	queryCtx, cancel := queryExecutionContext(ctx, req.QueryTimeoutSec)
	defer cancel()
	prepared, err := rqs.prepareSQLQuery(queryCtx, req)
	if err != nil {
		return nil, err
	}
	var totalCount int64
	if req.NeedTotal {
		if prepared.builder != nil {
			var counted *interfaces.RawQueryResponse
			counted, err = rqs.executeSQLBuilder(queryCtx, prepared.catalog, prepared.builder, &rawSQLBuildOptions{count: true})
			if err == nil {
				totalCount, err = rawQueryTotalCount(counted)
			}
		} else {
			totalCount, err = rqs.executeSQLTotalCount(queryCtx, prepared.catalog, prepared.sql)
		}
		if err != nil {
			return nil, err
		}
	}
	session, err := rawQueryCursorSessions.create(
		accountIDFromContext(ctx),
		prepared.catalog.ID,
		prepared.resourceIDs,
		prepared.sql,
		req.Paging.Limit,
		req.Paging.KeepAliveSec,
		req.QueryTimeoutSec,
	)
	if err != nil {
		return nil, cursorSessionCreateError(ctx, err)
	}
	session.Lock()
	defer session.Unlock()
	session.SQLQuery = prepared.builder
	session.SQLResourceVersions = prepared.resourceVersions
	session.SQLCatalogUpdateTime = prepared.catalog.UpdateTime
	session.SQLConnectorType = prepared.catalog.ConnectorType
	session.PageOffset = req.Paging.Offset
	session.TotalCount = totalCount
	session.HasTotalCount = req.NeedTotal
	session.NeedTotal = req.NeedTotal
	bindCursorResource(session, req)
	if session.SQLQuery != nil {
		if err := rqs.openSQLBuilderCursor(queryCtx, session, prepared.catalog); err != nil {
			rawQueryCursorSessions.remove(session.ID)
			return nil, err
		}
	}
	result, err := rqs.executeSQLCursorPage(queryCtx, session, prepared.catalog, prepared.warnings)
	if err != nil {
		// The client has not received this token, so it cannot retry this
		// session. Do not retain it until idle expiry.
		rawQueryCursorSessions.remove(session.ID)
	}
	return result, err
}

func (rqs *rawQueryService) executeSQLCursorContinuation(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	session, ok := rawQueryCursorSessions.acquire(req.Paging.Cursor)
	if !ok || session.ResourceDataParams != nil {
		if ok {
			rawQueryCursorSessions.release(session)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor not found or expired")
	}
	defer rawQueryCursorSessions.release(session)
	if session.AccountID != accountIDFromContext(ctx) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor does not belong to the current account")
	}
	if err := validateCursorResourceBinding(ctx, session, req); err != nil {
		return nil, err
	}
	queryCtx, cancel := queryExecutionContext(ctx, session.QueryTimeoutSec)
	defer cancel()
	catalog, warnings, err := rqs.checkSameDataSource(queryCtx, session.ResourceIDs)
	if err != nil {
		invalidateSQLCursor(session, err)
		return nil, err
	}
	if catalog.ID != session.CatalogID {
		if session.SQLQuery != nil {
			rawQueryCursorSessions.remove(session.ID)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("cursor catalog changed")
	}
	if session.SQLQuery != nil {
		if err := rqs.validateSQLBuilderCursor(queryCtx, session, catalog); err != nil {
			invalidateSQLCursor(session, err)
			return nil, err
		}
	}
	switch session.QueryFormat {
	case interfaces.QueryFormatSQL:
		return rqs.executeSQLCursorPage(queryCtx, session, catalog, warnings)
	case interfaces.QueryFormatDSL:
		return rqs.executeOpenSearchCursorPage(queryCtx, session, catalog, warnings)
	default:
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("cursor has unsupported query format")
	}
}

// prepareSQLQuery is the shared SQL preparation pipeline for both single and
// cursor execution. MariaDB/MySQL、PG、HANA 和 Doris 强制使用公共 Builder；其他连接器保留 SQLGlot。
func (rqs *rawQueryService) prepareSQLQuery(ctx context.Context, req *interfaces.RawQueryRequest) (*preparedSQLQuery, error) {
	inputDialect := req.EffectiveInputDialect()
	var resourceIDs []string
	var parsed *sb.SelectBuilder
	var parseErr, err error
	if inputDialect == "mysql" {
		parsed, parseErr = compiler.ParseSQL(req.Query.(string))
		if parseErr == nil {
			seen := make(map[string]bool)
			for _, table := range sqlBuilderTables(parsed) {
				if !seen[table.ResourceID] {
					resourceIDs = append(resourceIDs, table.ResourceID)
					seen[table.ResourceID] = true
				}
			}
		} else {
			// 仅提取路由信息；MySQL 目标仍返回原解析错误，其他目标保留旧语法能力。
			resourceIDs, err = compiler.ResourceIDs(req.Query.(string))
		}
	} else {
		resourceIDs, err = rqs.extractResourceIDs(ctx, req.Query, inputDialect)
	}
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("extract resource ids failed: %v", err))
	}
	if len(resourceIDs) == 0 {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("at least one resource_id is required for SQL queries")
	}
	catalog, warnings, err := rqs.checkSameDataSource(ctx, resourceIDs)
	if err != nil {
		return nil, err
	}
	if catalog.ConnectorType == interfaces.ConnectorTypeMySQL ||
		catalog.ConnectorType == interfaces.ConnectorTypeMariaDB ||
		catalog.ConnectorType == interfaces.ConnectorTypePostgreSQL ||
		catalog.ConnectorType == interfaces.ConnectorTypeHANA ||
		catalog.ConnectorType == interfaces.ConnectorTypeDoris {
		if inputDialect != "mysql" {
			return nil, sqlBuilderQueryInvalid(ctx, "MariaDB/MySQL, PostgreSQL, HANA and Doris queries only support mysql input syntax")
		}
		if parseErr != nil {
			return nil, sqlBuilderQueryInvalid(ctx, parseErr.Error())
		}
		return rqs.prepareSQLBuilder(ctx, parsed, catalog, resourceIDs, warnings)
	}
	targetDialect, err := targetDialectForCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	// Compile temporary table references before binding source-dialect names.
	needsUnicodeNormalization := targetDialect == "tsql" && strings.ContainsFunc(req.Query.(string), func(r rune) bool {
		return r > unicode.MaxASCII
	})
	var finalSQL string
	if inputDialect == targetDialect && !needsUnicodeNormalization {
		finalSQL, err = rqs.replaceResourceIDWithSchemaTable(ctx, req.Query, resourceIDs, catalog, inputDialect)
		if err != nil {
			return nil, err
		}
		allowed, err := rqs.resourceSourceIdentifiers(ctx, resourceIDs)
		if err != nil {
			return nil, err
		}
		if err := validateSQLPolicy(ctx, finalSQL, targetDialect, allowed, true); err != nil {
			return nil, err
		}
	} else {
		query := req.Query.(string)
		markers := make([]string, len(resourceIDs))
		for i, resourceID := range resourceIDs {
			marker := fmt.Sprintf("__vega_resource_%d", i)
			for strings.Contains(query, marker) {
				marker += "_"
			}
			markers[i] = marker
			query = replacePlaceholderInSQLCode(query, "{{"+resourceID+"}}", marker, inputDialect)
			query = replacePlaceholderInSQLCode(query, "{{."+resourceID+"}}", marker, inputDialect)
		}
		if err := validateSQLPolicy(ctx, query, inputDialect, markers, inputDialect == targetDialect); err != nil {
			return nil, err
		}
		compiled, err := sqlglot.TranspileSQL(ctx, query, inputDialect, targetDialect)
		if err != nil {
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails("failed to compile SQL query")
		}
		references, err := rqs.resourceSourceIdentifiers(ctx, resourceIDs)
		if err != nil {
			return nil, err
		}
		bindings := make(map[string]string, len(markers))
		for i, marker := range markers {
			bindings[marker] = references[i]
		}
		bound, err := sqlglot.BindTableReferences(ctx, compiled.SQL, targetDialect, bindings)
		if err != nil {
			return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails("failed to bind SQL resource references")
		}
		finalSQL = bound.SQL
		if err := validateSQLPolicy(ctx, finalSQL, targetDialect, references, true); err != nil {
			return nil, err
		}
	}
	return &preparedSQLQuery{catalog: catalog, resourceIDs: resourceIDs, sql: trimSQLTerminator(finalSQL), warnings: warnings}, nil
}

func validateSQLPolicy(ctx context.Context, sql, dialect string, allowedReferences []string, derivedTable bool) error {
	var err error
	if derivedTable {
		err = rawQueryPolicy.ValidateDerivedTable(ctx, sql, dialect)
	} else {
		err = rawQueryPolicy.ValidateSQL(ctx, sql, dialect)
	}
	if err != nil {
		if httpErr := rawQueryValidationError(ctx, err); httpErr != nil {
			return httpErr
		}
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("failed to validate SQL query")
	}
	if err := rawQueryPolicy.ValidateTableReferences(ctx, sql, dialect, allowedReferences); err != nil {
		if httpErr := rawQueryValidationError(ctx, err); httpErr != nil {
			return httpErr
		}
		return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("failed to validate SQL resource references")
	}
	return nil
}

func (rqs *rawQueryService) resourceSourceIdentifiers(ctx context.Context, resourceIDs []string) ([]string, error) {
	identifiers := make([]string, 0, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		resource, err := rqs.rs.InternalGetByID(ctx, nil, resourceID)
		if err != nil {
			return nil, err
		}
		if resource == nil || strings.TrimSpace(resource.SourceIdentifier) == "" {
			return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
				WithErrorDetails(fmt.Sprintf("resource %s has no queryable source", resourceID))
		}
		identifiers = append(identifiers, resource.SourceIdentifier)
	}
	return identifiers, nil
}

func trimSQLTerminator(sql string) string {
	return strings.TrimSuffix(strings.TrimSpace(sql), ";")
}

func (rqs *rawQueryService) executeSQLCursorPage(ctx context.Context, session *interfaces.CursorSession, catalog *interfaces.Catalog, warnings []string) (*interfaces.RawQueryResponse, error) {
	pageCtx, cancel := queryExecutionContext(ctx, session.QueryTimeoutSec)
	defer cancel()

	if session.SQLQuery != nil && session.PageOffset > math.MaxInt-session.PageLimit {
		rawQueryCursorSessions.remove(session.ID)
		return nil, sqlBuilderQueryInvalid(ctx, "cursor paging offset exceeds the supported range")
	}
	options := &rawSQLBuildOptions{offset: session.PageOffset, limit: session.PageLimit + 1}
	var result *interfaces.RawQueryResponse
	var err error
	if session.SQLQuery != nil {
		result, err = readSQLCursorPage(pageCtx, session)
	} else {
		result, err = rqs.executeSQL(pageCtx, catalog, session.CompiledSQL, interfaces.PagingModeCursor, options)
	}
	if err != nil {
		return nil, err
	}
	hasNext := len(result.Entries) > session.PageLimit || session.SQLPendingRow != nil
	if hasNext {
		result.Entries = result.Entries[:session.PageLimit]
		session.PageOffset += session.PageLimit
		rawQueryCursorSessions.markPageSuccess(session)
		result.Paging = cursorPagingResponse(session)
	} else {
		result.Paging = &interfaces.PagingResponse{}
		rawQueryCursorSessions.closeSession(session.ID)
	}
	if session.HasTotalCount {
		totalCount := session.TotalCount
		result.TotalCount = &totalCount
	} else {
		result.TotalCount = nil
	}
	result.Warnings = append(result.Warnings, warnings...)
	return withExecutedResourceIDs(result, session.ResourceIDs), nil
}

func accountIDFromContext(ctx context.Context) string {
	account, ok := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if !ok {
		return ""
	}
	return account.ID
}

func (rqs *rawQueryService) executeInitialOpenSearchCursor(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	queryCtx, cancel := queryExecutionContext(ctx, req.QueryTimeoutSec)
	defer cancel()

	query, indexName, catalog, warning, err := rqs.prepareOpenSearchCursorQuery(queryCtx, req)
	if err != nil {
		return nil, err
	}
	if hasOpenSearchAggregation(query) {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor paging does not support OpenSearch aggregation queries")
	}
	session, err := rawQueryCursorSessions.create(
		accountIDFromContext(ctx), catalog.ID, []string{queryResourceID(req.Query)}, "", req.Paging.Limit, req.Paging.KeepAliveSec, req.QueryTimeoutSec,
	)
	if err != nil {
		return nil, cursorSessionCreateError(ctx, err)
	}
	session.QueryFormat = interfaces.QueryFormatDSL
	bindCursorResource(session, req)
	session.OpenSearchQuery = query
	session.OpenSearchIndex = indexName
	session.PageOffset = req.Paging.Offset
	session.NeedTotal = req.NeedTotal
	session.Lock()
	defer session.Unlock()
	warnings := make([]string, 0, 1)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	result, err := rqs.executeOpenSearchCursorPage(queryCtx, session, catalog, warnings)
	if err != nil {
		rawQueryCursorSessions.remove(session.ID)
	}
	return result, err
}

func hasOpenSearchAggregation(query map[string]any) bool {
	_, hasAggs := query["aggs"]
	_, hasAggregations := query["aggregations"]
	return hasAggs || hasAggregations
}

func queryResourceID(query any) string {
	queryMap, _ := query.(map[string]any)
	resourceID, _ := queryMap["resource_id"].(string)
	return resourceID
}

func cursorSessionCreateError(ctx context.Context, err error) error {
	if errors.Is(err, errCursorAccountLimitReached) {
		return httperrors.NewHTTPError(ctx, http.StatusTooManyRequests, verrors.VegaBackend_Query_CursorSessionLimitExceeded).
			WithErrorDetails("per-account cursor session limit reached")
	}
	if errors.Is(err, errCursorSessionLimitReached) {
		return httperrors.NewHTTPError(ctx, http.StatusTooManyRequests, verrors.VegaBackend_Query_CursorSessionLimitExceeded).
			WithErrorDetails("global cursor session limit reached")
	}
	return err
}

func bindCursorResource(session *interfaces.CursorSession, req *interfaces.RawQueryRequest) {
	if req == nil || req.ResourceDataResourceID == "" {
		return
	}
	session.ResourceDataResourceID = req.ResourceDataResourceID
	session.ResourceDataUpdateTime = req.ResourceDataUpdateTime
}

func validateCursorResourceBinding(ctx context.Context, session *interfaces.CursorSession,
	req *interfaces.RawQueryRequest) error {
	if session.ResourceDataResourceID == "" {
		if req != nil && req.ResourceDataResourceID != "" {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorResourceChanged).
				WithErrorDetails("raw query cursor cannot be used for resource data paging")
		}
		return nil
	}
	if req == nil || req.ResourceDataResourceID != session.ResourceDataResourceID {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorResourceChanged).
			WithErrorDetails("cursor does not belong to the current resource")
	}
	if req.ResourceDataUpdateTime != session.ResourceDataUpdateTime {
		rawQueryCursorSessions.closeSession(session.ID)
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorResourceChanged).
			WithErrorDetails("resource changed after cursor creation")
	}
	return nil
}

func (rqs *rawQueryService) prepareOpenSearchCursorQuery(ctx context.Context, req *interfaces.RawQueryRequest) (map[string]any, string, *interfaces.Catalog, string, error) {
	queryMap := req.Query.(map[string]any)
	resourceID := queryResourceID(queryMap)
	if resourceID == "" {
		return nil, "", nil, "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("resource_id is required for DSL queries")
	}
	if sort, ok := queryMap["sort"].([]any); !ok || len(sort) == 0 {
		return nil, "", nil, "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("sort is required for OpenSearch cursor paging")
	}

	resource, err := rqs.resourceForQuery(ctx, resourceID)
	if err != nil {
		return nil, "", nil, "", err
	}
	if resource == nil {
		return nil, "", nil, "", httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
			WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
	}
	warning, err := resourcelogic.EnsureResourceQueryable(ctx, resource)
	if err != nil {
		return nil, "", nil, "", err
	}
	// Resource query permission was checked above. The Catalog is loaded
	// solely for query execution, so it must not impose catalog:view_detail.
	catalog, err := rqs.cs.InternalGetByID(ctx, resource.CatalogID, true)
	if err != nil {
		return nil, "", nil, "", err
	}
	if catalog == nil {
		return nil, "", nil, "", httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", resource.CatalogID))
	}
	if err := ensureCatalogEnabled(ctx, catalog); err != nil {
		return nil, "", nil, "", err
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeOpenSearch {
		return nil, "", nil, "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("DSL input requires an opensearch catalog, got: %s", catalog.ConnectorType))
	}

	prepared := make(map[string]any, len(queryMap))
	for key, value := range queryMap {
		if key != "resource_id" && key != "size" && key != "from" && key != "search_after" && key != "track_total_hits" {
			prepared[key] = value
		}
	}
	prepared["size"] = req.Paging.Limit
	if req.NeedTotal {
		// Exact hit counts are only required when the caller asks for one.
		prepared["track_total_hits"] = true
	}
	if req.Paging.Offset > 0 {
		prepared["from"] = req.Paging.Offset
	}
	return prepared, resource.SourceIdentifier, catalog, warning, nil
}

func (rqs *rawQueryService) executeOpenSearchCursorPage(ctx context.Context, session *interfaces.CursorSession, catalog *interfaces.Catalog, warnings []string) (*interfaces.RawQueryResponse, error) {
	query := make(map[string]any, len(session.OpenSearchQuery)+1)
	for key, value := range session.OpenSearchQuery {
		query[key] = value
	}
	if len(session.SearchAfter) > 0 {
		delete(query, "from")
		query["search_after"] = session.SearchAfter
	}
	if session.NeedTotal && session.HasTotalCount {
		delete(query, "track_total_hits")
	}
	pageCtx := ctx
	if session.QueryTimeoutSec > 0 {
		var cancel context.CancelFunc
		pageCtx, cancel = context.WithTimeout(ctx, time.Duration(session.QueryTimeoutSec)*time.Second)
		defer cancel()
	}

	connector, err := rqs.cf.CreateConnectorInstance(pageCtx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, connectorInitializationError(ctx, err)
	}
	defer func() { _ = connector.Close(pageCtx) }()

	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("opensearch connector does not implement IndexConnector")
	}

	if err := indexConnector.Connect(pageCtx); err != nil {
		return nil, connectorInitializationError(ctx, err)
	}

	result, err := indexConnector.ExecuteRawQuery(pageCtx, session.OpenSearchIndex, query)
	if err != nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("query execution failed")
	}
	if session.NeedTotal && !session.HasTotalCount && result.TotalCount != nil {
		session.TotalCount = *result.TotalCount
		session.HasTotalCount = true
	}

	hasNext := false
	if session.NeedTotal {
		// Real OpenSearch responses provide an exact total on the first page.
		// Retain the fallback for connector implementations that omit it.
		hasMoreResults := !session.HasTotalCount ||
			int64(session.PageOffset+len(result.Entries)) < session.TotalCount
		hasNext = len(result.Entries) == session.PageLimit && hasMoreResults && len(result.SearchAfter) > 0
	} else {
		// Without an exact total, a full page may be the final page. Preserve
		// the cursor and let one final empty request close that exact-multiple
		// case; this keeps size within OpenSearch's result-window limit.
		hasNext = len(result.Entries) == session.PageLimit && len(result.SearchAfter) > 0
	}
	if hasNext {
		session.SearchAfter = append([]any(nil), result.SearchAfter...)
		session.PageOffset += len(result.Entries)
		rawQueryCursorSessions.markPageSuccess(session)
		result.Paging = cursorPagingResponse(session)
	} else {
		result.Paging = &interfaces.PagingResponse{}
		rawQueryCursorSessions.closeSession(session.ID)
	}
	if session.HasTotalCount {
		totalCount := session.TotalCount
		result.TotalCount = &totalCount
	} else if !session.NeedTotal {
		result.TotalCount = nil
	}
	result.Warnings = append(result.Warnings, warnings...)
	return withExecutedResourceIDs(result, session.ResourceIDs), nil
}

func (rqs *rawQueryService) executeInitialDSLQuery(ctx context.Context, req *interfaces.RawQueryRequest) (*interfaces.RawQueryResponse, error) {
	queryCtx, cancel := queryExecutionContext(ctx, req.QueryTimeoutSec)
	defer cancel()

	requestQuery := req.Query.(map[string]any)
	queryMap := make(map[string]any, len(requestQuery)+2)
	for key, value := range requestQuery {
		// Paging and total-count behavior are controlled by the API contract.
		if key != "search_after" && key != "track_total_hits" {
			queryMap[key] = value
		}
	}
	isAggregation := hasOpenSearchAggregation(queryMap)
	if isAggregation {
		// Aggregations are expanded into row-oriented entries. Document paging
		// must not rewrite the aggregation into a hit query.
		queryMap["size"] = 0
		delete(queryMap, "from")
	} else {
		queryMap["size"] = req.Paging.Limit
		queryMap["from"] = req.Paging.Offset
	}
	if req.NeedTotal {
		queryMap["track_total_hits"] = true
	}
	resourceID, _ := queryMap["resource_id"].(string)
	if resourceID == "" {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("resource_id is required for DSL queries")
	}

	resource, err := rqs.resourceForQuery(queryCtx, resourceID)
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
			WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
	}
	warning, err := resourcelogic.EnsureResourceQueryable(queryCtx, resource)
	if err != nil {
		return nil, err
	}

	// Resource query permission was checked above. The Catalog is loaded
	// solely for query execution, so it must not impose catalog:view_detail.
	catalog, err := rqs.cs.InternalGetByID(queryCtx, resource.CatalogID, true)
	if err != nil {
		return nil, err
	}
	if catalog == nil {
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", resource.CatalogID))
	}
	if err := ensureCatalogEnabled(queryCtx, catalog); err != nil {
		return nil, err
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeOpenSearch {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("DSL input requires an opensearch catalog, got: %s", catalog.ConnectorType))
	}

	delete(queryMap, "resource_id")
	connector, err := rqs.cf.CreateConnectorInstance(queryCtx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, connectorInitializationError(ctx, err)
	}
	defer func() { _ = connector.Close(queryCtx) }()

	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("opensearch connector does not implement IndexConnector")
	}
	if err := indexConnector.Connect(queryCtx); err != nil {
		return nil, connectorInitializationError(ctx, err)
	}

	result, err := indexConnector.ExecuteRawQuery(queryCtx, resource.SourceIdentifier, queryMap)
	if err != nil {
		var validationErr *interfaces.RawAggregationValidationError
		if errors.As(err, &validationErr) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(validationErr.Error())
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("query execution failed")
	}
	if isAggregation {
		start := min(req.Paging.Offset, len(result.Entries))
		end := len(result.Entries)
		if req.Paging.Limit > 0 {
			end = min(start+req.Paging.Limit, end)
		}
		result.Entries = result.Entries[start:end]
	}
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	if !req.NeedTotal {
		result.TotalCount = nil
	}
	return withExecutedResourceIDs(result, []string{resourceID}), nil
}

func withExecutedResourceIDs(result *interfaces.RawQueryResponse, resourceIDs []string) *interfaces.RawQueryResponse {
	if result == nil {
		return nil
	}
	result.ResourceIDs = append([]string(nil), resourceIDs...)
	return result
}

func queryExecutionContext(ctx context.Context, queryTimeoutSec int) (context.Context, context.CancelFunc) {
	if queryTimeoutSec <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(queryTimeoutSec)*time.Second)
}

func targetDialectForCatalog(ctx context.Context, catalog *interfaces.Catalog) (string, error) {
	switch catalog.ConnectorType {
	case interfaces.ConnectorTypeMariaDB, interfaces.ConnectorTypeMySQL:
		return "mysql", nil
	case interfaces.ConnectorTypePostgreSQL:
		return "postgres", nil
	case interfaces.ConnectorTypeSQLServer:
		return "tsql", nil
	case interfaces.ConnectorTypeOracle:
		return "oracle", nil
	case interfaces.ConnectorTypeDoris:
		return "doris", nil
	case interfaces.ConnectorTypeHANA:
		return sqlglot.GenericDialect, nil
	default:
		return "", httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("unsupported connector type: %s", catalog.ConnectorType))
	}
}

func (rqs *rawQueryService) executeSQLTotalCount(ctx context.Context, catalog *interfaces.Catalog, sql string) (int64, error) {
	result, err := rqs.executeSQL(ctx, catalog, sql, interfaces.PagingModeSingle, &rawSQLBuildOptions{count: true})
	if err != nil {
		return 0, err
	}
	return rawQueryTotalCount(result)
}

func rawQueryTotalCount(result *interfaces.RawQueryResponse) (int64, error) {
	if result == nil || len(result.Entries) != 1 {
		return 0, fmt.Errorf("count query returned an invalid result")
	}
	value, ok := result.Entries[0][rawQueryTotalCountColumn]
	if !ok {
		return 0, fmt.Errorf("count query did not return %s", rawQueryTotalCountColumn)
	}
	if count, ok := common.NumberAsInt64(value); ok {
		if count < 0 {
			return 0, fmt.Errorf("count query returned an invalid number")
		}
		return count, nil
	}
	switch v := value.(type) {
	case string:
		count, err := strconv.ParseInt(v, 10, 64)
		if err != nil || count < 0 {
			return 0, fmt.Errorf("count query returned an invalid number")
		}
		return count, nil
	default:
		return 0, fmt.Errorf("count query returned an unsupported value")
	}
}

// extractResourceIDs extracts all {{.resource_id}} placeholders from FROM and JOIN table references.
func (rqs *rawQueryService) extractResourceIDs(ctx context.Context, query any, inputDialect string) ([]string, error) {
	if queryStr, ok := query.(string); ok {
		return rawQueryPolicy.ExtractTableResourceIDs(ctx, queryStr, inputDialect)
	}

	// OpenSearch DSL queries use a map and identify their index through resource_id.
	return []string{}, nil
}

// checkSameDataSource verifies that all resource IDs belong to one catalog and are queryable.
// It returns the catalog, warnings for deprecated resources, and any validation error.
func (rqs *rawQueryService) checkSameDataSource(ctx context.Context, resourceIDs []string) (*interfaces.Catalog, []string, error) {
	if len(resourceIDs) == 0 {
		return nil, nil, fmt.Errorf("no resource ids provided")
	}

	for _, resourceID := range resourceIDs {
		if err := rqs.rs.CheckResourcePermission(ctx, resourceID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
			return nil, nil, err
		}
	}
	resourcesByID, err := rqs.rs.InternalGetByIDs(ctx, resourceIDs)
	if err != nil {
		return nil, nil, err
	}
	resources := make([]*interfaces.Resource, 0, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		if resource, ok := resourcesByID[resourceID]; ok {
			resources = append(resources, resource)
		}
	}
	if len(resources) != len(resourceIDs) {
		resourceMap := make(map[string]bool)
		for _, r := range resources {
			resourceMap[r.ID] = true
		}
		for _, id := range resourceIDs {
			if !resourceMap[id] {
				return nil, nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
					WithErrorDetails(fmt.Sprintf("resource %s not found", id))
			}
		}
	}

	// Disabled or stale resources are rejected; deprecated resources produce warnings.
	warnings, err := resourcelogic.EnsureResourcesQueryable(ctx, resources)
	if err != nil {
		return nil, nil, err
	}

	// Verify that every resource belongs to the same catalog.
	catalogIDs := make(map[string]bool)
	for _, r := range resources {
		catalogIDs[r.CatalogID] = true
	}
	if len(catalogIDs) > 1 {
		localizedText, renderErr := i18n.Render(i18n.GetLanguageByCtx(ctx), "VegaBackend.Validation.Detail.MultiCatalogJoinNotSupported", nil)
		if renderErr != nil {
			logger.Errorf("localized message rendering failed: %v", renderErr)
		}
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusNotImplemented, verrors.VegaBackend_Query_MultiCatalogNotSupported).
			WithErrorDetails(localizedText)
	}

	// Load the catalog.
	var catalogID string
	for id := range catalogIDs {
		catalogID = id
		break
	}

	// checkSameDataSource has already resolved every referenced Resource through
	// its PEP. This internal load retains lifecycle checks below without turning
	// a Resource query into a public Catalog detail request.
	catalog, err := rqs.cs.InternalGetByID(ctx, catalogID, true)
	if err != nil {
		return nil, nil, err
	}
	if catalog == nil {
		return nil, nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", catalogID))
	}
	if err := ensureCatalogEnabled(ctx, catalog); err != nil {
		return nil, nil, err
	}

	return catalog, warnings, nil
}

func ensureCatalogEnabled(ctx context.Context, catalog *interfaces.Catalog) error {
	if catalog != nil && !catalog.Enabled {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Catalog_IsDisabled).
			WithErrorDetails("catalog is disabled")
	}
	return nil
}

// replaceResourceIDWithSchemaTable replaces resource placeholders with quoted source identifiers.
func (rqs *rawQueryService) replaceResourceIDWithSchemaTable(ctx context.Context,
	sql any, resourceIDs []string, _ *interfaces.Catalog, inputDialect string) (string, error) {

	replacedSQL := sql.(string)
	logger.Infof("Before replace - %s, resource_ids: %v", SafeQuerySummary(replacedSQL), resourceIDs)

	for _, resourceID := range resourceIDs {
		// Load resource metadata.
		resource, err := rqs.rs.InternalGetByID(ctx, nil, resourceID)
		if err != nil {
			return "", err
		}
		if resource == nil {
			return "", httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
				WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
		}

		// Replace both supported placeholder forms with the quoted identifier.
		placeholder1 := fmt.Sprintf("{{.%s}}", resourceID)
		placeholder2 := fmt.Sprintf("{{%s}}", resourceID)
		quotedIdentifier := resource.SourceIdentifier
		replacedSQL = replacePlaceholderInSQLCode(replacedSQL, placeholder1, quotedIdentifier, inputDialect)
		replacedSQL = replacePlaceholderInSQLCode(replacedSQL, placeholder2, quotedIdentifier, inputDialect)
	}

	logger.Infof("After replace - %s", SafeQuerySummary(replacedSQL))
	return replacedSQL, nil
}

func (rqs *rawQueryService) resourceForQuery(ctx context.Context, resourceID string) (*interfaces.Resource, error) {
	if err := rqs.rs.CheckResourcePermission(ctx, resourceID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return nil, err
	}
	return rqs.rs.InternalGetByID(ctx, nil, resourceID)
}

// replacePlaceholderInSQLCode preserves comments and quoted literals. They
// are not resource bindings and must remain semantically unchanged.
func replacePlaceholderInSQLCode(sql, placeholder, replacement, inputDialect string) string {
	resourceID := placeholder[2 : len(placeholder)-2]
	var output strings.Builder
	output.Grow(len(sql))
	for index := 0; index < len(sql); {
		if strings.HasPrefix(sql[index:], "--") {
			end := strings.IndexByte(sql[index:], '\n')
			if end < 0 {
				output.WriteString(sql[index:])
				break
			}
			end += index + 1
			output.WriteString(sql[index:end])
			index = end
			continue
		}
		if strings.HasPrefix(sql[index:], "/*") {
			end := strings.Index(sql[index+2:], "*/")
			if end < 0 {
				output.WriteString(sql[index:])
				break
			}
			end += index + 4
			output.WriteString(sql[index:end])
			index = end
			continue
		}
		if sql[index] == '\'' || sql[index] == '"' || sql[index] == '`' {
			quote := sql[index]
			end := index + 1
			for end < len(sql) {
				if sql[end] == quote {
					if end+1 < len(sql) && sql[end+1] == quote {
						end += 2
						continue
					}
					end++
					break
				}
				if (inputDialect == "mysql" || inputDialect == "doris") && (quote == '\'' || quote == '"') && sql[end] == '\\' && end+1 < len(sql) {
					end += 2
					continue
				}
				end++
			}
			output.WriteString(sql[index:end])
			index = end
			continue
		}
		if strings.HasPrefix(sql[index:], "{{") {
			// 与 MySQL RESOURCE token 一致，仅允许占位符两侧的 ASCII 空白。
			remaining := strings.TrimLeft(sql[index+2:], " \t\r\n")
			if strings.HasPrefix(remaining, resourceID) {
				suffix := strings.TrimLeft(remaining[len(resourceID):], " \t\r\n")
				if strings.HasPrefix(suffix, "}}") {
					output.WriteString(replacement)
					index = len(sql) - len(suffix) + 2
					continue
				}
			}
		}
		output.WriteByte(sql[index])
		index++
	}
	return output.String()
}

// executeSQL executes SQL queries and records pagination patterns.
func (rqs *rawQueryService) executeSQL(ctx context.Context, catalog *interfaces.Catalog, sql string,
	pagingMode interfaces.PagingMode, buildOptions *rawSQLBuildOptions) (*interfaces.RawQueryResponse, error) {
	// Create a connector
	connector, err := rqs.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Create connector failed", err)
		return nil, connectorInitializationError(ctx, err)
	}
	defer func() { _ = connector.Close(ctx) }()

	tableConnector, ok := connector.(interfaces.TableConnector)
	if !ok {
		return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("unsupported connector type: %s", catalog.ConnectorType))
	}
	// Establish the connection before any connector-specific operation. Some
	// connectors probe server capabilities during Connect, which SQL builders
	// need before selecting their syntax.
	if err := tableConnector.Connect(ctx); err != nil {
		otellog.LogError(ctx, "Connect connector before executing SQL failed", err)
		return nil, connectorInitializationError(ctx, err)
	}

	if buildOptions != nil {
		if buildOptions.count {
			sql = tableConnector.BuildCountSQL(sql)
		} else {
			sql = tableConnector.BuildPagedSQL(sql, buildOptions.offset, buildOptions.limit)
		}
	}
	logger.Infof("Executing query - %s, paging_mode: %s, catalog: %s", SafeQuerySummary(sql), pagingMode, catalog.Name)
	result, err := tableConnector.ExecuteRawSQL(ctx, sql)
	if err != nil {
		otellog.LogError(ctx, "Execute SQL failed", err)
		var sourceReadForbidden *interfaces.SourceReadForbiddenError
		if errors.As(err, &sourceReadForbidden) {
			return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Resource_SourceReadForbidden)
		}
		if detail, ok := interfaces.RequestSideQueryError(err); ok {
			return nil, httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(detail)
		}
		return nil, httperrors.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
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
	return httperrors.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("raw query rejected by read-only policy")
}

// prepareSQLBuilder 复用已授权的资源集，只补齐源端标识；SQL 生成由目标连接器负责。
func (rqs *rawQueryService) prepareSQLBuilder(ctx context.Context, query *sb.SelectBuilder, catalog *interfaces.Catalog, ids []string, warnings []string) (*preparedSQLQuery, error) {
	if catalog.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
		return nil, sqlBuilderQueryForbidden(ctx)
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
				return nil, sqlBuilderQueryForbidden(ctx)
			}
			if _, err := resourcelogic.EnsureResourceQueryable(ctx, resource); err != nil {
				return nil, err
			}
			if resource.Category != interfaces.ResourceCategoryTable {
				return nil, sqlBuilderQueryInvalid(ctx, "SQL queries currently only support physical table resources")
			}
			if resource.CatalogID != catalog.ID {
				return nil, sqlBuilderCursorChanged(ctx)
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
				return nil, sqlBuilderQueryInvalid(ctx, "paging offset and limit must not be negative")
			}
			offset, limit := uint64(options.offset), uint64(options.limit)
			if query.OffsetPart > math.MaxUint64-offset {
				return nil, sqlBuilderQueryInvalid(ctx, "paging offset exceeds the source range")
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
		return nil, sqlBuilderQueryInvalid(ctx, "connector does not support SQL Builder execution")
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
		return sqlBuilderQueryInvalid(ctx, "paging offset must not be negative")
	}
	offset := uint64(session.PageOffset)
	if query.OffsetPart > math.MaxUint64-offset {
		return sqlBuilderQueryInvalid(ctx, "paging offset exceeds the source range")
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
	executor, ok := connector.(interfaces.SQLBuilderExecutor)
	if !ok {
		return sqlBuilderQueryInvalid(ctx, "connector does not support SQL Rows execution")
	}
	// 保留请求身份与观测信息，Rows 生命周期由会话控制，不能随首屏 HTTP 请求结束。
	cursorCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	session.SQLCancel = cancel
	stop := watchSQLCursorRequest(ctx, cancel)
	rows, columns, err := executor.OpenSQLBuilder(cursorCtx, &read) //nolint:rowserrcheck // Rows 转交会话，ScanSQLBuilderRow 在读取结束时检查 Err。
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
	executor := session.SQLConnector.(interfaces.SQLBuilderExecutor)
	stop := watchSQLCursorRequest(ctx, session.SQLCancel)
	result := &interfaces.RawQueryResponse{
		Columns: session.SQLColumns,
		Entries: make([]map[string]any, 0, session.PageLimit),
	}
	var err error
	if session.SQLPendingRow != nil {
		result.Entries = append(result.Entries, session.SQLPendingRow)
		session.SQLPendingRow = nil
	}
	for len(result.Entries) <= session.PageLimit {
		var row map[string]any
		row, err = executor.ScanSQLBuilderRow(session.SQLRows, session.SQLColumns)
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

func sqlBuilderExecutionError(ctx context.Context, err error) error {
	var forbidden *interfaces.SourceReadForbiddenError
	if errors.As(err, &forbidden) {
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Resource_SourceReadForbidden)
	}
	if detail, ok := interfaces.RequestSideQueryError(err); ok {
		return sqlBuilderQueryInvalid(ctx, detail)
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
		return sqlBuilderQueryForbidden(ctx)
	}
	if catalog.ConnectorType != session.SQLConnectorType || catalog.UpdateTime != session.SQLCatalogUpdateTime {
		return sqlBuilderCursorChanged(ctx)
	}
	for _, table := range sqlBuilderTables(session.SQLQuery) {
		resource, err := rqs.rs.InternalGetByID(ctx, nil, table.ResourceID)
		if err != nil {
			return err
		}
		if resource == nil || resource.CatalogID != catalog.ID || resource.Category != interfaces.ResourceCategoryTable || resource.UpdateTime != session.SQLResourceVersions[table.ResourceID] || resource.SourceIdentifier != table.SourceIdentifier {
			return sqlBuilderCursorChanged(ctx)
		}
		if resource.Builtin && !interfaces.IsBuiltinAdmin(ctx) {
			return sqlBuilderQueryForbidden(ctx)
		}
	}
	return ctx.Err()
}

func sqlBuilderCursorChanged(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_ExecuteFailed).WithErrorDetails("cursor source metadata changed; start a new query")
}
func sqlBuilderQueryInvalid(ctx context.Context, detail string) error {
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

func sqlBuilderQueryForbidden(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden)
}
