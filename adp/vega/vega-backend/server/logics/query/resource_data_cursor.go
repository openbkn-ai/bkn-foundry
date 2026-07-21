package query

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/rest"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
)

// ResourceDataPageExecutor executes one normalized structured resource query.
// It is supplied by resource_data to keep connector semantics out of this
// package while sharing CursorSession lifecycle management with Raw Query.
type ResourceDataPageExecutor func(context.Context, *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error)

func ExecuteInitialResourceDataCursor(ctx context.Context, accountID string, resource *interfaces.Resource,
	params *interfaces.ResourceDataQueryParams, execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	session, err := rawQueryCursorSessions.createResourceData(accountID, resource, params)
	if err != nil {
		return nil, cursorSessionLimitError(ctx)
	}
	session.Offset = params.Paging.Offset
	session.mu.Lock()
	defer session.mu.Unlock()
	result, err := executeResourceDataCursorPage(ctx, session, execute)
	if err != nil {
		rawQueryCursorSessions.remove(session.ID)
	}
	return result, err
}

func ExecuteResourceDataCursorContinuation(ctx context.Context, accountID, resourceID, cursor string,
	execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	session, ok := rawQueryCursorSessions.get(cursor)
	if !ok || session.ResourceDataParams == nil {
		return nil, cursorNotFoundError(ctx)
	}
	if session.AccountID != accountID {
		return nil, rest.NewHTTPError(ctx, http.StatusForbidden, verrors.VegaBackend_Query_InvalidParameter).
			WithErrorDetails("cursor does not belong to the current account")
	}
	if session.ResourceDataResourceID != resourceID {
		return nil, cursorNotFoundError(ctx)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
		rawQueryCursorSessions.expire(session.ID)
		return nil, cursorNotFoundError(ctx)
	}
	return executeResourceDataCursorPage(ctx, session, execute)
}

func executeResourceDataCursorPage(ctx context.Context, session *cursorSession,
	execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	params := cloneResourceDataQueryParams(session.ResourceDataParams)
	params.Offset = session.Offset
	params.Limit = session.PageSize + 1
	if session.ResourceDataCategory == interfaces.ResourceCategoryIndex {
		// The connector returns the search_after value from the last fetched hit.
		// Fetch exactly one page so that value belongs to a returned entry rather
		// than the dropped lookahead entry.
		params.Limit = session.PageSize
	}
	params.Paging = interfaces.PagingRequest{}
	params.SearchAfter = append([]any(nil), session.SearchAfter...)
	entries, total, err := execute(ctx, params)
	if err != nil {
		return nil, err
	}
	hasNext := len(entries) > session.PageSize
	if session.ResourceDataCategory == interfaces.ResourceCategoryIndex {
		hasNext = len(entries) == session.PageSize &&
			int64(session.Offset+len(entries)) < total && len(params.SearchAfter) > 0
	}
	if !hasNext {
		rawQueryCursorSessions.closeSession(session.ID)
		return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: &interfaces.PagingResponse{}, IncludeTotal: session.ResourceDataParams.NeedTotal}, nil
	}
	if session.ResourceDataCategory != interfaces.ResourceCategoryIndex {
		entries = entries[:session.PageSize]
	}
	session.Offset += len(entries)
	if len(params.SearchAfter) > 0 {
		session.SearchAfter = append([]any(nil), params.SearchAfter...)
	}
	rawQueryCursorSessions.markPageSuccess(session)
	return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: cursorPagingResponse(session), IncludeTotal: session.ResourceDataParams.NeedTotal}, nil
}

func cursorNotFoundError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("cursor not found or expired")
}
