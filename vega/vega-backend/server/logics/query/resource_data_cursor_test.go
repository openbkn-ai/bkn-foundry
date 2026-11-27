// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.

package query

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
)

func TestOpenResourceTableCursor(t *testing.T) {
	t.Run("reads the count before returning the stream for the first page", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		reader := vmock.NewMockTableReadSessionConnector(ctrl)
		session := &interfaces.CursorSession{PageLimit: 1}
		resource := &interfaces.Resource{ID: "table-1"}
		params := &interfaces.ResourceDataQueryParams{NeedTotal: true}
		readParams := *params
		readParams.Timeout = time.Minute
		total := int64(2)
		stream := vmock.NewMockQueryPageSession(ctrl)
		gomock.InOrder(
			reader.EXPECT().OpenTableRead(gomock.Any(), resource, &readParams).Return(stream, nil),
			stream.EXPECT().ReadTotal(gomock.Any()).Return(&total, nil),
			stream.EXPECT().ReadPage(gomock.Any(), 1).Return(&interfaces.QueryPage{TotalCount: &total,
				Entries: []map[string]any{{"id": int64(1)}}, HasMore: true}, nil),
			stream.EXPECT().Close().Return(nil),
			connector.EXPECT().Close(gomock.Any()).Return(nil),
		)
		require.NoError(t, OpenResourceTableCursor(t.Context(), session, connector, reader, resource, params))
		defer closeSQLCursorResources(session)
		assert.Zero(t, params.Timeout)
		page, err := ReadResourceCursorPage(t.Context(), session)
		require.NoError(t, err)
		assert.Equal(t, &total, page.TotalCount)
		assert.Equal(t, []map[string]any{{"id": int64(1)}}, page.Entries)
		assert.True(t, session.ReadHasMore)
	})

	t.Run("a count failure reopens the stream without counting", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		reader := vmock.NewMockTableReadSessionConnector(ctrl)
		session := &interfaces.CursorSession{PageLimit: 1}
		resource := &interfaces.Resource{ID: "table-1"}
		params := &interfaces.ResourceDataQueryParams{NeedTotal: true, Timeout: 5 * time.Second}
		ctx := t.Context()
		cause := errors.New("source count failed")
		stream := vmock.NewMockQueryPageSession(ctrl)
		rows := vmock.NewMockQueryPageSession(ctrl)
		readParams := *params
		readParams.NeedTotal = false
		gomock.InOrder(
			reader.EXPECT().OpenTableRead(ctx, resource, params).Return(stream, nil),
			stream.EXPECT().ReadTotal(ctx).Return(nil, cause),
			stream.EXPECT().Close().Return(nil),
			reader.EXPECT().OpenTableRead(ctx, resource, &readParams).Return(rows, nil),
			rows.EXPECT().ReadPage(ctx, 1).Return(&interfaces.QueryPage{Entries: []map[string]any{{"id": 1}}}, nil),
			rows.EXPECT().Close().Return(nil),
			connector.EXPECT().Close(gomock.Any()).Return(nil),
		)
		require.NoError(t, OpenResourceTableCursor(ctx, session, connector, reader, resource, params))
		defer closeSQLCursorResources(session)
		page, err := ReadResourceCursorPage(ctx, session)
		require.NoError(t, err)
		assert.Nil(t, page.TotalCount)
		assert.Equal(t, []map[string]any{{"id": 1}}, page.Entries)
	})
}

func TestExecuteResourceDataCursorContinuation(t *testing.T) {
	t.Run("busy cursor can be retried after a failure before source consumption", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		resource := &interfaces.Resource{ID: "api-1", Category: interfaces.ResourceCategoryAPI}
		params := &interfaces.ResourceDataQueryParams{
			Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
		}
		execute := func(_ context.Context, page *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			return &interfaces.ResourceDataQueryResult{
				Entries:    []map[string]any{{"id": page.Paging.Offset + 1}},
				TotalCount: new(int64(2)),
			}, nil
		}
		first, err := ExecuteInitialResourceDataCursor(ctx, "account-1", resource, params, execute)
		require.NoError(t, err)
		require.NotNil(t, first.Paging.NextCursor)
		cursor := *first.Paging.NextCursor

		entered := make(chan struct{})
		release := make(chan struct{})
		finished := make(chan error, 1)
		readErr := errors.New("source read did not start")
		go func() {
			_, err := ExecuteResourceDataCursorContinuation(ctx, "account-1", resource, cursor,
				func(context.Context, *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					close(entered)
					select {
					case <-release:
						return nil, readErr
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				})
			finished <- err
		}()
		select {
		case <-entered:
		case err := <-finished:
			t.Fatalf("continuation returned early: %v", err)
		case <-ctx.Done():
			t.Fatalf("continuation did not start: %v", <-finished)
		}

		_, busyErr := ExecuteResourceDataCursorContinuation(ctx, "account-1", resource, cursor, execute)
		_, wrongAccountErr := ExecuteResourceDataCursorContinuation(ctx, "account-2", resource, cursor, execute)
		otherResource := &interfaces.Resource{ID: "api-2", Category: interfaces.ResourceCategoryAPI}
		_, wrongResourceErr := ExecuteResourceDataCursorContinuation(ctx, "account-1", otherResource, cursor, execute)
		close(release)
		require.ErrorIs(t, <-finished, readErr)

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, busyErr, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_CursorInUse, httpErr.ErrorCode)
		assertHTTPError(t, wrongAccountErr, http.StatusNotFound)
		assertHTTPError(t, wrongResourceErr, http.StatusNotFound)

		last, err := ExecuteResourceDataCursorContinuation(ctx, "account-1", resource, cursor, execute)
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 2}}, last.Entries)
		assert.Nil(t, last.Paging.NextCursor)
	})

	t.Run("API cursor uses the exact requested page size and freezes total", func(t *testing.T) {
		for _, test := range []struct {
			name                string
			needTotal, hasTotal bool
		}{
			{name: "total omitted from the request"},
			{name: "requested total unavailable", needTotal: true},
			{name: "requested total available", needTotal: true, hasTotal: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				previousManager := rawQueryCursorSessions
				rawQueryCursorSessions = newCursorSessionManager(10, 9)
				t.Cleanup(func() { rawQueryCursorSessions = previousManager })

				resource := &interfaces.Resource{ID: "api-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryAPI}
				params := &interfaces.ResourceDataQueryParams{
					Paging:    interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2},
					NeedTotal: test.needTotal, OutputFields: []string{"id"},
					FilterCondition: map[string]any{"field": "id"},
					Sort:            []*interfaces.SortField{{Field: "id", Direction: interfaces.ASC_DIRECTION}},
				}
				var offsets []int
				execute := func(_ context.Context, page *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					offsets = append(offsets, page.Paging.Offset)
					assert.Equal(t, interfaces.PagingModeSingle, page.Paging.Mode)
					assert.Equal(t, 2, page.Paging.Limit)
					assert.Equal(t, params.OutputFields, page.OutputFields)
					assert.Equal(t, params.FilterCondition, page.FilterCondition)
					assert.Equal(t, test.needTotal && (!test.hasTotal || page.Paging.Offset == 0), page.NeedTotal)
					result := &interfaces.ResourceDataQueryResult{IsSorted: true, Warnings: []string{"sort was ignored"}}
					switch page.Paging.Offset {
					case 0:
						if test.hasTotal {
							result.TotalCount = new(int64(4))
						}
						result.Entries = []map[string]any{{"id": 1}, {"id": 2}}
					case 2:
						result.Entries = []map[string]any{{"id": 3}, {"id": 4}}
					}
					return result, nil
				}
				first, err := ExecuteInitialResourceDataCursor(t.Context(), "account-1", resource, params, execute)
				require.NoError(t, err)
				require.NotNil(t, first.Paging.NextCursor)
				assert.False(t, first.IsSorted)
				assert.Equal(t, []string{"sort was ignored"}, first.Warnings)
				token := *first.Paging.NextCursor
				_, err = ExecuteResourceDataCursorContinuation(t.Context(), "wrong-account", resource, token, execute)
				assertHTTPError(t, err, http.StatusForbidden)
				assert.Equal(t, []int{0}, offsets)

				readErr := errors.New("source failed before reading a page")
				_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token,
					func(_ context.Context, page *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
						assert.Equal(t, 2, page.Paging.Offset)
						return nil, readErr
					})
				require.ErrorIs(t, err, readErr)

				second, err := ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token, execute)
				require.NoError(t, err)
				assert.Equal(t, []map[string]any{{"id": 3}, {"id": 4}}, second.Entries)
				assert.False(t, second.IsSorted)
				if test.hasTotal {
					assert.Equal(t, new(int64(4)), second.TotalCount)
					assert.Nil(t, second.Paging.NextCursor)
					assert.Equal(t, []int{0, 2}, offsets)
				} else {
					assert.Nil(t, first.TotalCount)
					assert.Nil(t, second.TotalCount)
					require.NotNil(t, second.Paging.NextCursor)
					// A caller that fails after reading the source page still holds the old token.
					_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token, execute)
					assertHTTPError(t, err, http.StatusNotFound)
					require.Equal(t, []int{0, 2}, offsets)
					assert.NotEqual(t, token, *second.Paging.NextCursor)
					assertHTTPError(t, CloseCursorSession(t.Context(), "account-1", token), http.StatusNotFound)

					final, err := ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, *second.Paging.NextCursor, execute)
					require.NoError(t, err)
					assert.Empty(t, final.Entries)
					assert.False(t, final.IsSorted)
					assert.Nil(t, final.Paging.NextCursor)
					assert.Equal(t, []int{0, 2, 4}, offsets)
				}
				_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token, execute)
				assertHTTPError(t, err, http.StatusNotFound)
			})
		}
	})

	t.Run("API cursor continues nonempty short pages when total is known", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			mode      string
			needTotal bool
			total     int64
			offsets   []int
			rowIDs    []int
		}{
			{name: "offset requested total", mode: "offset", needTotal: true, total: 5, offsets: []int{2, 3, 4}, rowIDs: []int{3, 4, 5}},
			{name: "offset reported total", mode: "offset", total: 5, offsets: []int{2, 3, 4}, rowIDs: []int{3, 4, 5}},
			{name: "page requested total from nonzero offset", mode: "page", needTotal: true, total: 4, offsets: []int{2, 4, 6, 8}, rowIDs: []int{2, 3, 4}},
			{name: "page reported total from nonzero offset", mode: "page", total: 4, offsets: []int{2, 4, 6, 8}, rowIDs: []int{2, 3, 4}},
			{name: "page reported total from zero offset", mode: "page", total: 3, offsets: []int{0, 2, 4}, rowIDs: []int{1, 2, 3}},
		} {
			t.Run(test.name, func(t *testing.T) {
				previousManager := rawQueryCursorSessions
				rawQueryCursorSessions = newCursorSessionManager(10, 9)
				t.Cleanup(func() { rawQueryCursorSessions = previousManager })

				zero := 0
				resource := &interfaces.Resource{
					ID: "api-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryAPI,
					APIDefinition: &interfaces.APIDefinition{PagingRule: &interfaces.APIPagingRule{Mode: test.mode, PageBase: &zero}},
				}
				params := &interfaces.ResourceDataQueryParams{
					Paging:    interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: test.offsets[0], Limit: 2},
					NeedTotal: test.needTotal,
				}
				calls := 0
				execute := func(_ context.Context, page *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					require.Less(t, calls, len(test.offsets))
					assert.Equal(t, interfaces.PagingModeSingle, page.Paging.Mode)
					assert.Equal(t, 2, page.Paging.Limit)
					assert.Equal(t, test.offsets[calls], page.Paging.Offset)
					assert.Equal(t, test.needTotal && calls == 0, page.NeedTotal)
					result := &interfaces.ResourceDataQueryResult{}
					if calls < len(test.rowIDs) {
						result.Entries = []map[string]any{{"id": test.rowIDs[calls]}}
					}
					if calls == 0 {
						result.TotalCount = new(test.total)
					}
					calls++
					return result, nil
				}

				result, err := ExecuteInitialResourceDataCursor(t.Context(), "account-1", resource, params, execute)
				for i := range test.offsets {
					require.NoError(t, err)
					if i < len(test.rowIDs) {
						assert.Equal(t, []map[string]any{{"id": test.rowIDs[i]}}, result.Entries)
					} else {
						assert.Empty(t, result.Entries)
					}
					if test.needTotal {
						assert.Equal(t, new(test.total), result.TotalCount)
					} else {
						assert.Nil(t, result.TotalCount)
					}
					if i == len(test.offsets)-1 {
						assert.Nil(t, result.Paging.NextCursor)
						break
					}
					require.NotNil(t, result.Paging.NextCursor, "missing continuation after source offset %d", test.offsets[i])
					result, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, *result.Paging.NextCursor, execute)
				}
				assert.Equal(t, len(test.offsets), calls)
			})
		}
	})

	t.Run("API cursor closes when an empty page contradicts known total", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mode   string
			offset int
		}{
			{name: "offset", mode: "offset"},
			{name: "page from zero offset", mode: "page"},
			{name: "page from nonzero offset", mode: "page", offset: 2},
		} {
			t.Run(test.name, func(t *testing.T) {
				previousManager := rawQueryCursorSessions
				rawQueryCursorSessions = newCursorSessionManager(10, 9)
				t.Cleanup(func() { rawQueryCursorSessions = previousManager })

				resource := &interfaces.Resource{
					ID: "api-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryAPI,
					APIDefinition: &interfaces.APIDefinition{PagingRule: &interfaces.APIPagingRule{Mode: test.mode}},
				}
				params := &interfaces.ResourceDataQueryParams{
					Paging:    interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: test.offset, Limit: 2},
					NeedTotal: true,
				}
				calls := 0
				execute := func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					calls++
					if calls == 1 {
						return &interfaces.ResourceDataQueryResult{
							Entries: []map[string]any{{"id": 1}, {"id": 2}}, TotalCount: new(int64(test.offset + 3)),
						}, nil
					}
					return &interfaces.ResourceDataQueryResult{}, nil
				}
				first, err := ExecuteInitialResourceDataCursor(t.Context(), "account-1", resource, params, execute)
				require.NoError(t, err)
				require.NotNil(t, first.Paging.NextCursor)
				token := *first.Paging.NextCursor
				_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token, execute)
				assertHTTPError(t, err, http.StatusBadGateway)
				_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token, execute)
				assertHTTPError(t, err, http.StatusNotFound)
				assert.Equal(t, 2, calls)
			})
		}
	})

	t.Run("API cursor ends when the next offset cannot be represented", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "api-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryAPI}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{
			Mode: interfaces.PagingModeCursor, Offset: math.MaxInt, Limit: 1,
		}}
		result, err := ExecuteInitialResourceDataCursor(t.Context(), "account-1", resource, params,
			func(_ context.Context, page *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				assert.Equal(t, math.MaxInt, page.Paging.Offset)
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}}}, nil
			})
		require.NoError(t, err)
		require.Len(t, result.Entries, 1)
		assert.Nil(t, result.Paging.NextCursor)
	})

	t.Run("API page cursor fails when the known total requires an unrepresentable offset", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{
			ID: "api-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryAPI,
			APIDefinition: &interfaces.APIDefinition{PagingRule: &interfaces.APIPagingRule{Mode: "page"}},
		}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{
			Mode: interfaces.PagingModeCursor, Offset: math.MaxInt - 3, Limit: 2,
		}}
		calls := 0
		execute := func(_ context.Context, page *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			require.Less(t, calls, 2)
			assert.Equal(t, math.MaxInt-3+calls*2, page.Paging.Offset)
			calls++
			return &interfaces.ResourceDataQueryResult{
				Entries: []map[string]any{{"id": calls}}, TotalCount: new(int64(math.MaxInt)),
			}, nil
		}
		first, err := ExecuteInitialResourceDataCursor(t.Context(), "account-1", resource, params, execute)
		require.NoError(t, err)
		require.NotNil(t, first.Paging.NextCursor)
		token := *first.Paging.NextCursor
		_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token, execute)
		assertHTTPError(t, err, http.StatusBadGateway)
		_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, token, execute)
		assertHTTPError(t, err, http.StatusNotFound)
		assert.Equal(t, 2, calls)
	})

	t.Run("API cursor rejects expired and changed resources before requesting another page", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		manager := newCursorSessionManager(10, 9)
		rawQueryCursorSessions = manager
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "api-1", UpdateTime: 1, Category: interfaces.ResourceCategoryAPI}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}}
		calls := 0
		execute := func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			calls++
			return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}}}, nil
		}
		first, err := ExecuteInitialResourceDataCursor(t.Context(), "account-1", resource, params, execute)
		require.NoError(t, err)
		require.NotNil(t, first.Paging.NextCursor)
		changed := *resource
		changed.UpdateTime++
		_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", &changed, *first.Paging.NextCursor, execute)
		assertHTTPError(t, err, http.StatusNotFound)

		second, err := ExecuteInitialResourceDataCursor(t.Context(), "account-1", resource, params, execute)
		require.NoError(t, err)
		require.NotNil(t, second.Paging.NextCursor)
		session, ok := manager.acquire(*second.Paging.NextCursor)
		require.True(t, ok)
		atomic.StoreInt64(&session.ExpiresAtSec, 0)
		manager.release(session)
		_, err = ExecuteResourceDataCursorContinuation(t.Context(), "account-1", resource, *second.Paging.NextCursor, execute)
		assertHTTPError(t, err, http.StatusNotFound)
		assert.Equal(t, 2, calls)
	})

	t.Run("pages and closes", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable}
		params := &interfaces.ResourceDataQueryParams{
			Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2},
			Sort:   []*interfaces.SortField{{Field: "id", Direction: interfaces.ASC_DIRECTION}},
		}
		var offsets []int
		executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			offsets = append(offsets, pageParams.Paging.Offset)
			assert.Equal(t, params.Sort, pageParams.Sort)
			pageParams.Sort = nil
			if pageParams.Paging.Offset == 0 {
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}, TotalCount: new(int64(3)), IsApproximate: true}, nil
			}
			return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 3}}, TotalCount: new(int64(3))}, nil
		}

		first, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource, params, executor)
		require.NoError(t, err)
		require.NotNil(t, first.Paging.NextCursor)
		assert.Len(t, first.Entries, 2)
		assert.True(t, first.IsApproximate)
		assert.True(t, first.IsSorted)

		final, err := ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *first.Paging.NextCursor, executor)
		require.NoError(t, err)
		assert.Len(t, final.Entries, 1)
		assert.False(t, final.IsApproximate)
		assert.True(t, final.IsSorted)
		assert.Nil(t, final.Paging.NextCursor)
		assert.Equal(t, []int{0, 2}, offsets)
	})

	t.Run("preserves need total", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1", Category: interfaces.ResourceCategoryTable}
		params := &interfaces.ResourceDataQueryParams{
			NeedTotal: true,
			Paging:    interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1},
		}
		executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			if pageParams.Paging.Offset == 0 {
				assert.True(t, pageParams.NeedTotal)
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}}, TotalCount: new(int64(2)), IsSorted: true}, nil
			}
			assert.False(t, pageParams.NeedTotal)
			return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 2}}, TotalCount: new(int64(99)), IsSorted: true}, nil
		}

		first, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource, params, executor)
		require.NoError(t, err)
		require.NotNil(t, first.Paging.NextCursor)
		assert.Equal(t, new(int64(2)), first.TotalCount)
		assert.False(t, first.IsSorted)

		continuation, err := ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *first.Paging.NextCursor, executor)
		require.NoError(t, err)
		assert.False(t, continuation.IsSorted)
		assert.Equal(t, new(int64(2)), continuation.TotalCount)
	})

	t.Run("rejects wrong resource", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1"}
		result, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}},
			func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}}, TotalCount: new(int64(2))}, nil
			})
		require.NoError(t, err)
		require.NotNil(t, result.Paging.NextCursor)

		_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", &interfaces.Resource{ID: "table-2"}, *result.Paging.NextCursor,
			func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				return &interfaces.ResourceDataQueryResult{Entries: nil, TotalCount: new(int64(0))}, nil
			})
		assertHTTPError(t, err, 404)
	})

	t.Run("preserves opensearch search after", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "index-1", CatalogID: "catalog-1"}
		var continuationSearchAfter []any
		executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			var resultSearchAfter []any

			if pageParams.Paging.Offset == 0 {
				resultSearchAfter = []any{"sort-2"}
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}}, TotalCount: new(int64(2)), SearchAfter: resultSearchAfter}, nil
			}
			continuationSearchAfter = append([]any(nil), pageParams.SearchAfter...)
			return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{}, TotalCount: new(int64(2)), SearchAfter: resultSearchAfter}, nil
		}

		first, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}}, executor)
		require.NoError(t, err)
		require.NotNil(t, first.Paging.NextCursor)
		_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *first.Paging.NextCursor, executor)
		require.NoError(t, err)
		assert.Equal(t, []any{"sort-2"}, continuationSearchAfter)
	})

	t.Run("uses last returned hit for search after", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
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
		executor := func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			var resultSearchAfter2 []any

			require.Equal(t, 1, pageParams.Paging.Limit)
			if pageIndex > 0 {
				require.Equal(t, pages[pageIndex-1].searchAfter, pageParams.SearchAfter)
			}
			page := pages[pageIndex]
			pageIndex++
			resultSearchAfter2 = append([]any(nil), page.searchAfter...)
			return &interfaces.ResourceDataQueryResult{Entries: page.entries, TotalCount: new(int64(len(pages))), SearchAfter: resultSearchAfter2}, nil
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
	})

	t.Run("rejects updated resource", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "view-1", CatalogID: "catalog-1", UpdateTime: 100}
		result, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
			&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}},
			func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}}, TotalCount: new(int64(2))}, nil
			})
		require.NoError(t, err)
		require.NotNil(t, result.Paging.NextCursor)

		updatedResource := *resource
		updatedResource.UpdateTime++
		executed := false
		_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", &updatedResource, *result.Paging.NextCursor,
			func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				executed = true
				return &interfaces.ResourceDataQueryResult{Entries: nil, TotalCount: new(int64(0))}, nil
			})
		assertHTTPError(t, err, http.StatusNotFound)
		assert.False(t, executed)

		_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource, *result.Paging.NextCursor,
			func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				return &interfaces.ResourceDataQueryResult{Entries: nil, TotalCount: new(int64(0))}, nil
			})
		assertHTTPError(t, err, http.StatusNotFound)
	})
}

func TestExecuteInitialResourceDataCursorWithCategory(t *testing.T) {
	t.Run("uses physical pagination category", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		resource := &interfaces.Resource{ID: "view-1", Category: interfaces.ResourceCategoryLogicalView, CatalogID: "catalog-1"}
		result, err := ExecuteInitialResourceDataCursorWithCategory(context.Background(), "account-1", resource,
			interfaces.ResourceCategoryIndex,
			&interfaces.ResourceDataQueryParams{NeedTotal: true, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}},
			func(_ context.Context, pageParams *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				var resultSearchAfter3 []any

				assert.Equal(t, 1, pageParams.Paging.Limit)
				assert.True(t, pageParams.NeedTotal)
				resultSearchAfter3 = []any{"sort-1"}
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}}, TotalCount: new(int64(2)), SearchAfter: resultSearchAfter3}, nil
			})
		require.NoError(t, err)
		require.NotNil(t, result.Paging.NextCursor)
	})
}

func TestExecuteResourceDataCursorContinuationWithRunnerRejectsChangedSource(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*interfaces.Resource)
	}{
		{name: "source revision", change: func(source *interfaces.Resource) { source.UpdateTime++ }},
		{name: "local index availability", change: func(source *interfaces.Resource) {
			source.LocalIndexStatus = interfaces.ResourceLocalIndexStatusUnavailable
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousManager := rawQueryCursorSessions
			rawQueryCursorSessions = newCursorSessionManager(10, 9)
			t.Cleanup(func() { rawQueryCursorSessions = previousManager })

			view := &interfaces.Resource{ID: "view-1", Category: interfaces.ResourceCategoryLogicalView, UpdateTime: 10}
			source := &interfaces.Resource{ID: "source-1", Category: interfaces.ResourceCategoryTable,
				UpdateTime: 20, LocalIndexName: "source-index", LocalIndexStatus: interfaces.ResourceLocalIndexStatusAvailable}
			calls := 0
			executor := ResourceDataPageExecutor(func(_ context.Context, params *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
				var resultSearchAfter4 []any

				calls++
				resultSearchAfter4 = []any{"next"}
				return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": calls}}, TotalCount: new(int64(2)), SearchAfter: resultSearchAfter4}, nil
			})
			first, err := ExecuteInitialResourceDataCursorWithCategoryRunner(context.Background(), "account-1", view,
				source, interfaces.ResourceCategoryIndex,
				&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}}, executor)
			require.NoError(t, err)
			require.NotNil(t, first.Paging.NextCursor)
			second, err := ExecuteResourceDataCursorContinuationWithRunner(context.Background(), "account-1", view,
				source, *first.Paging.NextCursor, executor)
			require.NoError(t, err)
			require.NotNil(t, second.Paging.NextCursor)

			changed := *source
			tc.change(&changed)
			_, err = ExecuteResourceDataCursorContinuationWithRunner(context.Background(), "account-1", view,
				&changed, *second.Paging.NextCursor, executor)
			assertHTTPError(t, err, http.StatusNotFound)
			assert.Equal(t, 2, calls)
		})
	}
}

func TestExecuteInitialResourceDataCursor(t *testing.T) {
	t.Run("accounts quota to trusted caller and binds cursor to access account", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		manager := newCursorSessionManager(4, 1)
		rawQueryCursorSessions = manager
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })
		resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1"}
		params := &interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}}
		execute := func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
			return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}}, TotalCount: new(int64(2))}, nil
		}
		first, err := ExecuteInitialResourceDataCursor(interfaces.WithCursorQuotaOwner(context.Background(), "caller-a"), "admin", resource, params, execute)
		require.NoError(t, err)
		require.NotNil(t, first.Paging.NextCursor)
		_, err = ExecuteInitialResourceDataCursor(interfaces.WithCursorQuotaOwner(context.Background(), "caller-a"), "admin", resource, params, execute)
		assertHTTPError(t, err, http.StatusTooManyRequests)
		second, err := ExecuteInitialResourceDataCursor(interfaces.WithCursorQuotaOwner(context.Background(), "caller-b"), "admin", resource, params, execute)
		require.NoError(t, err)
		require.NotNil(t, second.Paging.NextCursor)
		_, err = ExecuteResourceDataCursorContinuation(context.Background(), "caller-a", resource, *first.Paging.NextCursor, execute)
		assertHTTPError(t, err, http.StatusForbidden)
	})

	t.Run("active initial page is not reclaimed", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		manager := newCursorSessionManager(10, 9)
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
				func(_ context.Context, _ *interfaces.ResourceDataQueryParams) (*interfaces.ResourceDataQueryResult, error) {
					close(started)
					<-finish
					return &interfaces.ResourceDataQueryResult{Entries: []map[string]any{{"id": 1}, {"id": 2}}, TotalCount: new(int64(2))}, nil
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
		manager.closeExpiredSessions(time.Now().Unix())
		manager.mu.Lock()
		_, ok := manager.sessions[session.ID]
		manager.mu.Unlock()
		assert.True(t, ok)

		close(finish)
		require.NoError(t, <-errCh)
		result := <-resultCh
		require.NotNil(t, result.Paging.NextCursor)
		assert.Equal(t, session.ID, *result.Paging.NextCursor)
	})
}
