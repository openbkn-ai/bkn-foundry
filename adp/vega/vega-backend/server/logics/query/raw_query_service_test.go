// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
	"vega-backend/logics/connector/factory"
	"vega-backend/logics/query/querypolicy"
)

// NewRawQueryServiceWithDeps 创建SQL查询服务（用于测试）
func NewRawQueryServiceWithDeps(cs interfaces.CatalogService, rs interfaces.ResourceService) interfaces.RawQueryService {
	return &rawQueryService{cs: cs, rs: rs}
}

func expectIndexConnectorClose(connector *mock_interfaces.MockIndexConnector) {
	connector.EXPECT().Close(gomock.Any()).Return(nil).AnyTimes()
}

func TestRawQueryServiceExecute(t *testing.T) {
	t.Run("execute rejects disabled catalog for open search query", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		service := NewRawQueryServiceWithDeps(mockCS, mockRS)

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
			Return(&interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1"}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false, ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)

		_, err := service.Execute(context.Background(), &interfaces.RawQueryRequest{
			Query:        map[string]any{"resource_id": "resource-1"},
			QueryFormat:  interfaces.QueryFormatDSL,
			InputDialect: "opensearch",
		})
		assertCatalogDisabledError(t, err)
	})
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
}

func TestRawQueryValidationError(t *testing.T) {
	err := rawQueryValidationError(context.Background(), &querypolicy.ReadOnlySQLValidationError{
		Reason: "READ_ONLY_SQL_REJECTED: invalid SQL SELECT * FROM accounts WHERE password = 'secret'",
	})

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.BaseError.ErrorCode)
	assert.NotContains(t, httpErr.Error(), "secret")

	assert.NoError(t, rawQueryValidationError(context.Background(), errors.New("unexpected error")))
}

func TestRawQueryTotalCount(t *testing.T) {
	count, err := rawQueryTotalCount(&interfaces.RawQueryResponse{
		Entries: []map[string]any{{rawQueryTotalCountColumn: int64(42)}},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(42), count)

	_, err = rawQueryTotalCount(&interfaces.RawQueryResponse{Entries: []map[string]any{{rawQueryTotalCountColumn: "invalid"}}})
	require.Error(t, err)
}

func TestRawQueryServiceValidateRequest(t *testing.T) {
	svc := &rawQueryService{}
	tests := []struct {
		name       string
		req        *interfaces.RawQueryRequest
		wantStatus int
	}{
		{
			name:       "requires query format",
			req:        &interfaces.RawQueryRequest{Query: "select 1"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "requires query",
			req:        &interfaces.RawQueryRequest{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "rejects cursor without size",
			req: &interfaces.RawQueryRequest{
				Query:       "select 1",
				QueryFormat: interfaces.QueryFormatSQL,
				Paging:      interfaces.PagingRequest{Mode: interfaces.PagingModeCursor},
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.validateRequest(context.Background(), tt.req)

			assertHTTPError(t, err, tt.wantStatus)
		})
	}

	successTests := []struct {
		name string
		req  *interfaces.RawQueryRequest
	}{
		{
			name: "new SQL contract defaults to postgres",
			req: &interfaces.RawQueryRequest{
				Query:       "select * from {{r1}}",
				QueryFormat: interfaces.QueryFormatSQL,
			},
		},
		{
			name: "new opensearch DSL contract",
			req: &interfaces.RawQueryRequest{
				Query:        map[string]any{"resource_id": "r1"},
				QueryFormat:  interfaces.QueryFormatDSL,
				InputDialect: "opensearch",
			},
		},
	}

	for _, tt := range successTests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, svc.validateRequest(context.Background(), tt.req))
		})
	}
}

func TestRawQueryServiceValidateRequestNewContract(t *testing.T) {
	svc := &rawQueryService{}
	err := svc.validateRequest(context.Background(), &interfaces.RawQueryRequest{
		Query:        "select 1",
		QueryFormat:  interfaces.QueryFormatSQL,
		InputDialect: "opensearch",
	})

	assertHTTPError(t, err, http.StatusBadRequest)
}

func TestExtractResourceIDsSupportsHyphenatedIDs(t *testing.T) {
	ids, err := (&rawQueryService{}).extractResourceIDs("SELECT * FROM {{orders-2026}} JOIN {{.customer_data}} ON true")

	require.NoError(t, err)
	assert.Equal(t, []string{"orders-2026", "customer_data"}, ids)
}

func TestQueryExecutionContextAppliesTimeout(t *testing.T) {
	ctx, cancel := queryExecutionContext(context.Background(), 1)
	defer cancel()
	_, ok := ctx.Deadline()
	assert.True(t, ok)

	ctx, cancel = queryExecutionContext(context.Background(), 0)
	defer cancel()
	_, ok = ctx.Deadline()
	assert.False(t, ok)
}

func TestExecuteInitialDSLQueryAppliesTimeout(t *testing.T) {
	ctrl := gomock.NewController(t)
	indexConnector := mock_interfaces.NewMockIndexConnector(ctrl)
	expectIndexConnectorClose(indexConnector)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	svc := &rawQueryService{cs: mockCS, rs: mockRS}

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
		ID:               "resource-1",
		CatalogID:        "catalog-1",
		SourceIdentifier: "events",
	}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
		ID:            "catalog-1",
		Enabled:       true,
		ConnectorType: interfaces.ConnectorTypeOpenSearch,
	}, nil)

	patches := gomonkey.ApplyFunc(factory.GetFactory, func() *factory.ConnectorFactory {
		return &factory.ConnectorFactory{}
	})
	patches.ApplyMethod(&factory.ConnectorFactory{}, "CreateConnectorInstance",
		func(*factory.ConnectorFactory, context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
			return indexConnector, nil
		})
	defer patches.Reset()

	indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, _ map[string]any) (*interfaces.RawQueryResponse, error) {
			_, ok := ctx.Deadline()
			assert.True(t, ok)
			return &interfaces.RawQueryResponse{}, nil
		})

	_, err := svc.executeInitialDSLQuery(context.Background(), &interfaces.RawQueryRequest{
		Query:           map[string]any{"resource_id": "resource-1"},
		QueryTimeoutSec: 1,
		Paging:          interfaces.PagingRequest{Limit: 10},
	})
	require.NoError(t, err)
}

func TestPrepareOpenSearchCursorQuery(t *testing.T) {
	t.Run("requires a stable sort", func(t *testing.T) {
		svc := &rawQueryService{}
		_, _, _, _, err := svc.prepareOpenSearchCursorQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:  map[string]any{"resource_id": "resource-1"},
			Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 10},
		})

		assertHTTPError(t, err, http.StatusBadRequest)
		assert.ErrorContains(t, err, "sort is required")
	})

	t.Run("drops client search after and freezes first page paging", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := mock_interfaces.NewMockCatalogService(ctrl)
		mockRS := mock_interfaces.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: mockCS, rs: mockRS}
		clientSearchAfter := []any{"client-cursor"}
		requestQuery := map[string]any{
			"resource_id":  "resource-1",
			"sort":         []any{"timestamp"},
			"search_after": clientSearchAfter,
			"size":         999,
		}

		mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
			ID:               "resource-1",
			CatalogID:        "catalog-1",
			SourceIdentifier: "events",
		}, nil)
		mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
			ID:            "catalog-1",
			Enabled:       true,
			ConnectorType: interfaces.ConnectorTypeOpenSearch,
		}, nil)

		prepared, index, catalog, warning, err := svc.prepareOpenSearchCursorQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:     requestQuery,
			NeedTotal: true,
			Paging: interfaces.PagingRequest{
				Mode:   interfaces.PagingModeCursor,
				Limit:  25,
				Offset: 50,
			},
		})

		require.NoError(t, err)
		assert.Equal(t, "events", index)
		assert.Equal(t, "catalog-1", catalog.ID)
		assert.Empty(t, warning)
		assert.Equal(t, []any{"timestamp"}, prepared["sort"])
		assert.Equal(t, 25, prepared["size"])
		assert.Equal(t, 50, prepared["from"])
		assert.Equal(t, true, prepared["track_total_hits"])
		assert.NotContains(t, prepared, "resource_id")
		assert.NotContains(t, prepared, "search_after")
		assert.Equal(t, clientSearchAfter, requestQuery["search_after"])
	})
}

func TestInitialOpenSearchCursorRespectsInitialOffsetForLastPage(t *testing.T) {
	ctrl := gomock.NewController(t)
	indexConnector := mock_interfaces.NewMockIndexConnector(ctrl)
	expectIndexConnectorClose(indexConnector)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	svc := &rawQueryService{cs: mockCS, rs: mockRS}

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").Return(&interfaces.Resource{
		ID:               "resource-1",
		CatalogID:        "catalog-1",
		SourceIdentifier: "events",
	}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
		ID:            "catalog-1",
		Enabled:       true,
		ConnectorType: interfaces.ConnectorTypeOpenSearch,
	}, nil)

	patches := gomonkey.ApplyFunc(factory.GetFactory, func() *factory.ConnectorFactory {
		return &factory.ConnectorFactory{}
	})
	patches.ApplyMethod(&factory.ConnectorFactory{}, "CreateConnectorInstance",
		func(*factory.ConnectorFactory, context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
			return indexConnector, nil
		})
	defer patches.Reset()

	indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, query map[string]any) (*interfaces.RawQueryResponse, error) {
			assert.Equal(t, 90, query["from"])
			return &interfaces.RawQueryResponse{
				Entries:     []map[string]any{{"id": "91"}, {"id": "92"}},
				SearchAfter: []any{"page-92"},
				TotalCount:  92,
			}, nil
		})

	result, err := svc.executeInitialOpenSearchCursor(context.Background(), &interfaces.RawQueryRequest{
		Query: map[string]any{
			"resource_id": "resource-1",
			"sort":        []any{"timestamp"},
		},
		NeedTotal: true,
		Paging:    interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: 90, Limit: 2},
	})

	require.NoError(t, err)
	assert.Nil(t, result.Paging.NextCursor)
}

func TestExecuteOpenSearchCursorPage(t *testing.T) {
	ctrl := gomock.NewController(t)
	indexConnector := mock_interfaces.NewMockIndexConnector(ctrl)
	indexConnector.EXPECT().Close(gomock.Any()).Return(nil).Times(2)
	manager := newCursorSessionManager(10)
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = manager
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 2, 60, 0)
	require.NoError(t, err)
	session.QueryFormat = interfaces.QueryFormatDSL
	session.OpenSearchIndex = "events"
	session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "from": 10, "size": 2}
	session.NeedTotal = true

	patches := gomonkey.ApplyFunc(factory.GetFactory, func() *factory.ConnectorFactory {
		return &factory.ConnectorFactory{}
	})
	patches.ApplyMethod(&factory.ConnectorFactory{}, "CreateConnectorInstance",
		func(*factory.ConnectorFactory, context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
			return indexConnector, nil
		})
	defer patches.Reset()

	callCount := 0
	indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, query map[string]any) (*interfaces.RawQueryResponse, error) {
			callCount++
			if callCount == 1 {
				assert.Equal(t, 10, query["from"])
				assert.NotContains(t, query, "search_after")
				return &interfaces.RawQueryResponse{
					Entries:     []map[string]any{{"id": "1"}, {"id": "2"}},
					SearchAfter: []any{"page-1"},
					TotalCount:  3,
				}, nil
			}
			assert.NotContains(t, query, "from")
			assert.Equal(t, []any{"page-1"}, query["search_after"])
			return &interfaces.RawQueryResponse{Entries: []map[string]any{{"id": "3"}}, TotalCount: 3}, nil
		}).Times(2)

	svc := &rawQueryService{}
	catalog := &interfaces.Catalog{ID: "catalog-1", ConnectorType: interfaces.ConnectorTypeOpenSearch}
	first, err := svc.executeOpenSearchCursorPage(context.Background(), session, catalog, nil)
	require.NoError(t, err)
	require.NotNil(t, first.Paging)
	require.NotNil(t, first.Paging.NextCursor)
	assert.Equal(t, session.ID, *first.Paging.NextCursor)
	assert.True(t, first.NeedTotal)

	last, err := svc.executeOpenSearchCursorPage(context.Background(), session, catalog, nil)
	require.NoError(t, err)
	require.NotNil(t, last.Paging)
	assert.Nil(t, last.Paging.NextCursor)
	assert.Nil(t, last.Paging.ExpiresAtSec)
	_, ok := manager.get(session.ID)
	assert.False(t, ok)
}

func TestOpenSearchCursorDoesNotCreateEmptyPageForExactMultiple(t *testing.T) {
	ctrl := gomock.NewController(t)
	indexConnector := mock_interfaces.NewMockIndexConnector(ctrl)
	expectIndexConnectorClose(indexConnector)
	manager := newCursorSessionManager(10)
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = manager
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 2, 60, 0)
	require.NoError(t, err)
	session.QueryFormat = interfaces.QueryFormatDSL
	session.OpenSearchIndex = "events"
	session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 2}
	session.NeedTotal = true

	patches := gomonkey.ApplyFunc(factory.GetFactory, func() *factory.ConnectorFactory {
		return &factory.ConnectorFactory{}
	})
	patches.ApplyMethod(&factory.ConnectorFactory{}, "CreateConnectorInstance",
		func(*factory.ConnectorFactory, context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
			return indexConnector, nil
		})
	defer patches.Reset()

	indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).Return(&interfaces.RawQueryResponse{
		Entries:     []map[string]any{{"id": "1"}, {"id": "2"}},
		SearchAfter: []any{"page-2"},
		TotalCount:  2,
	}, nil)

	result, err := (&rawQueryService{}).executeOpenSearchCursorPage(context.Background(), session,
		&interfaces.Catalog{ID: "catalog-1", ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)
	require.NoError(t, err)
	assert.Nil(t, result.Paging.NextCursor)
	_, ok := manager.get(session.ID)
	assert.False(t, ok)
}

func TestOpenSearchCursorContinuationIsSerialized(t *testing.T) {
	ctrl := gomock.NewController(t)
	indexConnector := mock_interfaces.NewMockIndexConnector(ctrl)
	expectIndexConnectorClose(indexConnector)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	manager := newCursorSessionManager(10)
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = manager
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 1, 60, 0)
	require.NoError(t, err)
	session.QueryFormat = interfaces.QueryFormatDSL
	session.OpenSearchIndex = "events"
	session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 1}
	session.NeedTotal = true

	resource := &interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1", Status: interfaces.ResourceStatusActive}
	catalog := &interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeOpenSearch}
	mockRS.EXPECT().GetByIDs(gomock.Any(), []string{"resource-1"}).Return([]*interfaces.Resource{resource}, nil).Times(2)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).Return(catalog, nil).Times(2)

	patches := gomonkey.ApplyFunc(factory.GetFactory, func() *factory.ConnectorFactory {
		return &factory.ConnectorFactory{}
	})
	patches.ApplyMethod(&factory.ConnectorFactory{}, "CreateConnectorInstance",
		func(*factory.ConnectorFactory, context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
			return indexConnector, nil
		})
	defer patches.Reset()

	callCount := 0
	indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, query map[string]any) (*interfaces.RawQueryResponse, error) {
			callCount++
			if callCount == 1 {
				assert.NotContains(t, query, "search_after")
				return &interfaces.RawQueryResponse{Entries: []map[string]any{{"id": "1"}}, SearchAfter: []any{"page-1"}}, nil
			}
			assert.Equal(t, []any{"page-1"}, query["search_after"])
			return &interfaces.RawQueryResponse{Entries: []map[string]any{}}, nil
		}).Times(2)

	svc := &rawQueryService{cs: mockCS, rs: mockRS}
	ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "account-1"})
	req := &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Cursor: session.ID}}
	errs := make(chan error, 2)
	go func() { _, err := svc.executeSQLCursorContinuation(ctx, req); errs <- err }()
	go func() { _, err := svc.executeSQLCursorContinuation(ctx, req); errs <- err }()

	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	assert.Equal(t, 2, callCount)
	_, ok := manager.get(session.ID)
	assert.False(t, ok)
}

func TestOpenSearchCursorPageFailureDoesNotRefreshExpiry(t *testing.T) {
	ctrl := gomock.NewController(t)
	indexConnector := mock_interfaces.NewMockIndexConnector(ctrl)
	expectIndexConnectorClose(indexConnector)
	manager := newCursorSessionManager(10)
	previousManager := rawQueryCursorSessions
	rawQueryCursorSessions = manager
	t.Cleanup(func() { rawQueryCursorSessions = previousManager })

	session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 1, 60, 0)
	require.NoError(t, err)
	session.OpenSearchIndex = "events"
	session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 1}
	expiresAt := time.Now().Add(30 * time.Second).Unix()
	atomic.StoreInt64(&session.ExpiresAtSec, expiresAt)

	patches := gomonkey.ApplyFunc(factory.GetFactory, func() *factory.ConnectorFactory {
		return &factory.ConnectorFactory{}
	})
	patches.ApplyMethod(&factory.ConnectorFactory{}, "CreateConnectorInstance",
		func(*factory.ConnectorFactory, context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
			return indexConnector, nil
		})
	defer patches.Reset()

	indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).Return(nil, errors.New("backend unavailable"))
	_, err = (&rawQueryService{}).executeOpenSearchCursorPage(context.Background(), session,
		&interfaces.Catalog{ID: "catalog-1", ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)
	require.Error(t, err)
	assert.Equal(t, expiresAt, atomic.LoadInt64(&session.ExpiresAtSec))
}

func TestRawQueryServiceExtractResourceIDs(t *testing.T) {
	t.Run("raw query service extract resource ids", func(t *testing.T) {
		svc := &rawQueryService{}

		got, err := svc.extractResourceIDs("select * from {{.r1}} join {{r2}} on x where id in (select id from {{.r1}})")

		require.NoError(t, err)
		assert.Equal(t, []string{"r1", "r2"}, got)

		got, err = svc.extractResourceIDs(map[string]any{"query": map[string]any{"match_all": map[string]any{}}})
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestHasOpenSearchAggregation(t *testing.T) {
	assert.True(t, hasOpenSearchAggregation(map[string]any{"aggs": map[string]any{"by_category": map[string]any{}}}))
	assert.True(t, hasOpenSearchAggregation(map[string]any{"aggregations": map[string]any{"by_category": map[string]any{}}}))
	assert.False(t, hasOpenSearchAggregation(map[string]any{"query": map[string]any{"match_all": map[string]any{}}}))
}

func TestRawQueryServiceReplaceResourceIDWithSchemaTable(t *testing.T) {
	t.Run("raw query service replace resource idwith schema table", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}

		rs.EXPECT().GetByID(gomock.Any(), "r1").Return(&interfaces.Resource{
			ID:               "r1",
			SourceIdentifier: "schema.table_one",
		}, nil)
		rs.EXPECT().GetByID(gomock.Any(), "r2").Return(&interfaces.Resource{
			ID:               "r2",
			SourceIdentifier: "schema.table_two",
		}, nil)

		got, err := svc.replaceResourceIDWithSchemaTable(context.Background(),
			"select * from {{.r1}} join {{r2}} on {{.r1}}.id = {{r2}}.id",
			[]string{"r1", "r2"},
			&interfaces.Catalog{Name: "catalog"},
		)

		require.NoError(t, err)
		assert.Equal(t, "select * from schema.table_one join schema.table_two on schema.table_one.id = schema.table_two.id", got)
	})
}

func TestRawQueryServiceCheckSameDataSource(t *testing.T) {
	t.Run("raw query service check same data source", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cs := mock_interfaces.NewMockCatalogService(ctrl)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: cs, rs: rs}

		resources := []*interfaces.Resource{
			{ID: "r1", CatalogID: "catalog-1", Status: interfaces.ResourceStatusActive},
			{ID: "r2", CatalogID: "catalog-1", Status: interfaces.ResourceStatusDeprecated},
		}
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"r1", "r2"}).Return(resources, nil)
		cs.EXPECT().GetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
			ID:      "catalog-1",
			Enabled: true,
		}, nil)

		catalog, warnings, err := svc.checkSameDataSource(context.Background(), []string{"r1", "r2"})

		require.NoError(t, err)
		assert.Equal(t, "catalog-1", catalog.ID)
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "r2")
	})

	t.Run("rejects no ids", func(t *testing.T) {
		svc := &rawQueryService{}

		catalog, warnings, err := svc.checkSameDataSource(context.Background(), nil)

		require.Error(t, err)
		assert.Nil(t, catalog)
		assert.Nil(t, warnings)
		assert.ErrorContains(t, err, "no resource ids")
	})

	t.Run("rejects missing resource", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"r1", "missing"}).Return([]*interfaces.Resource{
			{ID: "r1", CatalogID: "catalog-1", Status: interfaces.ResourceStatusActive},
		}, nil)

		catalog, warnings, err := svc.checkSameDataSource(context.Background(), []string{"r1", "missing"})

		assertHTTPError(t, err, http.StatusNotFound)
		assert.Nil(t, catalog)
		assert.Nil(t, warnings)
	})

	t.Run("rejects multi catalog resources", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := mock_interfaces.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}
		rs.EXPECT().GetByIDs(gomock.Any(), []string{"r1", "r2"}).Return([]*interfaces.Resource{
			{ID: "r1", CatalogID: "catalog-1", Status: interfaces.ResourceStatusActive},
			{ID: "r2", CatalogID: "catalog-2", Status: interfaces.ResourceStatusActive},
		}, nil)

		catalog, warnings, err := svc.checkSameDataSource(context.Background(), []string{"r1", "r2"})

		assertHTTPError(t, err, http.StatusNotImplemented)
		assert.Nil(t, catalog)
		assert.Nil(t, warnings)
	})
}

func TestEnsureCatalogEnabled(t *testing.T) {
	t.Run("ensure catalog enabled", func(t *testing.T) {
		require.NoError(t, ensureCatalogEnabled(context.Background(), nil))
		require.NoError(t, ensureCatalogEnabled(context.Background(), &interfaces.Catalog{Enabled: true}))

		err := ensureCatalogEnabled(context.Background(), &interfaces.Catalog{Enabled: false})

		assertHTTPError(t, err, http.StatusConflict)
		var httpErr *rest.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
	})
}

func assertHTTPError(t *testing.T, err error, status int) {
	t.Helper()
	require.Error(t, err)

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, status, httpErr.HTTPCode)
}
