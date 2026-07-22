package query

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
)

func TestResourceDataCursorPagesAndCloses(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1"}
	params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{
		Mode:  interfaces.PagingModeCursor,
		Limit: 2,
	}}
	var offsets []int
	executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
		offsets = append(offsets, pageParams.Offset)
		if pageParams.Offset == 0 {
			return []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}, 3, nil
		}
		return []map[string]any{{"id": 3}}, 3, nil
	}

	first, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource, params, executor)
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	assert.Len(t, first.Entries, 2)

	final, err := ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *first.Paging.NextCursor, executor)
	require.NoError(t, err)
	assert.Len(t, final.Entries, 1)
	assert.Nil(t, final.Paging.NextCursor)
	assert.Equal(t, []int{0, 2}, offsets)
}

func TestResourceDataCursorPreservesNeedTotalAcrossContinuation(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1"}
	params := &interfaces.ResourceDataQueryParams{
		NeedTotal: true,
		Paging:    interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
	}
	executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
		if pageParams.Offset == 0 {
			assert.True(t, pageParams.NeedTotal)
			return []map[string]any{{"id": 1}, {"id": 2}}, 2, nil
		}
		assert.False(t, pageParams.NeedTotal)
		return []map[string]any{{"id": 2}}, 99, nil
	}

	first, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource, params, executor)
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	assert.True(t, first.NeedTotal)

	continuation, err := ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *first.Paging.NextCursor, executor)
	require.NoError(t, err)
	assert.True(t, continuation.NeedTotal)
	assert.Equal(t, int64(2), continuation.TotalCount)
}

func TestResourceDataCursorRejectsWrongResource(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1"}
	result, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
		&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}},
		func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			return []map[string]any{{"id": 1}, {"id": 2}}, 2, nil
		})
	require.NoError(t, err)
	require.NotNil(t, result.Paging.NextCursor)

	_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", &interfaces.Resource{ID: "table-2"}, *result.Paging.NextCursor,
		func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			return nil, 0, nil
		})
	assertHTTPError(t, err, 404)
}

func TestResourceDataCursorPreservesOpenSearchSearchAfter(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "index-1", CatalogID: "catalog-1"}
	var continuationSearchAfter []any
	executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
		if pageParams.Offset == 0 {
			pageParams.SearchAfter = []any{"sort-2"}
			return []map[string]any{{"id": 1}, {"id": 2}}, 2, nil
		}
		continuationSearchAfter = append([]any(nil), pageParams.SearchAfter...)
		return []map[string]any{}, 2, nil
	}

	first, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
		&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}}, executor)
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *first.Paging.NextCursor, executor)
	require.NoError(t, err)
	assert.Equal(t, []any{"sort-2"}, continuationSearchAfter)
}

func TestResourceDataIndexCursorUsesLastReturnedHitForSearchAfter(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "index-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryIndex}
	pages := []struct {
		entries     []map[string]any
		searchAfter []any
	}{
		{entries: []map[string]any{{"id": 1}}, searchAfter: []any{"sort-1"}},
		{entries: []map[string]any{{"id": 2}}, searchAfter: []any{"sort-2"}},
		{entries: []map[string]any{{"id": 3}}, searchAfter: []any{"sort-3"}},
	}
	pageIndex := 0
	executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
		require.Equal(t, 1, pageParams.Limit)
		if pageIndex > 0 {
			require.Equal(t, pages[pageIndex-1].searchAfter, pageParams.SearchAfter)
		}
		page := pages[pageIndex]
		pageIndex++
		pageParams.SearchAfter = append([]any(nil), page.searchAfter...)
		return page.entries, int64(len(pages)), nil
	}

	result, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
		&interfaces.ResourceDataQueryParams{NeedTotal: true, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}}, executor)
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"id": 1}}, result.Entries)
	for expectedID := 2; result.Paging.NextCursor != nil; expectedID++ {
		result, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *result.Paging.NextCursor, executor)
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": expectedID}}, result.Entries)
	}
	assert.Equal(t, 3, pageIndex)
}

func TestResourceDataCursorRejectsUpdatedResource(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "view-1", CatalogID: "catalog-1", UpdateTime: 100}
	result, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
		&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}},
		func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			return []map[string]any{{"id": 1}, {"id": 2}}, 2, nil
		})
	require.NoError(t, err)
	require.NotNil(t, result.Paging.NextCursor)

	updatedResource := *resource
	updatedResource.UpdateTime++
	executed := false
	_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", &updatedResource, *result.Paging.NextCursor,
		func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			executed = true
			return nil, 0, nil
		})
	assertHTTPError(t, err, http.StatusNotFound)
	assert.False(t, executed)

	_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *result.Paging.NextCursor,
		func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			return nil, 0, nil
		})
	assertHTTPError(t, err, http.StatusNotFound)
}

func TestResourceDataCursorUsesPhysicalPaginationCategory(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "view-1", Category: interfaces.ResourceCategoryLogicView, CatalogID: "catalog-1"}
	result, err := ExecuteInitialResourceDataCursorWithCategory(context.Background(), "account-1", resource,
		interfaces.ResourceCategoryIndex,
		&interfaces.ResourceDataQueryParams{NeedTotal: true, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}},
		func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			assert.Equal(t, 1, pageParams.Limit)
			assert.True(t, pageParams.NeedTotal)
			pageParams.SearchAfter = []any{"sort-1"}
			return []map[string]any{{"id": 1}}, 2, nil
		})
	require.NoError(t, err)
	require.NotNil(t, result.Paging.NextCursor)
}

func TestResourceDataCursorInitialPageIsNotReclaimedWhileExecuting(t *testing.T) {
	previousManager := rawQueryCursorSessions
	manager := newCursorSessionManager(10)
	rawQueryCursorSessions = manager
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	started := make(chan struct{})
	finish := make(chan struct{})
	resultCh := make(chan *interfaces.ResourceDataQueryResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1",
			&interfaces.Resource{ID: "table-1", CatalogID: "catalog-1"},
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}},
			func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
				close(started)
				<-finish
				return []map[string]any{{"id": 1}, {"id": 2}}, 2, nil
			})
		resultCh <- result
		errCh <- err
	}()
	<-started

	manager.mu.Lock()
	var session *interfaces.CursorSession
	for _, candidate := range manager.sessions {
		session = candidate
	}
	manager.mu.Unlock()
	require.NotNil(t, session)
	atomic.StoreInt64(&session.ExpiresAtSec, time.Now().Add(-time.Second).Unix())
	manager.mu.Lock()
	manager.removeExpiredLocked(time.Now().Unix())
	_, ok := manager.sessions[session.ID]
	manager.mu.Unlock()
	assert.True(t, ok)

	close(finish)
	require.NoError(t, <-errCh)
	result := <-resultCh
	require.NotNil(t, result.Paging.NextCursor)
	assert.Equal(t, session.ID, *result.Paging.NextCursor)
}
