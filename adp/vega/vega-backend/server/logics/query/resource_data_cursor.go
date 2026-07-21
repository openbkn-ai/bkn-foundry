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
	session, err := rawQueryCursorSessions.createResourceData(accountID, resource.CatalogID, resource.ID, params)
	if err != nil {
		return nil, cursorSessionLimitError(ctx)
	}
	session.Offset = params.Paging.Offset
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
	params.Paging = interfaces.PagingRequest{}
	entries, total, err := execute(ctx, params)
	if err != nil {
		return nil, err
	}
	if len(entries) <= session.PageSize {
		rawQueryCursorSessions.closeSession(session.ID)
		return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: &interfaces.PagingResponse{}}, nil
	}
	entries = entries[:session.PageSize]
	session.Offset += session.PageSize
	rawQueryCursorSessions.markPageSuccess(session)
	return &interfaces.ResourceDataQueryResult{Entries: entries, TotalCount: total, Paging: cursorPagingResponse(session)}, nil
}

func cursorNotFoundError(ctx context.Context) error {
	return rest.NewHTTPError(ctx, http.StatusNotFound, verrors.VegaBackend_Query_InvalidParameter).
		WithErrorDetails("cursor not found or expired")
}
