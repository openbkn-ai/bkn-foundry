// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"bkn-backend/interfaces"
)

func TestResourceDataQueryRequestUsesPagingContract(t *testing.T) {
	t.Run("first page", func(t *testing.T) {
		request, err := resourceDataQueryRequest(&interfaces.ResourceDataQueryParams{
			Paging: interfaces.ResourceDataPagingRequest{Mode: "single", Offset: 40, Limit: 80},
		})
		require.NoError(t, err)
		assert.Equal(t, interfaces.ResourceDataPagingRequest{Mode: "single", Offset: 40, Limit: 80}, request.Paging)
	})

	t.Run("opaque cursor continuation", func(t *testing.T) {
		request, err := resourceDataQueryRequest(&interfaces.ResourceDataQueryParams{
			Paging: interfaces.ResourceDataPagingRequest{Cursor: "cursor-1"},
		})
		require.NoError(t, err)
		assert.Equal(t, interfaces.ResourceDataPagingRequest{Cursor: "cursor-1"}, request.Paging)
	})

	t.Run("cursor first page", func(t *testing.T) {
		request, err := resourceDataQueryRequest(&interfaces.ResourceDataQueryParams{
			Paging: interfaces.ResourceDataPagingRequest{Mode: "cursor", Offset: 4, Limit: 20},
			Sort:   []*interfaces.SortParams{{Field: "id", Direction: "asc"}},
		})
		require.NoError(t, err)
		assert.Equal(t, interfaces.ResourceDataPagingRequest{Mode: "cursor", Offset: 4, Limit: 20}, request.Paging)
	})

	t.Run("paging is required", func(t *testing.T) {
		_, err := resourceDataQueryRequest(&interfaces.ResourceDataQueryParams{})
		require.Error(t, err)
	})
}
