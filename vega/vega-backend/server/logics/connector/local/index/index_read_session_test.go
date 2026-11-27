// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package index

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
)

type indexSessionSnapshotStub struct {
	searchPage func(context.Context, int, []any, bool) (*interfaces.QueryPage, []any, error)
	closeErr   error
	closeCalls int
}

func (s *indexSessionSnapshotStub) SearchPage(ctx context.Context, limit int, after []any, trackTotal bool) (*interfaces.QueryPage, []any, error) {
	return s.searchPage(ctx, limit, after, trackTotal)
}

func (s *indexSessionSnapshotStub) Close() error {
	s.closeCalls++
	return s.closeErr
}

func TestIndexReadSessionReadTotal(t *testing.T) {
	ctx := t.Context()
	searchErr := errors.New("snapshot count failed")
	var total int64
	var searches int
	snapshot := &indexSessionSnapshotStub{searchPage: func(_ context.Context, limit int, after []any, trackTotal bool) (*interfaces.QueryPage, []any, error) {
		searches++
		assert.Zero(t, limit)
		assert.Empty(t, after)
		assert.True(t, trackTotal)
		if searches == 1 {
			return nil, nil, searchErr
		}
		return &interfaces.QueryPage{TotalCount: &total}, nil, nil
	}}
	session := NewIndexReadSession(snapshot, 0, true, true)
	t.Cleanup(func() { assert.NoError(t, session.Close()) })

	count, err := session.ReadTotal(ctx)
	require.ErrorIs(t, err, searchErr)
	assert.Nil(t, count)
	count, err = session.ReadTotal(ctx)
	require.NoError(t, err)
	require.NotNil(t, count)
	assert.Zero(t, *count)
	cached, err := session.ReadTotal(ctx)
	require.NoError(t, err)
	assert.Equal(t, count, cached)
	assert.Equal(t, 2, searches)
}

func TestIndexReadSessionReadPage(t *testing.T) {
	t.Run("preserves paging state when a page is missing its continuation", func(t *testing.T) {
		ctx := t.Context()
		total := int64(6)
		next := []any{json.Number("9007199254740993")}
		calls := 0
		snapshot := &indexSessionSnapshotStub{searchPage: func(gotCtx context.Context, limit int, after []any, trackTotal bool) (*interfaces.QueryPage, []any, error) {
			assert.Equal(t, ctx, gotCtx)
			assert.Equal(t, 1, limit)
			calls++
			if calls <= 2 {
				assert.Empty(t, after)
				assert.True(t, trackTotal)
				page := &interfaces.QueryPage{Entries: []map[string]any{{"id": 5}}, TotalCount: &total}
				if calls == 1 {
					return page, nil, nil
				}
				return page, next, nil
			}
			assert.Equal(t, next, after)
			assert.False(t, trackTotal)
			return &interfaces.QueryPage{Entries: []map[string]any{{"id": 6}}}, nil, nil
		}}
		session := NewIndexReadSession(snapshot, 4, true, true)

		page, err := session.ReadPage(ctx, 0)
		require.ErrorContains(t, err, "invalid index cursor page limit")
		assert.Nil(t, page)
		page, err = session.ReadPage(ctx, 1)
		require.ErrorContains(t, err, "index cursor page is missing its sort values")
		var invalid *interfaces.ConditionBuildError
		require.ErrorAs(t, err, &invalid)
		assert.Contains(t, invalid.Reason, "use a unique field as the last sort key and restart the query")
		assert.Nil(t, page)

		first, err := session.ReadPage(ctx, 1)
		require.NoError(t, err)
		assert.True(t, first.HasMore)
		require.NotNil(t, first.TotalCount)
		assert.Equal(t, total, *first.TotalCount)
		count, err := session.ReadTotal(ctx)
		require.NoError(t, err)
		require.NotNil(t, count)
		assert.Equal(t, total, *count)
		assert.Equal(t, 2, calls)
		last, err := session.ReadPage(ctx, 1)
		require.NoError(t, err)
		assert.False(t, last.HasMore)
		assert.Equal(t, []map[string]any{{"id": 6}}, last.Entries)
		assert.Equal(t, first.TotalCount, last.TotalCount)
		empty, err := session.ReadPage(ctx, 1)
		require.NoError(t, err)
		assert.Empty(t, empty.Entries)
		assert.Equal(t, 3, calls)
		require.NoError(t, session.Close())
		_, err = session.ReadTotal(ctx)
		require.ErrorIs(t, err, interfaces.ErrQueryPageSessionExpired)
	})
	t.Run("omits total count when it was not requested", func(t *testing.T) {
		total := int64(1)
		snapshot := &indexSessionSnapshotStub{searchPage: func(_ context.Context, _ int, _ []any, trackTotal bool) (*interfaces.QueryPage, []any, error) {
			assert.True(t, trackTotal)
			return &interfaces.QueryPage{Entries: []map[string]any{{"id": 1}}, TotalCount: &total}, nil, nil
		}}
		session := NewIndexReadSession(snapshot, 0, false, true)
		count, err := session.ReadTotal(t.Context())
		require.NoError(t, err)
		assert.Nil(t, count)
		page, err := session.ReadPage(t.Context(), 2)
		require.NoError(t, err)
		assert.Nil(t, page.TotalCount)
		assert.False(t, page.HasMore)
		count, err = session.ReadTotal(t.Context())
		require.NoError(t, err)
		assert.Nil(t, count)
		last, err := session.ReadPage(t.Context(), 2)
		require.NoError(t, err)
		assert.Empty(t, last.Entries)
		assert.Nil(t, last.TotalCount)
		require.NoError(t, session.Close())
	})
	t.Run("checks continuation pages against the frozen total", func(t *testing.T) {
		total := int64(7)
		calls := 0
		snapshot := &indexSessionSnapshotStub{searchPage: func(_ context.Context, limit int, after []any, trackTotal bool) (*interfaces.QueryPage, []any, error) {
			assert.Equal(t, 2, limit)
			calls++
			switch calls {
			case 1:
				assert.True(t, trackTotal)
				assert.Empty(t, after)
				return &interfaces.QueryPage{Entries: []map[string]any{{"id": 3}, {"id": 4}}, TotalCount: &total}, []any{4}, nil
			case 2:
				assert.False(t, trackTotal)
				assert.Equal(t, []any{4}, after)
				return &interfaces.QueryPage{Entries: []map[string]any{{"id": 5}}}, []any{5}, nil
			case 3:
				assert.False(t, trackTotal)
				assert.Equal(t, []any{4}, after)
				return &interfaces.QueryPage{Entries: []map[string]any{{"id": 5}, {"id": 6}}}, []any{6}, nil
			default:
				assert.False(t, trackTotal)
				assert.Equal(t, []any{6}, after)
				return &interfaces.QueryPage{Entries: []map[string]any{{"id": 7}}}, nil, nil
			}
		}}
		session := NewIndexReadSession(snapshot, 2, true, true)
		first, err := session.ReadPage(t.Context(), 2)
		require.NoError(t, err)
		assert.True(t, first.HasMore)
		short, err := session.ReadPage(t.Context(), 2)
		require.ErrorContains(t, err, "index cursor page size does not match its exact total")
		assert.Nil(t, short)
		second, err := session.ReadPage(t.Context(), 2)
		require.NoError(t, err)
		assert.True(t, second.HasMore)
		last, err := session.ReadPage(t.Context(), 2)
		require.NoError(t, err)
		assert.False(t, last.HasMore)
		assert.Equal(t, &total, last.TotalCount)
		assert.Equal(t, []map[string]any{{"id": 7}}, last.Entries)
		require.NoError(t, session.Close())
	})
}

func TestIndexReadSessionClose(t *testing.T) {
	cause := errors.New("snapshot close failed")
	snapshot := &indexSessionSnapshotStub{closeErr: cause}
	session := NewIndexReadSession(snapshot, 0, false, false)
	require.ErrorIs(t, session.Close(), cause)
	require.NoError(t, session.Close())
	assert.Equal(t, 1, snapshot.closeCalls)
	page, err := session.ReadPage(t.Context(), 1)
	require.ErrorIs(t, err, interfaces.ErrQueryPageSessionExpired)
	assert.Nil(t, page)
}

func TestIndexReadSessionRejectsSkippedRowsWithoutRequestedTotal(t *testing.T) {
	total := int64(2)
	var calls int
	snapshot := &indexSessionSnapshotStub{searchPage: func(_ context.Context, limit int, after []any, trackTotal bool) (*interfaces.QueryPage, []any, error) {
		assert.Equal(t, 1, limit)
		calls++
		if calls == 1 {
			assert.Empty(t, after)
			page := &interfaces.QueryPage{Entries: []map[string]any{{"id": "a"}}}
			if trackTotal {
				page.TotalCount = &total
			}
			return page, []any{1}, nil
		}
		assert.Equal(t, []any{1}, after)
		assert.False(t, trackTotal)
		// Both documents have score 1. The search_after tuple skips the second one.
		return &interfaces.QueryPage{Entries: []map[string]any{}}, nil, nil
	}}
	session := NewIndexReadSession(snapshot, 0, false, true)
	t.Cleanup(func() { assert.NoError(t, session.Close()) })

	first, err := session.ReadPage(t.Context(), 1)
	require.NoError(t, err)
	assert.True(t, first.HasMore)
	assert.Nil(t, first.TotalCount)
	last, err := session.ReadPage(t.Context(), 1)
	require.ErrorContains(t, err, "use a unique field as the last sort key and restart the query")
	assert.Nil(t, last)
}
