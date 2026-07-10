// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-comm-go/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "vega-backend/errors"
	"vega-backend/interfaces"
	mock_interfaces "vega-backend/interfaces/mock"
)

// NewRawQueryServiceWithDeps 创建SQL查询服务（用于测试）
func NewRawQueryServiceWithDeps(cs interfaces.CatalogService, rs interfaces.ResourceService) interfaces.RawQueryService {
	return &rawQueryService{cs: cs, rs: rs}
}

func TestExecuteRejectsDisabledCatalogForOpenSearchQuery(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	service := NewRawQueryServiceWithDeps(mockCS, mockRS)

	mockRS.EXPECT().GetByID(gomock.Any(), "resource-1").
		Return(&interfaces.Resource{ID: "resource-1", CatalogID: "catalog-1"}, nil)
	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false, ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)

	_, err := service.Execute(context.Background(), &interfaces.RawQueryRequest{
		ResourceType: interfaces.ConnectorTypeOpenSearch,
		Query:        map[string]any{"resource_id": "resource-1"},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.HTTPCode != http.StatusConflict {
		t.Fatalf("expected HTTP 409, got %d", httpErr.HTTPCode)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_Catalog_IsDisabled {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
	}
}

func TestExecuteRejectsDisabledCatalogForExistingStreamSession(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockCS := mock_interfaces.NewMockCatalogService(ctrl)
	mockRS := mock_interfaces.NewMockResourceService(ctrl)
	service := NewRawQueryServiceWithDeps(mockCS, mockRS)

	session, err := GetStreamQueryManager().CreateSession(
		interfaces.ConnectorTypeMariaDB,
		"catalog",
		"catalog-1",
		&interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeMariaDB},
		100,
		"select * from {{resource-1}}",
		[]string{"resource-1"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer GetStreamQueryManager().RemoveSession(session.QueryID)

	mockCS.EXPECT().GetByID(gomock.Any(), "catalog-1", true).
		Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false, ConnectorType: interfaces.ConnectorTypeMariaDB}, nil)

	_, err = service.Execute(context.Background(), &interfaces.RawQueryRequest{
		QueryType: interfaces.QueryType_Stream,
		QueryID:   session.QueryID,
	})
	assertCatalogDisabledError(t, err)
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	httpErr, ok := err.(*rest.HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.HTTPCode != http.StatusConflict {
		t.Fatalf("expected HTTP 409, got %d", httpErr.HTTPCode)
	}
	if httpErr.BaseError.ErrorCode != verrors.VegaBackend_Catalog_IsDisabled {
		t.Fatalf("expected %s, got %s", verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
	}
}

func TestRawQueryServiceValidateRequest(t *testing.T) {
	svc := &rawQueryService{}
	tests := []struct {
		name       string
		req        *interfaces.RawQueryRequest
		wantStatus int
	}{
		{
			name:       "rejects unsupported query type",
			req:        &interfaces.RawQueryRequest{QueryType: "batch", Query: "select 1"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "requires query when query id is absent",
			req:        &interfaces.RawQueryRequest{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "requires sql string for non opensearch",
			req:        &interfaces.RawQueryRequest{Query: map[string]any{}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "requires opensearch map query",
			req: &interfaces.RawQueryRequest{
				ResourceType: interfaces.ConnectorTypeOpenSearch,
				Query:        "not-json-object",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "requires sort for opensearch stream query",
			req: &interfaces.RawQueryRequest{
				QueryType:    interfaces.QueryType_Stream,
				ResourceType: interfaces.ConnectorTypeOpenSearch,
				Query:        map[string]any{"resource_id": "r1"},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "rejects stream query id and query together",
			req: &interfaces.RawQueryRequest{
				QueryType:  interfaces.QueryType_Stream,
				QueryID:    "q1",
				Query:      "select * from {{r1}}",
				StreamSize: 100,
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "requires stream size for sql stream",
			req: &interfaces.RawQueryRequest{
				QueryType: interfaces.QueryType_Stream,
				Query:     "select * from {{r1}}",
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
}

func TestRawQueryServiceValidateRequestSuccess(t *testing.T) {
	svc := &rawQueryService{}
	tests := []struct {
		name string
		req  *interfaces.RawQueryRequest
	}{
		{
			name: "standard sql",
			req:  &interfaces.RawQueryRequest{QueryType: interfaces.QueryType_Standard, Query: "select * from {{r1}}"},
		},
		{
			name: "sql stream with size",
			req: &interfaces.RawQueryRequest{
				QueryType:  interfaces.QueryType_Stream,
				Query:      "select * from {{r1}}",
				StreamSize: 100,
			},
		},
		{
			name: "stream with existing query id",
			req:  &interfaces.RawQueryRequest{QueryType: interfaces.QueryType_Stream, QueryID: "q1"},
		},
		{
			name: "opensearch stream with sort",
			req: &interfaces.RawQueryRequest{
				QueryType:    interfaces.QueryType_Stream,
				ResourceType: interfaces.ConnectorTypeOpenSearch,
				Query: map[string]any{
					"resource_id": "r1",
					"sort":        []any{map[string]any{"created_at": "asc"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, svc.validateRequest(context.Background(), tt.req))
		})
	}
}

func TestRawQueryServiceExtractResourceIDs(t *testing.T) {
	svc := &rawQueryService{}

	got, err := svc.extractResourceIDs("select * from {{.r1}} join {{r2}} on x where id in (select id from {{.r1}})")

	require.NoError(t, err)
	assert.Equal(t, []string{"r1", "r2"}, got)

	got, err = svc.extractResourceIDs(map[string]any{"query": map[string]any{"match_all": map[string]any{}}})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestRawQueryServiceReplaceResourceIDWithSchemaTable(t *testing.T) {
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
}

func TestRawQueryServiceCheckSameDataSource(t *testing.T) {
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
}

func TestRawQueryServiceCheckSameDataSourceErrors(t *testing.T) {
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
	require.NoError(t, ensureCatalogEnabled(context.Background(), nil))
	require.NoError(t, ensureCatalogEnabled(context.Background(), &interfaces.Catalog{Enabled: true}))

	err := ensureCatalogEnabled(context.Background(), &interfaces.Catalog{Enabled: false})

	assertHTTPError(t, err, http.StatusConflict)
	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.BaseError.ErrorCode)
}

func assertHTTPError(t *testing.T, err error, status int) {
	t.Helper()
	require.Error(t, err)

	var httpErr *rest.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, status, httpErr.HTTPCode)
}
