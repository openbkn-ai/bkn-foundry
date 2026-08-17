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

	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"

	"vega-backend/common"
	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	"vega-backend/locale"
	"vega-backend/logics/catalog"
	"vega-backend/logics/connector/factory"
	opensearchconnector "vega-backend/logics/connector/local/index/opensearch"
	"vega-backend/logics/query/querypolicy"
	"vega-backend/logics/query/sqlglot"
	resourcelogic "vega-backend/logics/resource"
)

var (
	rqServiceOnce sync.Once
	rqService     interfaces.RawQueryService
)

type rawQueryService struct {
	cf interfaces.ConnectorFactory
	cs interfaces.CatalogService
	rs interfaces.ResourceService
}

const rawQueryTotalCountColumn = "_raw_query_total_count"

// NewRawQueryService creates SQL query services (singleton pattern)
func NewRawQueryService(appSetting *common.AppSetting) interfaces.RawQueryService {
	rawQueryCursorSessions.configure(appSetting.QuerySetting.CursorMaxSessions)
	rqServiceOnce.Do(func() {
		rqService = &rawQueryService{
			cf: factory.GetFactory(appSetting),
			cs: catalog.NewCatalogService(appSetting),
			rs: resourcelogic.NewResourceService(appSetting),
		}
	})
	return rqService
}

// Execute executes SQL queries
func (rqs *rawQueryService) Execute(ctx context.Context, req *interfaces.RawQueryRequest) (resp *interfaces.RawQueryResponse, err error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "SQLQueryExecute")
	defer span.End()

	// Collect resource status alerts (deprecated hits) and attach them to the response on each successful return path.
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
	queryCtx, cancel := queryExecutionContext(ctx, req.QueryTimeoutSec)
	defer cancel()

	prepared, err := rqs.prepareSQLQuery(queryCtx, req)
	if err != nil {
		return nil, err
	}

	var totalCount int64
	if req.NeedTotal {
		totalCount, err = rqs.executeSQLTotalCount(queryCtx, prepared.catalog, prepared.sql)
		if err != nil {
			return nil, err
		}
	}
	result, err := rqs.executeSQL(queryCtx, prepared.catalog, prepared.sql, interfaces.PagingModeSingle, &rawSQLBuildOptions{
		offset: req.Paging.Offset,
		limit:  req.Paging.Limit,
	})
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
		totalCount, err = rqs.executeSQLTotalCount(queryCtx, prepared.catalog, prepared.sql)
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
		return nil, cursorSessionLimitError(ctx)
	}
	session.Offset = req.Paging.Offset
	session.TotalCount = totalCount
	session.HasTotalCount = req.NeedTotal
	session.NeedTotal = req.NeedTotal
	bindCursorResource(session, req)
	session.Lock()
	defer session.Unlock()
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
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor not found or expired")
	}
	defer rawQueryCursorSessions.release(session)
	if session.AccountID != accountIDFromContext(ctx) {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor does not belong to the current account")
	}
	if err := validateCursorResourceBinding(ctx, session, req); err != nil {
		return nil, err
	}
	queryCtx, cancel := queryExecutionContext(ctx, session.QueryTimeoutSec)
	defer cancel()
	catalog, warnings, err := rqs.checkSameDataSource(queryCtx, session.ResourceIDs)
	if err != nil {
		return nil, err
	}
	if catalog.ID != session.CatalogID {
		return nil, rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("cursor catalog changed")
	}
	switch session.QueryFormat {
	case interfaces.QueryFormatSQL:
		return rqs.executeSQLCursorPage(queryCtx, session, catalog, warnings)
	case interfaces.QueryFormatDSL:
		return rqs.executeOpenSearchCursorPage(queryCtx, session, catalog, warnings)
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
	inputDialect := req.EffectiveInputDialect()
	resourceIDs, err := rqs.extractResourceIDs(ctx, req.Query, inputDialect)
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
	targetDialect, err := targetDialectForCatalog(ctx, catalog)
	if err != nil {
		return nil, err
	}
	replacedSQL, err := rqs.replaceResourceIDWithSchemaTable(ctx, req.Query, resourceIDs, catalog, inputDialect)
	if err != nil {
		return nil, err
	}
	allowedReferences, err := rqs.resourceSourceIdentifiers(ctx, resourceIDs, inputDialect)
	if err != nil {
		return nil, err
	}
	if err := validateSQLPolicy(ctx, replacedSQL, inputDialect, allowedReferences, inputDialect == targetDialect); err != nil {
		return nil, err
	}
	finalSQL := replacedSQL
	if inputDialect != targetDialect {
		result, err := sqlglot.TranspileSQL(ctx, replacedSQL, inputDialect, targetDialect)
		if err != nil {
			return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails("failed to compile SQL query")
		}
		finalSQL = result.SQL
		targetReferences, err := rqs.resourceSourceIdentifiers(ctx, resourceIDs, targetDialect)
		if err != nil {
			return nil, err
		}
		// The connector executes finalSQL, not the input-dialect SQL validated
		// above. Revalidate the target-dialect output so the read-only and
		// table-reference boundaries apply to the executable statement.
		if err := validateSQLPolicy(ctx, finalSQL, targetDialect, targetReferences, true); err != nil {
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
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("failed to validate SQL query")
	}
	if err := rawQueryPolicy.ValidateTableReferences(ctx, sql, dialect, allowedReferences); err != nil {
		if httpErr := rawQueryValidationError(ctx, err); httpErr != nil {
			return httpErr
		}
		return rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("failed to validate SQL resource references")
	}
	return nil
}

func (rqs *rawQueryService) resourceSourceIdentifiers(ctx context.Context, resourceIDs []string, dialect string) ([]string, error) {
	identifiers := make([]string, 0, len(resourceIDs))
	for _, resourceID := range resourceIDs {
		resource, err := rqs.rs.GetByID(ctx, resourceID)
		if err != nil {
			return nil, err.(*rest.HTTPError)
		}
		if resource == nil || strings.TrimSpace(resource.SourceIdentifier) == "" {
			return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
				WithErrorDetails(fmt.Sprintf("resource %s has no queryable source", resourceID))
		}
		identifiers = append(identifiers, quotedResourceSourceIdentifier(resource, dialect))
	}
	return identifiers, nil
}

func trimSQLTerminator(sql string) string {
	return strings.TrimSuffix(strings.TrimSpace(sql), ";")
}

func (rqs *rawQueryService) executeSQLCursorPage(ctx context.Context, session *interfaces.CursorSession, catalog *interfaces.Catalog, warnings []string) (*interfaces.RawQueryResponse, error) {
	pageCtx, cancel := queryExecutionContext(ctx, session.QueryTimeoutSec)
	defer cancel()

	result, err := rqs.executeSQL(pageCtx, catalog, session.CompiledSQL, interfaces.PagingModeCursor, &rawSQLBuildOptions{
		offset: session.Offset,
		limit:  session.Limit + 1,
	})
	if err != nil {
		return nil, err
	}
	hasNext := len(result.Entries) > session.Limit
	if hasNext {
		result.Entries = result.Entries[:session.Limit]
		session.Offset += session.Limit
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
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor paging does not support OpenSearch aggregation queries")
	}
	session, err := rawQueryCursorSessions.create(
		accountIDFromContext(ctx), catalog.ID, []string{queryResourceID(req.Query)}, "", req.Paging.Limit, req.Paging.KeepAliveSec, req.QueryTimeoutSec,
	)
	if err != nil {
		return nil, cursorSessionLimitError(ctx)
	}
	session.QueryFormat = interfaces.QueryFormatDSL
	bindCursorResource(session, req)
	session.OpenSearchQuery = query
	session.OpenSearchIndex = indexName
	session.Offset = req.Paging.Offset
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

func cursorSessionLimitError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusTooManyRequests, verrors.VegaBackend_Query_CursorSessionLimitExceeded).
		WithErrorDetails("cursor session limit reached, please retry later")
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
			return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorResourceChanged).
				WithErrorDetails("raw query cursor cannot be used for resource data paging")
		}
		return nil
	}
	if req == nil || req.ResourceDataResourceID != session.ResourceDataResourceID {
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorResourceChanged).
			WithErrorDetails("cursor does not belong to the current resource")
	}
	if req.ResourceDataUpdateTime != session.ResourceDataUpdateTime {
		rawQueryCursorSessions.closeSession(session.ID)
		return rest.NewHTTPError(ctx, http.StatusConflict, verrors.VegaBackend_Query_CursorResourceChanged).
			WithErrorDetails("resource changed after cursor creation")
	}
	return nil
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
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("connector initialization failed")
	}
	defer func() { _ = connector.Close(pageCtx) }()
	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("opensearch connector does not implement IndexConnector")
	}
	result, err := indexConnector.ExecuteRawQuery(pageCtx, session.OpenSearchIndex, query)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
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
			int64(session.Offset+len(result.Entries)) < session.TotalCount
		hasNext = len(result.Entries) == session.Limit && hasMoreResults && len(result.SearchAfter) > 0
	} else {
		// Without an exact total, a full page may be the final page. Preserve
		// the cursor and let one final empty request close that exact-multiple
		// case; this keeps size within OpenSearch's result-window limit.
		hasNext = len(result.Entries) == session.Limit && len(result.SearchAfter) > 0
	}
	if hasNext {
		session.SearchAfter = append([]any(nil), result.SearchAfter...)
		session.Offset += len(result.Entries)
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
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("resource_id is required for DSL queries")
	}

	resource, err := rqs.rs.GetByID(queryCtx, resourceID)
	if err != nil {
		return nil, err.(*rest.HTTPError)
	}
	if resource == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
			WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
	}
	warning, err := resourcelogic.EnsureResourceQueryable(queryCtx, resource)
	if err != nil {
		return nil, err
	}

	catalog, err := rqs.cs.GetByID(queryCtx, resource.CatalogID, true)
	if err != nil {
		return nil, err.(*rest.HTTPError)
	}
	if catalog == nil {
		return nil, rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_CatalogNotFound).
			WithErrorDetails(fmt.Sprintf("catalog %s not found", resource.CatalogID))
	}
	if err := ensureCatalogEnabled(queryCtx, catalog); err != nil {
		return nil, err
	}
	if catalog.ConnectorType != interfaces.ConnectorTypeOpenSearch {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("DSL input requires an opensearch catalog, got: %s", catalog.ConnectorType))
	}

	delete(queryMap, "resource_id")
	connector, err := rqs.cf.CreateConnectorInstance(queryCtx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("connector initialization failed")
	}
	defer func() { _ = connector.Close(queryCtx) }()
	indexConnector, ok := connector.(interfaces.IndexConnector)
	if !ok {
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("opensearch connector does not implement IndexConnector")
	}
	result, err := indexConnector.ExecuteRawQuery(queryCtx, resource.SourceIdentifier, queryMap)
	if err != nil {
		var validationErr *opensearchconnector.RawAggregationValidationError
		if errors.As(err, &validationErr) {
			return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
				WithErrorDetails(validationErr.Error())
		}
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
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
	default:
		return "", rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
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
	switch v := value.(type) {
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case float64:
		if math.Trunc(v) != v || v < 0 || v > math.MaxInt64 {
			return 0, fmt.Errorf("count query returned an invalid number")
		}
		return int64(v), nil
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

	// Get all resources
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
		return nil, nil, rest.NewHTTPError(ctx, http.StatusNotImplemented, verrors.VegaBackend_Query_MultiCatalogNotSupported).
			WithErrorDetails(locale.ValidationDetail(ctx, "MultiCatalogJoinNotSupported", nil))
	}

	// Load the catalog.
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

// replaceResourceIDWithSchemaTable replaces resource placeholders with quoted source identifiers.
func (rqs *rawQueryService) replaceResourceIDWithSchemaTable(ctx context.Context,
	sql any, resourceIDs []string, catalog *interfaces.Catalog, inputDialect string) (string, error) {

	replacedSQL := sql.(string)
	logger.Infof("Before replace - %s, resource_ids: %v", SafeQuerySummary(replacedSQL), resourceIDs)

	for _, resourceID := range resourceIDs {
		// Load resource metadata.
		resource, err := rqs.rs.GetByID(ctx, resourceID)
		if err != nil {
			return "", err.(*rest.HTTPError)
		}
		if resource == nil {
			return "", rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_ResourceNotFound).
				WithErrorDetails(fmt.Sprintf("resource %s not found", resourceID))
		}

		// Use the resource source identifier as the table reference.
		// schemaTable := fmt.Sprintf(`%s.%s`, catalog.Name, resource.SourceIdentifier)

		// Replace both supported placeholder forms with the quoted identifier.
		placeholder1 := fmt.Sprintf("{{.%s}}", resourceID)
		placeholder2 := fmt.Sprintf("{{%s}}", resourceID)
		quotedIdentifier := quotedResourceSourceIdentifier(resource, inputDialect)
		replacedSQL = replacePlaceholderInSQLCode(replacedSQL, placeholder1, quotedIdentifier, inputDialect)
		replacedSQL = replacePlaceholderInSQLCode(replacedSQL, placeholder2, quotedIdentifier, inputDialect)
	}

	logger.Infof("After replace - %s", SafeQuerySummary(replacedSQL))
	return replacedSQL, nil
}

func quotedResourceSourceIdentifier(resource *interfaces.Resource, dialect string) string {
	sourceIdentifier := strings.TrimSpace(resource.SourceIdentifier)
	schema := strings.TrimSpace(resource.Schema)
	table := sourceIdentifier
	if schema != "" {
		prefixLength := len(schema) + 1
		if len(sourceIdentifier) >= prefixLength &&
			strings.EqualFold(sourceIdentifier[:prefixLength], schema+".") {
			table = sourceIdentifier[prefixLength:]
		} else if separator := strings.IndexByte(sourceIdentifier, '.'); separator >= 0 &&
			strings.EqualFold(strings.TrimSpace(sourceIdentifier[:separator]), schema) {
			table = sourceIdentifier[separator+1:]
		}
	} else if separator := strings.IndexByte(sourceIdentifier, '.'); separator >= 0 {
		schema, table = sourceIdentifier[:separator], sourceIdentifier[separator+1:]
	}
	if schema == "" {
		return quoteSQLIdentifier(strings.TrimSpace(table), dialect)
	}
	return quoteSQLIdentifier(schema, dialect) + "." + quoteSQLIdentifier(strings.TrimSpace(table), dialect)
}

func quoteSQLIdentifier(identifier, dialect string) string {
	switch dialect {
	case "mysql":
		return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
	case "tsql":
		return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
	default:
		return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
	}
}

// replacePlaceholderInSQLCode preserves comments and quoted literals. They
// are not resource bindings and must remain semantically unchanged.
func replacePlaceholderInSQLCode(sql, placeholder, replacement, inputDialect string) string {
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
				if inputDialect == "mysql" && quote == '\'' && sql[end] == '\\' && end+1 < len(sql) {
					end += 2
					continue
				}
				end++
			}
			output.WriteString(sql[index:end])
			index = end
			continue
		}
		if strings.HasPrefix(sql[index:], placeholder) {
			output.WriteString(replacement)
			index += len(placeholder)
			continue
		}
		output.WriteByte(sql[index])
		index++
	}
	return output.String()
}

type rawSQLBuildOptions struct {
	offset int
	limit  int
	count  bool
}

// executeSQL executes SQL queries and records pagination patterns.
func (rqs *rawQueryService) executeSQL(ctx context.Context, catalog *interfaces.Catalog, sql string,
	pagingMode interfaces.PagingMode, buildOptions *rawSQLBuildOptions) (*interfaces.RawQueryResponse, error) {
	// Create a connector
	connector, err := rqs.cf.CreateConnectorInstance(ctx, catalog.ConnectorType, catalog.ConnectorCfg)
	if err != nil {
		otellog.LogError(ctx, "Create connector failed", err)
		return nil, rest.NewHTTPError(ctx, http.StatusInternalServerError, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("connector initialization failed")
	}
	defer func() { _ = connector.Close(ctx) }()

	tableConnector, ok := connector.(interfaces.TableConnector)
	if !ok {
		return nil, rest.NewHTTPError(ctx, http.StatusBadRequest, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("unsupported connector type: %s", catalog.ConnectorType))
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
