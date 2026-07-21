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
	return ExecuteInitialResourceDataCursorWithCategory(ctx, accountID, resource, resource.Category, params, execute)
}

// ExecuteInitialResourceDataCursorWithCategory uses the physical data category
// when a virtual resource (such as a Derived Logic View) delegates to another
// connector. This keeps its first page and continuation on the same strategy.
func ExecuteInitialResourceDataCursorWithCategory(ctx context.Context, accountID string, resource *interfaces.Resource,
	paginationCategory string, params *interfaces.ResourceDataQueryParams,
	execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	session, err := rawQueryCursorSessions.createResourceData(accountID, resource, params)
	if err != nil {
		return nil, cursorSessionLimitError(ctx)
	}
	session.ResourceDataCategory = paginationCategory
	session.Offset = params.Paging.Offset
	session.Lock()
	defer session.Unlock()
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
	session.Lock()
	defer session.Unlock()
	if time.Now().Unix() >= atomic.LoadInt64(&session.ExpiresAtSec) {
		rawQueryCursorSessions.expire(session.ID)
		return nil, cursorNotFoundError(ctx)
	}
	return executeResourceDataCursorPage(ctx, session, execute)
}

func executeResourceDataCursorPage(ctx context.Context, session *interfaces.CursorSession,
	execute ResourceDataPageExecutor) (*interfaces.ResourceDataQueryResult, error) {
	params := cloneResourceDataQueryParams(session.ResourceDataParams)
	params.Offset = session.Offset
	params.Limit = session.Limit + 1
	if session.ResourceDataCategory == interfaces.ResourceCategoryIndex {
		params.TrackTotalHits = params.NeedTotal
		if params.NeedTotal {
			// Exact totals let the cursor determine the final page without a
			// look-ahead hit.
			params.Limit = session.Limit
		}
	}
	params.Paging = interfaces.PagingRequest{}
	params.SearchAfter = append([]any(nil), session.SearchAfter...)
	entries, total, err := execute(ctx, params)
	if err != nil {
		return nil, err
	}
	hasNext := len(entries) > session.Limit
	if session.ResourceDataCategory == interfaces.ResourceCategoryIndex {
		if params.NeedTotal {
			hasNext = len(entries) == session.Limit &&
				int64(session.Offset+len(entries)) < total && len(params.SearchAfter) > 0
		} else {
			hasNext = len(entries) == session.Limit && len(params.SearchAfter) > 0
		}
	}
	if !hasNext {
		rawQueryCursorSessions.closeSession(session.ID)
		return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: &interfaces.PagingResponse{}, NeedTotal: session.ResourceDataParams.NeedTotal}, nil
	}
	if session.ResourceDataCategory != interfaces.ResourceCategoryIndex {
		entries = entries[:session.Limit]
	}
	session.Offset += len(entries)
	if len(params.SearchAfter) > 0 {
		session.SearchAfter = append([]any(nil), params.SearchAfter...)
	}
	rawQueryCursorSessions.markPageSuccess(session)
	return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: cursorPagingResponse(session), NeedTotal: session.ResourceDataParams.NeedTotal}, nil
}

func cursorNotFoundError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("cursor not found or expired")
}
