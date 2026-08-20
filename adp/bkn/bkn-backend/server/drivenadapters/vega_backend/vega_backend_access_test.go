// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	rmock "github.com/openbkn-ai/bkn-foundry/comm-go/rest/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

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

func TestQueryResourceDataPreservesLargeIntegers(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
	mockHTTPClient.EXPECT().
		PostNoUnmarshal(gomock.Any(), "http://vega/resources/resource-1/data", gomock.Any(), gomock.Any()).
		Return(http.StatusOK, []byte(`{
			"entries":[{
				"int64_max":9223372036854775807,
				"int64_over":9223372036854775808,
				"uint64_max":18446744073709551615,
				"int64_min":-9223372036854775808,
				"safe":42,
				"ratio":1.5,
				"text":"00123",
				"flag":true,
				"nothing":null,
				"nested":{"uint64_max":18446744073709551615}
			}],
			"total_count":1
		}`), nil)

	access := &vegaBackendAccess{
		httpClient: mockHTTPClient,
		baseUrl:    "http://vega",
	}
	response, err := access.QueryResourceData(context.Background(), "resource-1", &interfaces.ResourceDataQueryParams{
		Paging: interfaces.ResourceDataPagingRequest{Mode: "single"},
	})
	require.NoError(t, err)
	require.Len(t, response.Entries, 1)
	assert.EqualValues(t, 1, response.TotalCount)

	expected := map[string]string{
		"int64_max":  "9223372036854775807",
		"int64_over": "9223372036854775808",
		"uint64_max": "18446744073709551615",
		"int64_min":  "-9223372036854775808",
		"safe":       "42",
		"ratio":      "1.5",
	}
	for field, literal := range expected {
		number, ok := response.Entries[0][field].(json.Number)
		require.Truef(t, ok, "%s should decode as json.Number", field)
		assert.Equal(t, literal, number.String())
	}
	assert.Equal(t, "00123", response.Entries[0]["text"])
	assert.Equal(t, true, response.Entries[0]["flag"])
	assert.Nil(t, response.Entries[0]["nothing"])
	nested, ok := response.Entries[0]["nested"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, json.Number("18446744073709551615"), nested["uint64_max"])

	wire, err := sonic.Marshal(response)
	require.NoError(t, err)
	for _, literal := range expected {
		assert.Contains(t, string(wire), literal)
	}
	assert.False(t, strings.Contains(string(wire), "e+"))
}
