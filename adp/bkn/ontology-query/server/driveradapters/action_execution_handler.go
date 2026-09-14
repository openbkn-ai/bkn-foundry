// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package driveradapters

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/comm-go/rest"
	attr "go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"ontology-query/common"
	"ontology-query/common/visitor"
	oerrors "ontology-query/errors"
	"ontology-query/interfaces"
)

// CheckActionExecutionByIn verifies the current internal caller against the
// trusted action execution dependencies without reading or executing data.
func (r *restHandler) CheckActionExecutionByIn(c *gin.Context) {
	requestVisitor := visitor.GenerateVisitor(c)
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{ID: requestVisitor.ID, Type: string(requestVisitor.Type)}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	req := interfaces.ActionExecutionRequest{}
	if err := common.BindPreciseJSON(c.Request.Body, &req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Parameter Failed: %s", err.Error()))
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	req.KNID = c.Param("kn_id")
	req.ActionTypeID = c.Param("at_id")
	req.Branch = c.DefaultQuery("branch", interfaces.MAIN_BRANCH)

	if err := r.ass.CheckActionExecution(ctx, &req); err != nil {
		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError).
				WithErrorDetails(err.Error())
		}
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusNoContent)
	rest.ReplyOK(c, http.StatusNoContent, nil)
}

// ExecuteActionByIn handles action execution request (internal)
func (r *restHandler) ExecuteActionByIn(c *gin.Context) {
	logger.Debug("Handler ExecuteActionByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.ExecuteAction(c, visitor)
}

// ExecuteActionByEx handles action execution request (external)
func (r *restHandler) ExecuteActionByEx(c *gin.Context) {
	logger.Debug("Handler ExecuteActionByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.ExecuteAction(c, visitor)
}

// ExecuteAction handles the action execution request
func (r *restHandler) ExecuteAction(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler ExecuteAction Start")
	startTime := time.Now()

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))
	otellog.LogInfo(ctx, fmt.Sprintf("Action execution request: [%s]", c.Request.RequestURI))

	// Get path parameters
	knID := c.Param("kn_id")
	atID := c.Param("at_id")
	branch := c.DefaultQuery("branch", interfaces.MAIN_BRANCH)
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("at_id").String(atID),
		attr.Key("branch").String(branch),
	)

	// Bind request body
	req := interfaces.ActionExecutionRequest{}
	if err := common.BindPreciseJSON(c.Request.Body, &req); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Parameter Failed: %s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	req.KNID = knID
	req.Branch = branch
	req.ActionTypeID = atID
	// Note: _instance_identities is optional
	// If not provided, the action will apply to all entities matching the action type's conditions

	// Execute action
	result, err := r.ass.ExecuteAction(ctx, &req)
	if err != nil {
		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError).
				WithErrorDetails(err.Error())
		}

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusAccepted)
	logger.Debugf("ExecuteAction completed in %dms", time.Since(startTime).Milliseconds())
	rest.ReplyOK(c, http.StatusAccepted, result)
}

// GetActionExecutionByIn handles get execution status request (internal)
func (r *restHandler) GetActionExecutionByIn(c *gin.Context) {
	logger.Debug("Handler GetActionExecutionByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.GetActionExecution(c, visitor, true)
}

// GetActionExecutionByEx handles get execution status request (external)
func (r *restHandler) GetActionExecutionByEx(c *gin.Context) {
	logger.Debug("Handler GetActionExecutionByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.GetActionExecution(c, visitor, false)
}

// GetActionExecution handles the get execution status request
func (r *restHandler) GetActionExecution(c *gin.Context, visitor hydra.Visitor, includeProxyContext bool) {
	logger.Debug("Handler GetActionExecution Start")
	startTime := time.Now()

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Get path parameters
	knID := c.Param("kn_id")
	executionID := c.Param("execution_id")
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("execution_id").String(executionID),
	)

	// Get execution
	result, err := r.ass.GetExecution(ctx, knID, executionID)
	if err != nil {
		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_InternalError).
				WithErrorDetails(err.Error())
		}

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debugf("GetActionExecution completed in %dms", time.Since(startTime).Milliseconds())
	if !includeProxyContext {
		result = redactActionExecutionProxyContext(result)
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// QueryActionLogsByIn handles query action logs request (internal)
func (r *restHandler) QueryActionLogsByIn(c *gin.Context) {
	logger.Debug("Handler QueryActionLogsByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.QueryActionLogs(c, visitor, true)
}

// QueryActionLogsByEx handles query action logs request (external)
func (r *restHandler) QueryActionLogsByEx(c *gin.Context) {
	logger.Debug("Handler QueryActionLogsByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.QueryActionLogs(c, visitor, false)
}

// QueryActionLogs handles the query action logs request (GET with query parameters)
func (r *restHandler) QueryActionLogs(c *gin.Context, visitor hydra.Visitor, includeProxyContext bool) {
	logger.Debug("Handler QueryActionLogs Start")
	startTime := time.Now()

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))
	otellog.LogInfo(ctx, fmt.Sprintf("行动日志查询请求参数: [%s]", c.Request.RequestURI))

	// Get path parameters
	knID := c.Param("kn_id")
	span.SetAttributes(attr.Key("kn_id").String(knID))

	// Bind query parameters
	query := interfaces.ActionLogQuery{}
	if err := c.ShouldBindQuery(&query); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Parameter Failed: %s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	query.KNID = knID

	keyword, err := normalizeActionLogKeyword(query.Keyword)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(err.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	query.Keyword = keyword

	// Convert GET query params to internal format
	if query.StartTimeFrom > 0 || query.StartTimeTo > 0 {
		query.StartTimeRange = []int64{query.StartTimeFrom, query.StartTimeTo}
	}

	searchAfter, err := parseSearchAfterQuery(query.SearchAfterStr)
	if err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails("invalid search_after cursor")
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}
	query.SearchAfter = searchAfter

	// Set default limit
	if query.Limit <= 0 {
		query.Limit = 20
	}
	if query.Limit > 1000 {
		query.Limit = 1000
	}

	// Query executions
	result, err := r.als.QueryExecutions(ctx, &query)
	if err != nil {
		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_QueryExecutionsFailed).
				WithErrorDetails(err.Error())
		}

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debugf("QueryActionLogs completed in %dms", time.Since(startTime).Milliseconds())
	if !includeProxyContext {
		result = redactActionExecutionListProxyContext(result)
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// GetActionLogByIn handles get single action log request (internal)
func (r *restHandler) GetActionLogByIn(c *gin.Context) {
	logger.Debug("Handler GetActionLogByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.GetActionLog(c, visitor, true)
}

// GetActionLogByEx handles get single action log request (external)
func (r *restHandler) GetActionLogByEx(c *gin.Context) {
	logger.Debug("Handler GetActionLogByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.GetActionLog(c, visitor, false)
}

// GetActionLog handles the get single action log request
func (r *restHandler) GetActionLog(c *gin.Context, visitor hydra.Visitor, includeProxyContext bool) {
	logger.Debug("Handler GetActionLog Start")
	startTime := time.Now()

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Get path parameters
	knID := c.Param("kn_id")
	logID := c.Param("log_id")
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("log_id").String(logID),
	)

	// Bind query parameters for results pagination
	query := interfaces.ActionLogDetailQuery{}
	if err := c.ShouldBindQuery(&query); err != nil {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(fmt.Sprintf("Binding Parameter Failed: %s", err.Error()))

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	query.KNID = knID
	query.LogID = logID

	// Set default values for results pagination
	if query.ResultsLimit <= 0 {
		query.ResultsLimit = 100
	}
	if query.ResultsLimit > 1000 {
		query.ResultsLimit = 1000
	}
	if query.ResultsOffset < 0 {
		query.ResultsOffset = 0
	}
	if query.ResultsOffset+query.ResultsLimit > interfaces.MaxActionResultsWindow {
		httpErr := rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
			WithErrorDetails(interfaces.ErrActionResultsWindowExceeded.Error())
		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	// Get execution log with pagination
	result, err := r.als.GetExecution(ctx, &query)
	if err != nil {
		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			httpErr = rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ExecutionNotFound).
				WithErrorDetails(err.Error())
			if errors.Is(err, interfaces.ErrActionResultsWindowExceeded) {
				httpErr = rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
					WithErrorDetails(err.Error())
			}
		}

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debugf("GetActionLog completed in %dms", time.Since(startTime).Milliseconds())
	if !includeProxyContext {
		result = redactActionExecutionProxyContext(result)
	}
	rest.ReplyOK(c, http.StatusOK, result)
}

// maxActionLogKeywordLength bounds the execution-id search term; ids are at most 36 characters.
const maxActionLogKeywordLength = 128

// normalizeActionLogKeyword trims the execution-id search term and rejects oversized input.
func normalizeActionLogKeyword(keyword string) (string, error) {
	keyword = strings.TrimSpace(keyword)
	if utf8.RuneCountInString(keyword) > maxActionLogKeywordLength {
		return "", fmt.Errorf("keyword must not exceed %d characters", maxActionLogKeywordLength)
	}
	return keyword, nil
}

// QueryActionLogResultsByIn handles the results page request of one execution (internal)
func (r *restHandler) QueryActionLogResultsByIn(c *gin.Context) {
	logger.Debug("Handler QueryActionLogResultsByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.QueryActionLogResults(c, visitor)
}

// QueryActionLogResultsByEx handles the results page request of one execution (external)
func (r *restHandler) QueryActionLogResultsByEx(c *gin.Context) {
	logger.Debug("Handler QueryActionLogResultsByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.QueryActionLogResults(c, visitor)
}

// QueryActionLogResults returns one page of an execution's per-instance results, filtered and
// paginated by the storage layer rather than in memory.
func (r *restHandler) QueryActionLogResults(c *gin.Context, visitor hydra.Visitor) {
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	})
	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	query := interfaces.ActionResultsQuery{}
	if err := c.ShouldBindQuery(&query); err != nil {
		replyActionResultsError(ctx, c, span, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter,
			fmt.Sprintf("Binding Parameter Failed: %s", err.Error()))
		return
	}
	query.KNID = c.Param("kn_id")
	query.LogID = c.Param("log_id")
	span.SetAttributes(
		attr.Key("kn_id").String(query.KNID),
		attr.Key("log_id").String(query.LogID),
	)

	if err := normalizeActionResultsQuery(&query); err != nil {
		replyActionResultsError(ctx, c, span, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter, err.Error())
		return
	}

	result, err := r.als.QueryResults(ctx, &query)
	if err != nil {
		switch {
		case errors.Is(err, interfaces.ErrActionResultsWindowExceeded):
			replyActionResultsError(ctx, c, span, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter, err.Error())
		case strings.Contains(err.Error(), "not found"):
			replyActionResultsError(ctx, c, span, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ExecutionNotFound, err.Error())
		default:
			replyActionResultsError(ctx, c, span, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_QueryExecutionsFailed, err.Error())
		}
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	rest.ReplyOK(c, http.StatusOK, result)
}

// actionResultStatuses are the per-instance result statuses a results page can filter by.
var actionResultStatuses = map[string]bool{
	interfaces.ObjectStatusSuccess:   true,
	interfaces.ObjectStatusFailed:    true,
	interfaces.ObjectStatusCancelled: true,
}

// normalizeActionResultsQuery applies the results page defaults and bounds: limit defaults to
// 100 and is capped at 1000, and the page must end within MaxActionResultsWindow.
func normalizeActionResultsQuery(query *interfaces.ActionResultsQuery) error {
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 1000 {
		query.Limit = 1000
	}
	if query.Offset < 0 {
		return fmt.Errorf("offset must not be negative")
	}
	if query.Offset+query.Limit > interfaces.MaxActionResultsWindow {
		return interfaces.ErrActionResultsWindowExceeded
	}
	query.Status = strings.TrimSpace(query.Status)
	if query.Status != "" && !actionResultStatuses[query.Status] {
		return fmt.Errorf("status must be one of success, failed, cancelled")
	}
	return nil
}

func replyActionResultsError(ctx context.Context, c *gin.Context, span trace.Span, status int, code string, details string) {
	httpErr := rest.NewHTTPError(ctx, status, code).WithErrorDetails(details)
	oteltrace.AddHttpAttrs4HttpError(span, httpErr)
	otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
	rest.ReplyError(c, httpErr)
}

func redactActionExecutionProxyContext(execution *interfaces.ActionExecution) *interfaces.ActionExecution {
	if execution == nil {
		return nil
	}
	redacted := *execution
	redacted.Proxy = nil
	redacted.ProxyVersion = 0
	redacted.ProxyModelVersion = ""
	redacted.ProxyPermissionSnapshot = nil
	return &redacted
}

func redactActionExecutionListProxyContext(
	list *interfaces.ActionExecutionList,
) *interfaces.ActionExecutionList {
	if list == nil {
		return nil
	}
	redacted := *list
	redacted.Entries = append([]interfaces.ActionExecution(nil), list.Entries...)
	for index := range redacted.Entries {
		entry := redactActionExecutionProxyContext(&redacted.Entries[index])
		redacted.Entries[index] = *entry
	}
	return &redacted
}

// CancelActionLogByIn handles cancel action execution request (internal)
func (r *restHandler) CancelActionLogByIn(c *gin.Context) {
	logger.Debug("Handler CancelActionLogByIn Start")
	visitor := visitor.GenerateVisitor(c)
	r.CancelActionLog(c, visitor)
}

// CancelActionLogByEx handles cancel action execution request (external)
func (r *restHandler) CancelActionLogByEx(c *gin.Context) {
	logger.Debug("Handler CancelActionLogByEx Start")
	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	visitor, err := r.verifyOAuth(ctx, c)
	if err != nil {
		return
	}
	r.CancelActionLog(c, visitor)
}

// CancelActionLog handles the cancel action execution request
func (r *restHandler) CancelActionLog(c *gin.Context, visitor hydra.Visitor) {
	logger.Debug("Handler CancelActionLog Start")
	startTime := time.Now()

	ctx, span := oteltrace.StartServerSpan(c)
	defer span.End()

	accountInfo := interfaces.AccountInfo{
		ID:   visitor.ID,
		Type: string(visitor.Type),
	}
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, accountInfo)

	oteltrace.AddHttpAttrs4API(span, oteltrace.GetAttrsByGinCtx(c))

	// Get path parameters
	knID := c.Param("kn_id")
	logID := c.Param("log_id")
	span.SetAttributes(
		attr.Key("kn_id").String(knID),
		attr.Key("log_id").String(logID),
	)

	// Bind request body (optional)
	req := interfaces.CancelExecutionRequest{}
	// Ignore binding errors since request body is optional
	_ = c.ShouldBindJSON(&req)

	// Cancel execution
	result, err := r.als.CancelExecution(ctx, knID, logID, req.Reason)
	if err != nil {
		httpErr, ok := err.(*rest.HTTPError)
		if !ok {
			// Check if it's a "not found" error
			if strings.Contains(err.Error(), "not found") {
				httpErr = rest.NewHTTPError(ctx, http.StatusNotFound, oerrors.OntologyQuery_ActionExecution_ExecutionNotFound).
					WithErrorDetails(err.Error())
			} else if strings.Contains(err.Error(), "cannot be cancelled") {
				httpErr = rest.NewHTTPError(ctx, http.StatusBadRequest, oerrors.OntologyQuery_ActionExecution_InvalidParameter).
					WithErrorDetails(err.Error())
			} else {
				httpErr = rest.NewHTTPError(ctx, http.StatusInternalServerError, oerrors.OntologyQuery_ActionExecution_CancelExecutionFailed).
					WithErrorDetails(err.Error())
			}
		}

		oteltrace.AddHttpAttrs4HttpError(span, httpErr)
		otellog.LogError(ctx, fmt.Sprintf("%s. %v", httpErr.BaseError.Description, httpErr.BaseError.ErrorDetails), httpErr)
		rest.ReplyError(c, httpErr)
		return
	}

	oteltrace.AddHttpAttrs4Ok(span, http.StatusOK)
	logger.Debugf("CancelActionLog completed in %dms", time.Since(startTime).Milliseconds())
	rest.ReplyOK(c, http.StatusOK, result)
}
