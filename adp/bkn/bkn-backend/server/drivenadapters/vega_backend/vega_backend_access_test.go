// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package vega_backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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

func TestGetResourceSchemaUsesResolvedDelegatorAndOperation(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
	var gotHeaders map[string]string
	mockHTTPClient.EXPECT().
		GetNoUnmarshal(gomock.Any(), "http://vega/resources/resource-1/schema", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params url.Values, headers map[string]string) (int, []byte, error) {
			gotHeaders = headers
			assert.Equal(t, interfaces.OPERATION_TYPE_QUERY_DATA, params.Get("operation"))
			return http.StatusOK, []byte(`{"id":"resource-1","name":"orders","schema_definition":[{"name":"order_id","type":"integer"}]}`), nil
		})

	access := &vegaBackendAccess{httpClient: mockHTTPClient, baseUrl: "http://vega"}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "current-editor", Type: interfaces.ACCESSOR_TYPE_USER})
	ctx = interfaces.WithVerifiedDependencySources(ctx, []interfaces.ProxyGrantResolvedSource{{
		ProxyGrantSourceSpec: interfaces.ProxyGrantSourceSpec{
			ResourceType: "resource", ResourceID: "resource-1", Operation: interfaces.OPERATION_TYPE_QUERY_DATA,
			KNID: "kn-1", BindingType: interfaces.MODULE_TYPE_RELATION_TYPE, BindingID: "rt-1",
		},
		GrantedBy: "historical-grantor",
	}})
	ctx = interfaces.WithDependencyBindingScope(ctx, "kn-1", interfaces.MODULE_TYPE_RELATION_TYPE, "rt-1")

	resource, err := access.GetResourceSchema(ctx, "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA)

	require.NoError(t, err)
	require.NotNil(t, resource)
	assert.Equal(t, "orders", resource.Name)
	assert.Equal(t, "historical-grantor", gotHeaders[interfaces.HTTP_HEADER_ACCOUNT_ID])
	assert.Equal(t, interfaces.ACCESSOR_TYPE_USER, gotHeaders[interfaces.HTTP_HEADER_ACCOUNT_TYPE])
}

func TestGetResourceSchemaRefusesWithoutCallerIdentity(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
	// No GetNoUnmarshal expectation: falling back to the platform administrator
	// and reaching Vega would bypass the resource-scoped permission check.
	access := &vegaBackendAccess{httpClient: mockHTTPClient, baseUrl: "http://vega"}

	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "missing identity", ctx: context.Background()},
		{name: "empty identity", ctx: context.WithValue(context.Background(),
			interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := access.GetResourceSchema(tc.ctx, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL)

			var dependencyErr *interfaces.DependencyError
			require.ErrorAs(t, err, &dependencyErr)
			assert.Equal(t, interfaces.DependencyForbidden, dependencyErr.Kind)
		})
	}
}

func TestGetResourceSchemaClassifiesSafeDependencyErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       []byte
		requestErr error
		kind       interfaces.DependencyErrorKind
	}{
		{name: "forbidden", status: http.StatusForbidden, body: []byte(`{"secret":"permission detail"}`), kind: interfaces.DependencyForbidden},
		{name: "not found", status: http.StatusNotFound, kind: interfaces.DependencyNotFound},
		{name: "timeout", requestErr: context.DeadlineExceeded, kind: interfaces.DependencyTimeout},
		{name: "unavailable", requestErr: errors.New("dial tcp 10.0.0.1: refused"), kind: interfaces.DependencyUnavailable},
		{name: "downstream 4xx", status: http.StatusBadRequest, body: []byte(`{"secret":"contract detail"}`), kind: interfaces.DependencyDownstreamError},
		{name: "downstream 5xx", status: http.StatusInternalServerError, body: []byte(`{"secret":"sql text"}`), kind: interfaces.DependencyDownstreamError},
		{name: "invalid response", status: http.StatusOK, body: []byte(`not-json-with-secret`), kind: interfaces.DependencyInvalidResponse},
		{name: "missing schema", status: http.StatusOK, body: []byte(`{"id":"resource-1","name":"orders"}`), kind: interfaces.DependencyInvalidResponse},
		{name: "null schema", status: http.StatusOK, body: []byte(`{"id":"resource-1","name":"orders","schema_definition":null}`), kind: interfaces.DependencyInvalidResponse},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
			mockHTTPClient.EXPECT().GetNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(tc.status, tc.body, tc.requestErr)
			access := &vegaBackendAccess{httpClient: mockHTTPClient, baseUrl: "http://vega"}
			ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
				interfaces.AccountInfo{ID: "user-1", Type: interfaces.ACCESSOR_TYPE_USER})

			_, err := access.GetResourceSchema(ctx, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL)

			var dependencyErr *interfaces.DependencyError
			require.ErrorAs(t, err, &dependencyErr)
			assert.Equal(t, tc.kind, dependencyErr.Kind)
			assert.NotContains(t, err.Error(), "secret")
			assert.NotContains(t, err.Error(), "10.0.0.1")
		})
	}
}

func TestGetResourceSchemaRejectsVerifiedSourcesWithoutExactBindingScope(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
	access := &vegaBackendAccess{httpClient: mockHTTPClient, baseUrl: "http://vega"}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "current-editor", Type: interfaces.ACCESSOR_TYPE_USER})
	ctx = interfaces.WithVerifiedDependencySources(ctx, []interfaces.ProxyGrantResolvedSource{{
		ProxyGrantSourceSpec: interfaces.ProxyGrantSourceSpec{
			ResourceType: "resource", ResourceID: "resource-1", Operation: interfaces.OPERATION_TYPE_VIEW_DETAIL,
			KNID: "kn-1", BindingType: interfaces.MODULE_TYPE_OBJECT_TYPE, BindingID: "ot-1",
		},
		GrantedBy: "historical-grantor",
	}})

	_, err := access.GetResourceSchema(ctx, "resource-1", interfaces.OPERATION_TYPE_VIEW_DETAIL)

	var dependencyErr *interfaces.DependencyError
	require.ErrorAs(t, err, &dependencyErr)
	assert.Equal(t, interfaces.DependencyInvalidResponse, dependencyErr.Kind)
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

func TestWriteDatasetDocumentUsesSingleDocumentReplace(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
	access := &vegaBackendAccess{baseUrl: "http://vega", httpClient: mockHTTPClient}
	document := map[string]any{"_id": "document-1", "name": "one"}

	mockHTTPClient.EXPECT().
		PutNoUnmarshal(gomock.Any(), "http://vega/resources/dataset-1/data/document-1", gomock.Any(), document).
		Return(http.StatusOK, []byte(`{}`), nil)

	require.NoError(t, access.WriteDatasetDocument(context.Background(), "dataset-1", "document-1", document))
}

func TestWriteDatasetDocumentUsesExplicitDocumentID(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
	access := &vegaBackendAccess{baseUrl: "http://vega", httpClient: mockHTTPClient}
	document := map[string]any{"name": "one"}

	mockHTTPClient.EXPECT().
		PutNoUnmarshal(gomock.Any(), "http://vega/resources/dataset-1/data/document-1", gomock.Any(), document).
		Return(http.StatusOK, []byte(`{}`), nil)

	require.NoError(t, access.WriteDatasetDocument(context.Background(), "dataset-1", "document-1", document))
}

// The admin fallback in buildHeaders would authorize a read as the platform
// instead of as the caller, turning vega's per-resource check off without any
// signal. Raw query must refuse before reaching the network.
func TestRawQueryRefusesWithoutCallerIdentity(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
	// No PostNoUnmarshal expectation: reaching the network is itself the bug.

	access := &vegaBackendAccess{httpClient: mockHTTPClient, baseUrl: "http://vega"}

	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"no identity at all", context.Background()},
		{"identity present but empty", context.WithValue(context.Background(),
			interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := access.RawQuery(tc.ctx, &interfaces.RawQueryRequest{
				Query:        "SELECT 1 FROM {{.r1}}",
				InputDialect: interfaces.VEGA_DIALECT_MYSQL,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "identity")
		})
	}
}

func TestRawQueryForwardsCallerIdentityAndDialect(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)

	var gotHeaders map[string]string
	var gotBody any
	mockHTTPClient.EXPECT().
		PostNoUnmarshal(gomock.Any(), "http://vega/resources/query", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, headers map[string]string, body any) (int, []byte, error) {
			gotHeaders, gotBody = headers, body
			return http.StatusOK, []byte(`{
				"columns":[{"name":"order_id","type":"integer"}],
				"entries":[{"order_id":9223372036854775807}],
				"total_count":1
			}`), nil
		})

	access := &vegaBackendAccess{httpClient: mockHTTPClient, baseUrl: "http://vega"}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-42", Type: "user"})

	resp, err := access.RawQuery(ctx, &interfaces.RawQueryRequest{
		Query:        "SELECT o.order_id FROM {{.r1}} o",
		InputDialect: interfaces.VEGA_DIALECT_POSTGRES,
	})
	require.NoError(t, err)

	// The end user, never the admin constant.
	assert.Equal(t, "user-42", gotHeaders[interfaces.HTTP_HEADER_ACCOUNT_ID])
	assert.Equal(t, "user", gotHeaders[interfaces.HTTP_HEADER_ACCOUNT_TYPE])
	assert.NotEqual(t, interfaces.ADMIN_ACCOUNT_ID, gotHeaders[interfaces.HTTP_HEADER_ACCOUNT_ID])

	sent, ok := gotBody.(*interfaces.RawQueryRequest)
	require.True(t, ok)
	assert.Equal(t, interfaces.VEGA_QUERY_FORMAT_SQL, sent.QueryFormat, "query_format defaults to sql")
	assert.Equal(t, interfaces.VEGA_DIALECT_POSTGRES, sent.InputDialect, "dialect must reach vega unchanged")

	require.Len(t, resp.Columns, 1)
	assert.Equal(t, "order_id", resp.Columns[0].Name)
	// Ids must survive as written rather than being narrowed through float64.
	assert.Equal(t, "9223372036854775807", toJSONNumberString(t, resp.Entries[0]["order_id"]))
}

func toJSONNumberString(t *testing.T, v any) string {
	t.Helper()
	n, ok := v.(json.Number)
	require.True(t, ok, "expected json.Number, got %T", v)
	return n.String()
}

func TestRawQueryRejectsIncompleteRequest(t *testing.T) {
	access := &vegaBackendAccess{baseUrl: "http://vega"}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
		interfaces.AccountInfo{ID: "user-42", Type: "user"})

	_, err := access.RawQuery(ctx, &interfaces.RawQueryRequest{InputDialect: interfaces.VEGA_DIALECT_MYSQL})
	require.Error(t, err)

	_, err = access.RawQuery(ctx, &interfaces.RawQueryRequest{Query: "SELECT 1 FROM {{.r1}}"})
	require.Error(t, err, "an unset dialect would silently take vega's postgres default")
}

// Three ways the dependency can fail. In each of them the statement, which
// names physical tables and columns, must not travel back to the caller: it is
// summarised into the log instead.
func TestRawQueryDoesNotReturnDependencyDetail(t *testing.T) {
	const statement = "SELECT o.order_id FROM {{.r1}} o WHERE o.email = 'a@example.com'"

	for _, tc := range []struct {
		name     string
		respond  func() (int, []byte, error)
		leakFree []string
	}{
		{
			name:    "transport failure",
			respond: func() (int, []byte, error) { return 0, nil, errors.New("dial tcp 10.0.0.1:13014: refused") },
			// The transport's own message may quote the request it carried.
			leakFree: []string{"10.0.0.1", "order_id", "a@example.com"},
		},
		{
			name: "error status with a body",
			respond: func() (int, []byte, error) {
				return http.StatusBadRequest, []byte(`{"error_details":"syntax error near 'o.email'"}`), nil
			},
			leakFree: []string{"o.email", "syntax error", "a@example.com"},
		},
		{
			name:     "body that is not the expected shape",
			respond:  func() (int, []byte, error) { return http.StatusOK, []byte(`not json`), nil },
			leakFree: []string{"a@example.com", "order_id"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			mockHTTPClient := rmock.NewMockHTTPClient(mockCtrl)
			mockHTTPClient.EXPECT().
				PostNoUnmarshal(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, string, map[string]string, any) (int, []byte, error) {
					return tc.respond()
				})

			access := &vegaBackendAccess{httpClient: mockHTTPClient, baseUrl: "http://vega"}
			ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY,
				interfaces.AccountInfo{ID: "user-42", Type: "user"})

			_, err := access.RawQuery(ctx, &interfaces.RawQueryRequest{
				Query:        statement,
				InputDialect: interfaces.VEGA_DIALECT_MYSQL,
			})
			require.Error(t, err)
			for _, forbidden := range tc.leakFree {
				assert.NotContains(t, err.Error(), forbidden)
			}
		})
	}
}
