// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

// ResourceDataPageExecutor executes one normalized structured resource query.
// It is supplied by resource_data to keep connector semantics out of this
// package while sharing CursorSession lifecycle management with Raw Query.
type ResourceDataPageExecutor func(context.Context, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error)

// ResourceDataPageRunner executes a page without requiring a function callback.
type ResourceDataPageRunner interface {
	ExecutePage(context.Context, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error)
}

func (execute ResourceDataPageExecutor) ExecutePage(ctx context.Context,
	params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
	return execute(ctx, params)
}

func ExecuteInitialResourceDataCursor(ctx context.Context, accountID string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams, execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	return ExecuteInitialResourceDataCursorWithCategory(ctx, accountID, resource, resource.Category, params, execute)
}

// ExecuteInitialResourceDataCursorWithCategory uses the physical data category
// when a virtual resource (such as a Derived Logic View) delegates to another
// connector. This keeps its first page and continuation on the same strategy.
func ExecuteInitialResourceDataCursorWithCategory(ctx context.Context, accountID string, resource *interfaces.Resource,
	paginationCategory string, params *interfaces.ResourceDataQueryParams,
	execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	return ExecuteInitialResourceDataCursorWithCategoryRunner(ctx, accountID, resource, nil, paginationCategory, params, execute)
}

// ExecuteInitialResourceDataCursorWithCategoryRunner starts a cursor with a page runner.
func ExecuteInitialResourceDataCursorWithCategoryRunner(ctx context.Context, accountID string, resource *interfaces.Resource,
	source *interfaces.Resource, paginationCategory string, params *interfaces.ResourceDataQueryParams,
	execute ResourceDataPageRunner) (*interfaces.ResourceDataQueryResult, error) {
	quotaOwnerID := interfaces.CursorQuotaOwner(ctx)
	if quotaOwnerID == "" {
		quotaOwnerID = accountID
	}
	session, err := rawQueryCursorSessions.createResourceData(accountID, quotaOwnerID, resource, params)
	if err != nil {
		return nil, cursorSessionCreateError(ctx, err)
	}
	session.ResourceDataCategory = paginationCategory
	session.ResourceDataSource = cursorSourceBinding(source)
	session.PageOffset = params.Paging.Offset
	session.Lock()
	defer session.Unlock()
	result, err := executeResourceDataCursorPage(ctx, session, execute)
	if err != nil {
		rawQueryCursorSessions.remove(session.ID)
	}
	return result, err
}

func ExecuteResourceDataCursorContinuation(ctx context.Context, accountID string, resource *interfaces.Resource, cursor string,
	execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	return ExecuteResourceDataCursorContinuationWithRunner(ctx, accountID, resource, nil, cursor, execute)
}

// ExecuteResourceDataCursorContinuationWithRunner continues a cursor with a page runner.
func ExecuteResourceDataCursorContinuationWithRunner(ctx context.Context, accountID string, resource, source *interfaces.Resource,
	cursor string, execute ResourceDataPageRunner) (*interfaces.ResourceDataQueryResult, error) {
	session, ok := rawQueryCursorSessions.acquire(cursor)
	if !ok || session.ResourceDataParams == nil {
		if ok {
			rawQueryCursorSessions.release(session)
		}
		return nil, cursorNotFoundError(ctx)
	}
	defer rawQueryCursorSessions.release(session)
	if session.AccountID != accountID {
		return nil, httperrors.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor does not belong to the current account")
	}
	if resource == nil || session.ResourceDataResourceID != resource.ID {
		return nil, cursorNotFoundError(ctx)
	}
	if session.ResourceDataUpdateTime != resource.UpdateTime || !sameCursorSource(session.ResourceDataSource, source) {
		rawQueryCursorSessions.closeSession(session.ID)
		return nil, cursorNotFoundError(ctx)
	}
	return executeResourceDataCursorPage(ctx, session, execute)
}

func cursorSourceBinding(source *interfaces.Resource) *interfaces.ResourceDataCursorSource {
	if source == nil {
		return nil
	}
	return &interfaces.ResourceDataCursorSource{
		ID: source.ID, UpdateTime: source.UpdateTime, Category: source.Category,
		LocalIndexName: source.LocalIndexName, LocalIndexStatus: source.LocalIndexStatus,
	}
}

func sameCursorSource(saved *interfaces.ResourceDataCursorSource, source *interfaces.Resource) bool {
	current := cursorSourceBinding(source)
	if saved == nil || current == nil {
		return saved == nil && current == nil
	}
	return *saved == *current
}

func executeResourceDataCursorPage(ctx context.Context, session *interfaces.CursorSession,
	execute ResourceDataPageRunner) (*interfaces.ResourceDataQueryResult, error) {
	params := cloneResourceDataQueryParams(session.ResourceDataParams)
	params.NeedTotal = session.ResourceDataParams.NeedTotal && !session.HasTotalCount
	params.Paging = interfaces.PagingRequest{
		Mode:   interfaces.PagingModeSingle,
		Offset: session.PageOffset,
		Limit:  session.PageLimit + 1,
	}
	if session.ResourceDataCategory == interfaces.ResourceCategoryIndex {
		// OpenSearch uses search_after and must not request limit+1: size plus
		// the first-page offset can otherwise exceed max_result_window.
		params.Paging.Limit = session.PageLimit
	}
	params.SearchAfter = append([]any(nil), session.SearchAfter...)
	page, err := execute.ExecutePage(ctx, params)
	if err != nil {
		return nil, err
	}
	entries, total := page.Entries, page.TotalCount
	if session.ResourceDataParams.NeedTotal && !session.HasTotalCount {
		session.TotalCount = total
		session.HasTotalCount = true
	}
	responseTotal := total
	if session.HasTotalCount {
		responseTotal = session.TotalCount
	}
	page.TotalCount = responseTotal
	page.NeedTotal = session.ResourceDataParams.NeedTotal
	hasNext := len(entries) > session.PageLimit
	if session.ResourceDataCategory == interfaces.ResourceCategoryIndex {
		if session.HasTotalCount {
			hasNext = len(entries) == session.PageLimit &&
				int64(session.PageOffset+len(entries)) < session.TotalCount && len(page.SearchAfter) > 0
		} else {
			hasNext = len(entries) == session.PageLimit && len(page.SearchAfter) > 0
		}
	}
	if !hasNext {
		rawQueryCursorSessions.closeSession(session.ID)
		page.Paging = &interfaces.PagingResponse{}
		return page, nil
	}
	if session.ResourceDataCategory != interfaces.ResourceCategoryIndex {
		entries = entries[:session.PageLimit]
	}
	page.Entries = entries
	session.PageOffset += len(entries)
	if len(page.SearchAfter) > 0 {
		session.SearchAfter = append([]any(nil), page.SearchAfter...)
	}
	rawQueryCursorSessions.markPageSuccess(session)
	page.Paging = cursorPagingResponse(session)
	return page, nil
}

func cursorNotFoundError(ctx context.Context) error {
	return httperrors.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("cursor not found or expired")
}
