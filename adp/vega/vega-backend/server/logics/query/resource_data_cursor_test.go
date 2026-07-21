package query

import (
	"context"
	"testing"

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
		Mode: interfaces.PagingModeCursor,
		Size: 2,
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

	final, err := ExecuteResourceDataCursorContinuation(context.Background(), "account-1", resource.ID, *first.Paging.NextCursor, executor)
	require.NoError(t, err)
	assert.Len(t, final.Entries, 1)
	assert.Nil(t, final.Paging.NextCursor)
	assert.Equal(t, []int{0, 2}, offsets)
}

func TestResourceDataCursorRejectsWrongResource(t *testing.T) {
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10)
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	resource := &interfaces.Resource{ID: "table-1", CatalogID: "catalog-1"}
	result, err := ExecuteInitialResourceDataCursor(context.Background(), "account-1", resource,
		&interfaces.ResourceDataQueryParams{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Size: 1}},
		func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			return []map[string]any{{"id": 1}, {"id": 2}}, 2, nil
		})
	require.NoError(t, err)
	require.NotNil(t, result.Paging.NextCursor)

	_, err = ExecuteResourceDataCursorContinuation(context.Background(), "account-1", "table-2", *result.Paging.NextCursor,
		func(_ context.Context, _ *interfaces.ResourceDataQueryParams) ([]map[string]any, int64, error) {
			return nil, 0, nil
		})
	assertHTTPError(t, err, 404)
}
