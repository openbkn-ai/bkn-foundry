// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/agiledragon/gomonkey/v2"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	verrors "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	mysql "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/querypolicy"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/sqlglot"
)

type deadlineInspectingPolicy struct {
	sawDeadline bool
}

type recordingPolicy struct {
	dialects        []string
	derivedDialects []string
	resourceIDs     []string
}

type builderConnector struct {
	interfaces.Connector
	run  func(context.Context, *sb.SelectBuilder) (*interfaces.RawQueryResponse, error)
	open func(context.Context, *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error)
}

type unavailableSQLPolicy struct{}

// NewRawQueryServiceWithDeps 创建SQL查询服务（用于测试）
func NewRawQueryServiceWithDeps(cs interfaces.CatalogService, rs interfaces.ResourceService) interfaces.RawQueryService {
	return &rawQueryService{cs: cs, rs: rs}
}

func TestTargetDialectForCatalog(t *testing.T) {
	cases := []struct {
		name          string
		connectorType string
		want          string
	}{
		{name: "mysql", connectorType: interfaces.ConnectorTypeMySQL, want: "mysql"},
		{name: "mariadb", connectorType: interfaces.ConnectorTypeMariaDB, want: "mysql"},
		{name: "postgresql", connectorType: interfaces.ConnectorTypePostgreSQL, want: "postgres"},
		{name: "sqlserver", connectorType: interfaces.ConnectorTypeSQLServer, want: "tsql"},
		{name: "oracle", connectorType: interfaces.ConnectorTypeOracle, want: "oracle"},
		{name: "hana", connectorType: interfaces.ConnectorTypeHANA, want: sqlglot.GenericDialect},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := targetDialectForCatalog(context.Background(), &interfaces.Catalog{ConnectorType: tc.connectorType})
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	t.Run("rejects unsupported connector type", func(t *testing.T) {
		got, err := targetDialectForCatalog(context.Background(), &interfaces.Catalog{ConnectorType: "unknown"})
		require.Error(t, err)
		assert.Empty(t, got)
	})
}

func int64Pointer(value int64) *int64 {
	return &value
}

func expectIndexConnectorClose(connector *vmock.MockIndexConnector) {
	connector.EXPECT().Close(gomock.Any()).Return(nil).AnyTimes()
}

func expectRawQueryResource(mockRS *vmock.MockResourceService,
	resourceID string, resource *interfaces.Resource) {
	mockRS.EXPECT().CheckResourcePermission(gomock.Any(), resourceID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
	mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, resourceID).Return(resource, nil)
}

func expectRawQueryResources(mockRS *vmock.MockResourceService,
	resourceIDs []string, resources ...*interfaces.Resource) {
	for _, resourceID := range resourceIDs {
		mockRS.EXPECT().CheckResourcePermission(gomock.Any(), resourceID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
	}
	resourcesByID := make(map[string]*interfaces.Resource, len(resources))
	for _, resource := range resources {
		resourcesByID[resource.ID] = resource
	}
	mockRS.EXPECT().InternalGetByIDs(gomock.Any(), resourceIDs).Return(resourcesByID, nil)
}

func (p *recordingPolicy) ExtractTableResourceIDs(context.Context, string, string) ([]string, error) {
	return p.resourceIDs, nil
}

func (p *recordingPolicy) ValidateSQL(_ context.Context, _ string, dialect string) error {
	p.dialects = append(p.dialects, dialect)
	return nil
}

func (p *recordingPolicy) ValidateDerivedTable(_ context.Context, _ string, dialect string) error {
	p.derivedDialects = append(p.derivedDialects, dialect)
	return nil
}

func (p *deadlineInspectingPolicy) ExtractTableResourceIDs(context.Context, string, string) ([]string, error) {
	return []string{"resource-1"}, nil
}

func (p *recordingPolicy) ValidateTableReferences(context.Context, string, string, []string) error {
	return nil
}

func (p *deadlineInspectingPolicy) ValidateSQL(ctx context.Context, _ string, _ string) error {
	_, p.sawDeadline = ctx.Deadline()
	return errors.New("stop after inspecting policy context")
}

func (p *deadlineInspectingPolicy) ValidateDerivedTable(ctx context.Context, _ string, _ string) error {
	_, p.sawDeadline = ctx.Deadline()
	return errors.New("stop after inspecting policy context")
}

func (p *deadlineInspectingPolicy) ValidateTableReferences(context.Context, string, string, []string) error {
	return nil
}

func TestRawQueryServiceExecute(t *testing.T) {
	for _, connectorType := range []string{interfaces.ConnectorTypeMySQL, interfaces.ConnectorTypeMariaDB} {
		t.Run(connectorType+" actual connector", func(t *testing.T) {
			t.Run("single page binds exact values and counts the logical query", func(t *testing.T) {
				svc, rs, cs, cf := mysqlRawServices(t)
				mysqlRawMetadata(rs, cs, builderTestResource(), true, connectorType)
				mocks := nativeSQLConnectorMocks(t, cf, connectorType, 2)
				base := "SELECT `o`.`id`, CAST(? AS DECIMAL(19,3)) AS `amount`, ? AS `optional` FROM `sales.eu`.`order.items` AS `o` WHERE (`o`.`id` > ?) ORDER BY `o`.`id` ASC"
				mocks[0].ExpectQuery("SELECT COUNT(*) AS `_raw_query_total_count` FROM ("+base+" LIMIT 5 OFFSET 2) AS `_raw_query_total`").
					WithArgs("9007199254740993.125", nil, int64(1)).
					WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn(rawQueryTotalCountColumn).OfType("BIGINT", int64(0))).AddRow(5)).RowsWillBeClosed()
				mocks[0].ExpectClose()
				mocks[1].ExpectQuery(base+" LIMIT 2 OFFSET 3").WithArgs("9007199254740993.125", nil, int64(1)).
					WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
						sqlmock.NewColumn("id").OfType("BIGINT", int64(0)),
						sqlmock.NewColumn("amount").OfType("DECIMAL", ""),
						sqlmock.NewColumn("optional").OfType("VARCHAR", ""),
					).AddRow(4, []byte("9007199254740993.125"), nil).AddRow(5, []byte("9007199254740993.125"), nil)).RowsWillBeClosed()
				mocks[1].ExpectClose()

				result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{
					QueryFormat: interfaces.QueryFormatSQL,
					Query:       "SELECT o.id, 9007199254740993.125 AS amount, NULL AS optional FROM {{ orders }} o WHERE o.id > 1 ORDER BY o.id LIMIT 5 OFFSET 2",
					NeedTotal:   true, Paging: interfaces.PagingRequest{Limit: 2, Offset: 1},
				})
				require.NoError(t, err)
				assert.Equal(t, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "amount", Type: interfaces.DataType_Decimal}, {Name: "optional", Type: interfaces.DataType_String}}, result.Columns)
				assert.Equal(t, []map[string]any{{"id": int64(4), "amount": "9007199254740993.125", "optional": nil}, {"id": int64(5), "amount": "9007199254740993.125", "optional": nil}}, result.Entries)
				require.NotNil(t, result.TotalCount)
				assert.EqualValues(t, 5, *result.TotalCount)
				assert.Equal(t, []string{"orders"}, result.ResourceIDs)
			})
			t.Run("cursor keeps the actual connector and Rows after the first request ends", func(t *testing.T) {
				svc, rs, cs, cf := mysqlRawServices(t)
				resource := builderTestResource()
				mysqlRawMetadata(rs, cs, resource, true, connectorType)
				mocks := nativeSQLConnectorMocks(t, cf, connectorType, 1)
				mocks[0].ExpectQuery("SELECT `id`, CAST(? AS DECIMAL(19,3)) AS `amount` FROM `sales.eu`.`order.items` WHERE (`id` > ?) ORDER BY `id` ASC LIMIT 3 OFFSET 2").
					WithArgs("9007199254740993.125", int64(0)).
					WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
						sqlmock.NewColumn("id").OfType("BIGINT", int64(0)),
						sqlmock.NewColumn("amount").OfType("DECIMAL", ""),
					).AddRow(3, []byte("9007199254740993.125")).AddRow(4, nil).AddRow(5, []byte("9007199254740993.125"))).RowsWillBeClosed()
				mocks[0].ExpectClose()
				firstCtx, cancel := context.WithCancel(compileTestContext())
				defer cancel()
				first, err := svc.Execute(firstCtx, &interfaces.RawQueryRequest{
					QueryFormat: interfaces.QueryFormatSQL,
					Query:       "SELECT id, 9007199254740993.125 AS amount FROM {{orders}} WHERE id > 0 ORDER BY id LIMIT 4 OFFSET 1",
					Paging:      interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2, Offset: 1},
				})
				require.NoError(t, err)
				require.NotNil(t, first.Paging.NextCursor)
				token := *first.Paging.NextCursor
				assert.Equal(t, []map[string]any{{"id": int64(3), "amount": "9007199254740993.125"}, {"id": int64(4), "amount": nil}}, first.Entries)
				session, ok := rawQueryCursorSessions.acquire(token)
				require.True(t, ok)
				retainedRows := session.SQLRows
				assert.IsType(t, &mysql.MariaDBConnector{}, session.SQLConnector)
				rawQueryCursorSessions.release(session)
				cancel()

				mysqlRawMetadata(rs, cs, resource, true, connectorType)
				last, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
				require.NoError(t, err)
				assert.Equal(t, first.Columns, last.Columns)
				assert.Equal(t, []map[string]any{{"id": int64(5), "amount": "9007199254740993.125"}}, last.Entries)
				assert.Nil(t, last.TotalCount)
				assert.Nil(t, last.Paging.NextCursor)
				assert.NoError(t, retainedRows.Err())
				assert.Empty(t, rawQueryCursorSessions.sessions)
			})
			for _, failure := range []string{"unknown column", "duplicate output names", "cursor read failure"} {
				t.Run(failure+" releases the source without exposing its error", func(t *testing.T) {
					svc, rs, cs, cf := mysqlRawServices(t)
					mysqlRawMetadata(rs, cs, builderTestResource(), true, connectorType)
					mocks := nativeSQLConnectorMocks(t, cf, connectorType, 1)
					request := &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Limit: 2}}
					statement := "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 2"
					status := http.StatusBadRequest
					if failure == "cursor read failure" {
						request.Paging.Mode = interfaces.PagingModeCursor
						statement = "SELECT `id` FROM `sales.eu`.`order.items`"
						status = http.StatusInternalServerError
					}
					expected := mocks[0].ExpectQuery(statement)
					switch failure {
					case "unknown column":
						expected.WillReturnError(&mysqldriver.MySQLError{Number: 1054, Message: "private source detail"})
					case "duplicate output names":
						expected.WillReturnRows(sqlmock.NewRows([]string{"id", "id"}).AddRow(1, 2)).RowsWillBeClosed()
					case "cursor read failure":
						expected.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).RowError(1, errors.New("private source detail"))).RowsWillBeClosed()
					}
					mocks[0].ExpectClose()
					result, err := svc.Execute(compileTestContext(), request)
					assertHTTPError(t, err, status)
					assert.Nil(t, result)
					assert.NotContains(t, err.Error(), "private source detail")
					assert.Empty(t, rawQueryCursorSessions.sessions)
				})
			}
		})
	}
	t.Run("execute rejects disabled catalog for open search query", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		service := NewRawQueryServiceWithDeps(mockCS, mockRS)

		expectRawQueryResource(mockRS, "resource-1", &interfaces.Resource{
			ID: "resource-1", CatalogID: "catalog-1", Enabled: true, SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		})
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false, ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)

		_, err := service.Execute(context.Background(), &interfaces.RawQueryRequest{
			Query:        map[string]any{"resource_id": "resource-1"},
			QueryFormat:  interfaces.QueryFormatDSL,
			InputDialect: "opensearch",
		})
		assertCatalogDisabledError(t, err)
	})
	rejectedTSQLTests := []struct {
		name              string
		sql               string
		hasTableReference bool
		needTotal         bool
	}{
		{name: "exec", sql: "EXEC('SELECT * FROM {{resource-1}}')"},
		{name: "select into", sql: "SELECT * INTO archived_orders FROM {{resource-1}}", hasTableReference: true},
		{name: "ddl", sql: "DROP TABLE {{resource-1}}", hasTableReference: true},
		{name: "dml", sql: "UPDATE {{resource-1}} SET status = 'closed'", hasTableReference: true},
		{name: "multiple statements", sql: "SELECT * FROM {{resource-1}}; DELETE FROM {{resource-1}}", hasTableReference: true},
		{name: "top with ties", sql: "SELECT TOP (10) WITH TIES id FROM {{resource-1}} ORDER BY score", hasTableReference: true},
		{name: "top percent", sql: "SELECT TOP (10) PERCENT id FROM {{resource-1}} ORDER BY score", hasTableReference: true},
		{name: "for json", sql: "SELECT id FROM {{resource-1}} ORDER BY id FOR JSON PATH", hasTableReference: true},
		{name: "for xml", sql: "SELECT id FROM {{resource-1}} FOR XML PATH", hasTableReference: true},
		{name: "next sequence value", sql: "SELECT NEXT VALUE FOR dbo.order_seq OVER (ORDER BY id), id FROM {{resource-1}} ORDER BY id", hasTableReference: true},
		{name: "offset fetch", sql: "SELECT id FROM {{resource-1}} ORDER BY id OFFSET 10 ROWS FETCH NEXT 20 ROWS ONLY", hasTableReference: true},
		{name: "table lock hint", sql: "SELECT id FROM {{resource-1}} WITH (TABLOCKX)", hasTableReference: true},
		{name: "non-literal top", sql: "SELECT TOP (5 + 5) id FROM {{resource-1}} ORDER BY id", hasTableReference: true},
		{name: "unnamed aggregate with total", sql: "SELECT COUNT(*) FROM {{resource-1}}", hasTableReference: true, needTotal: true},
	}
	for _, test := range rejectedTSQLTests {
		t.Run("rejects tsql before connector creation: "+test.name, func(t *testing.T) {
			requireRawQuerySQLGlotRuntime(t)

			ctrl := gomock.NewController(t)
			var connectorCreations atomic.Int64
			connectorFactory := vmock.NewMockConnectorFactory(ctrl)
			connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
					connectorCreations.Add(1)
					return nil, errors.New("connector must not be created")
				}).AnyTimes()

			previousPolicy := rawQueryPolicy
			rawQueryPolicy = querypolicy.NewSQLGlotAdapter()
			t.Cleanup(func() { rawQueryPolicy = previousPolicy })

			catalogService := vmock.NewMockCatalogService(ctrl)
			resourceService := vmock.NewMockResourceService(ctrl)
			svc := &rawQueryService{cs: catalogService, rs: resourceService, cf: connectorFactory}
			if test.hasTableReference {
				resource := &interfaces.Resource{
					ID:               "resource-1",
					Enabled:          true,
					CatalogID:        "catalog-1",
					SourceIdentifier: "[dbo].[orders]",
					Status:           interfaces.ResourceStatusActive,
					SchemaDefinition: []*interfaces.Property{{Name: "id"}},
				}
				expectRawQueryResources(resourceService, []string{"resource-1"}, resource)
				catalogService.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
					ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeSQLServer,
				}, nil)
				resourceService.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").Return(resource, nil).Times(2)
			}

			result, err := svc.Execute(context.Background(), &interfaces.RawQueryRequest{
				Query:        test.sql,
				QueryFormat:  interfaces.QueryFormatSQL,
				InputDialect: "tsql",
				Paging:       interfaces.PagingRequest{Mode: interfaces.PagingModeSingle, Limit: 10},
				NeedTotal:    test.needTotal,
			})

			assertHTTPError(t, err, http.StatusBadRequest)
			assert.Nil(t, result)
			assert.Zero(t, connectorCreations.Load())
		})
	}
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.ErrorCode)
}

func TestRawQueryValidationError(t *testing.T) {
	t.Run("sanitizes read-only validation errors", func(t *testing.T) {
		err := rawQueryValidationError(context.Background(), &querypolicy.ReadOnlySQLValidationError{
			Reason: "READ_ONLY_SQL_REJECTED: invalid SQL SELECT * FROM accounts WHERE password = 'secret'",
		})

		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusBadRequest, httpErr.HTTPCode)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
		assert.NotContains(t, httpErr.Error(), "secret")
	})
	t.Run("ignores unrelated errors", func(t *testing.T) {
		assert.NoError(t, rawQueryValidationError(context.Background(), errors.New("unexpected error")))
	})
}

func TestRawQueryTotalCount(t *testing.T) {
	t.Run("returns total count", func(t *testing.T) {
		count, err := rawQueryTotalCount(&interfaces.RawQueryResponse{
			Entries: []map[string]any{{rawQueryTotalCountColumn: json.Number("9007199254740993")}},
		})
		require.NoError(t, err)
		assert.Equal(t, int64(9007199254740993), count)
	})
	t.Run("rejects invalid total count", func(t *testing.T) {
		_, err := rawQueryTotalCount(&interfaces.RawQueryResponse{
			Entries: []map[string]any{{rawQueryTotalCountColumn: "invalid"}},
		})
		require.Error(t, err)
	})
}

func TestWithExecutedResourceIDs(t *testing.T) {
	t.Run("copies internal resource IDs without serializing them", func(t *testing.T) {
		result := &interfaces.RawQueryResponse{
			Entries: []map[string]any{{"order_id": "PO-2024-001"}},
		}
		resourceIDs := []string{"res_purchase_order", "res_supplier"}

		got := withExecutedResourceIDs(result, resourceIDs)
		resourceIDs[0] = "mutated"

		require.Same(t, result, got)
		assert.Equal(t, []string{"res_purchase_order", "res_supplier"}, got.ResourceIDs)
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "ResourceIDs")
		assert.NotContains(t, string(raw), "res_purchase_order")
	})
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
			name: "new SQL contract defaults to mysql",
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
	t.Run("rejects incompatible input dialect", func(t *testing.T) {
		svc := &rawQueryService{}
		err := svc.validateRequest(context.Background(), &interfaces.RawQueryRequest{
			Query:        "select 1",
			QueryFormat:  interfaces.QueryFormatSQL,
			InputDialect: "opensearch",
		})

		assertHTTPError(t, err, http.StatusBadRequest)
	})
}

func TestRawQueryServicePrepareSQLQuery(t *testing.T) {

	t.Run("preserves ASCII TSQL without recompilation", func(t *testing.T) {
		requireRawQuerySQLGlotRuntime(t)
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		resource := &interfaces.Resource{
			ID: "resource-1", Enabled: true, CatalogID: "catalog-1",
			Schema: "dbo", SourceIdentifier: "[dbo].[orders]", Status: interfaces.ResourceStatusActive,
			SchemaDefinition: []*interfaces.Property{{Name: "created_at"}},
		}
		expectRawQueryResources(mockRS, []string{"resource-1"}, resource)
		mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").Return(resource, nil).Times(2)
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
			ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeSQLServer,
		}, nil)

		previousPolicy := rawQueryPolicy
		rawQueryPolicy = querypolicy.NewSQLGlotAdapter()
		t.Cleanup(func() { rawQueryPolicy = previousPolicy })

		patches := gomonkey.ApplyFunc(sqlglot.TranspileSQL,
			func(context.Context, string, string, string) (*sqlglot.SQLParseResult, error) {
				t.Error("ASCII TSQL must not be recompiled")
				return nil, errors.New("unexpected compilation")
			})
		defer patches.Reset()

		prepared, err := (&rawQueryService{cs: mockCS, rs: mockRS}).prepareSQLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query: "select DATEPART(YEAR, created_at) as year_value from {{resource-1}} where status = 'active'", QueryFormat: interfaces.QueryFormatSQL, InputDialect: "tsql",
		})
		require.NoError(t, err)
		assert.Equal(t, "select DATEPART(YEAR, created_at) as year_value from [dbo].[orders] where status = 'active'", prepared.sql)
	})
	for _, inputDialect := range []string{"mysql", "postgres", "tsql"} {
		t.Run("normalizes SQL Server Unicode literals from "+inputDialect, func(t *testing.T) {
			requireRawQuerySQLGlotRuntime(t)
			ctrl := gomock.NewController(t)
			mockCS := vmock.NewMockCatalogService(ctrl)
			mockRS := vmock.NewMockResourceService(ctrl)
			resource := &interfaces.Resource{
				ID: "resource-1", Enabled: true, CatalogID: "catalog-1",
				Schema: "dbo", SourceIdentifier: "[dbo].[orders]", Status: interfaces.ResourceStatusActive,
				SchemaDefinition: []*interfaces.Property{{Name: "name"}},
			}
			expectRawQueryResources(mockRS, []string{"resource-1"}, resource)
			mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").Return(resource, nil).Times(1)
			mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
				ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeSQLServer,
			}, nil)

			previousPolicy := rawQueryPolicy
			rawQueryPolicy = querypolicy.NewSQLGlotAdapter()
			t.Cleanup(func() { rawQueryPolicy = previousPolicy })

			prepared, err := (&rawQueryService{cs: mockCS, rs: mockRS}).prepareSQLQuery(context.Background(), &interfaces.RawQueryRequest{
				Query: "SELECT name FROM {{resource-1}} WHERE name = '刘🙂备'", QueryFormat: interfaces.QueryFormatSQL, InputDialect: inputDialect,
			})
			require.NoError(t, err)
			assert.Equal(t, "SELECT name FROM [dbo].[orders] WHERE name = N'刘🙂备'", prepared.sql)
		})
	}
	for _, tc := range []struct {
		name          string
		inputDialect  string
		connectorType string
		wantDialect   string
		wantSQL       string
	}{
		{name: "generic input for HANA", inputDialect: "generic", connectorType: interfaces.ConnectorTypeHANA, wantDialect: sqlglot.GenericDialect, wantSQL: `SELECT id FROM "APP"."orders"`},
		{name: "Oracle input for Oracle", inputDialect: "oracle", connectorType: interfaces.ConnectorTypeOracle, wantDialect: "oracle", wantSQL: `SELECT id FROM "APP"."orders"`},
		{name: "ASCII TSQL input for SQL Server", inputDialect: "tsql", connectorType: interfaces.ConnectorTypeSQLServer, wantDialect: "tsql", wantSQL: "SELECT id FROM [APP].[orders]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mockCS := vmock.NewMockCatalogService(ctrl)
			mockRS := vmock.NewMockResourceService(ctrl)
			resource := &interfaces.Resource{
				ID: "resource-1", Enabled: true, CatalogID: "catalog-1",
				Schema: "APP", SourceIdentifier: strings.TrimPrefix(tc.wantSQL, "SELECT id FROM "), Status: interfaces.ResourceStatusActive,
				SchemaDefinition: []*interfaces.Property{{Name: "id"}},
			}
			expectRawQueryResources(mockRS, []string{"resource-1"}, resource)
			mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").Return(resource, nil).Times(2)
			mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
				ID: "catalog-1", Enabled: true, ConnectorType: tc.connectorType,
			}, nil)

			policy := &recordingPolicy{resourceIDs: []string{"resource-1"}}
			previousPolicy := rawQueryPolicy
			rawQueryPolicy = policy
			t.Cleanup(func() { rawQueryPolicy = previousPolicy })

			prepared, err := (&rawQueryService{cs: mockCS, rs: mockRS}).prepareSQLQuery(context.Background(), &interfaces.RawQueryRequest{
				Query: "SELECT id FROM {{resource-1}}", QueryFormat: interfaces.QueryFormatSQL, InputDialect: tc.inputDialect,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantSQL, prepared.sql)
			assert.Empty(t, policy.dialects)
			assert.Equal(t, []string{tc.wantDialect}, policy.derivedDialects)
		})
	}

	for _, sql := range []string{
		"SELECT * FROM public.orders /* {{resource-1}} */",
		"SELECT * FROM public.orders -- {{resource-1}}\n",
		"SELECT '{{resource-1}}' FROM public.orders",
	} {
		t.Run("rejects placeholder outside table reference: "+sql, func(t *testing.T) {
			svc := &rawQueryService{}
			previousPolicy := rawQueryPolicy
			rawQueryPolicy = &recordingPolicy{}
			t.Cleanup(func() { rawQueryPolicy = previousPolicy })

			_, err := svc.prepareSQLQuery(context.Background(), &interfaces.RawQueryRequest{
				Query:        sql,
				QueryFormat:  interfaces.QueryFormatSQL,
				InputDialect: "postgres",
			})
			assertHTTPError(t, err, http.StatusBadRequest)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, "at least one resource_id is required for SQL queries", httpErr.ErrorDetails)
		})
	}
	t.Run("revalidates transpiled SQL for unchanged Doris", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: mockCS, rs: mockRS}
		resource := &interfaces.Resource{
			ID:               "resource-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			SourceIdentifier: "`orders`",
			Status:           interfaces.ResourceStatusActive,
			SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		}
		expectRawQueryResources(mockRS, []string{"resource-1"}, resource)
		mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").Return(resource, nil).AnyTimes()
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
			ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeDoris,
		}, nil)

		policy := &recordingPolicy{resourceIDs: []string{"resource-1"}}
		previousPolicy := rawQueryPolicy
		rawQueryPolicy = policy
		t.Cleanup(func() { rawQueryPolicy = previousPolicy })
		patches := gomonkey.ApplyFunc(sqlglot.TranspileSQL,
			func(context.Context, string, string, string) (*sqlglot.SQLParseResult, error) {
				return &sqlglot.SQLParseResult{SQL: "SELECT * FROM __vega_resource_0"}, nil
			})
		defer patches.Reset()
		patches.ApplyFunc(sqlglot.BindTableReferences, func(_ context.Context, sql, dialect string, bindings map[string]string) (*sqlglot.SQLParseResult, error) {
			assert.Equal(t, "SELECT * FROM __vega_resource_0", sql)
			assert.Equal(t, "doris", dialect)
			assert.Equal(t, map[string]string{"__vega_resource_0": "`orders`"}, bindings)
			return &sqlglot.SQLParseResult{SQL: "SELECT * FROM `orders`", Dialect: "doris"}, nil
		})

		prepared, err := svc.prepareSQLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:        "SELECT * FROM {{resource-1}}",
			QueryFormat:  interfaces.QueryFormatSQL,
			InputDialect: "trino",
		})
		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM `orders`", prepared.sql)
		assert.Equal(t, []string{"trino"}, policy.dialects)
		assert.Equal(t, []string{"doris"}, policy.derivedDialects)
	})
}

func TestReplacePlaceholderInSQLCode(t *testing.T) {
	t.Run("preserves comments and literals", func(t *testing.T) {
		got := replacePlaceholderInSQLCode(
			"SELECT '{{resource-1}}' FROM {{resource-1}} /* {{resource-1}} */ -- {{resource-1}}\n",
			"{{resource-1}}", "public.orders", "postgres",
		)
		assert.Equal(t, "SELECT '{{resource-1}}' FROM public.orders /* {{resource-1}} */ -- {{resource-1}}\n", got)
	})

	t.Run("honors mysql backslash escapes", func(t *testing.T) {
		got := replacePlaceholderInSQLCode(
			"SELECT 'it\\'s {{resource-1}}' FROM {{resource-1}}",
			"{{resource-1}}", "public.orders", "mysql",
		)
		assert.Equal(t, "SELECT 'it\\'s {{resource-1}}' FROM public.orders", got)
	})
	for _, dialect := range []string{"mysql", "doris"} {
		t.Run(dialect+" double-quoted backslash escapes", func(t *testing.T) {
			got := replacePlaceholderInSQLCode(
				`SELECT "a\"b {{resource-1}}" FROM {{resource-1}}`,
				"{{resource-1}}", "public.orders", dialect,
			)
			assert.Equal(t, `SELECT "a\"b {{resource-1}}" FROM public.orders`, got)
		})
	}

}

func TestQueryExecutionContext(t *testing.T) {
	t.Run("sets deadline for positive timeout", func(t *testing.T) {
		ctx, cancel := queryExecutionContext(context.Background(), 1)
		defer cancel()
		_, ok := ctx.Deadline()
		assert.True(t, ok)
	})
	t.Run("does not set deadline for zero timeout", func(t *testing.T) {
		ctx, cancel := queryExecutionContext(context.Background(), 0)
		defer cancel()
		_, ok := ctx.Deadline()
		assert.False(t, ok)
	})
}

func TestRawQueryServiceExecuteInitialSQLQuery(t *testing.T) {
	t.Run("propagates query timeout to validation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: mockCS, rs: mockRS}
		resource := &interfaces.Resource{
			ID:               "resource-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			SourceIdentifier: `"public"."orders"`,
			Status:           interfaces.ResourceStatusActive,
			SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		}
		expectRawQueryResources(mockRS, []string{"resource-1"}, resource)
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
			ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypePostgreSQL,
		}, nil)

		policy := &deadlineInspectingPolicy{}
		previousPolicy := rawQueryPolicy
		rawQueryPolicy = policy
		t.Cleanup(func() { rawQueryPolicy = previousPolicy })

		_, err := svc.executeInitialSQLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:           "SELECT * FROM {{resource-1}}",
			QueryFormat:     interfaces.QueryFormatSQL,
			QueryTimeoutSec: 1,
			Paging:          interfaces.PagingRequest{Limit: 1},
		})
		require.Error(t, err)
		assert.True(t, policy.sawDeadline)
	})
}

func TestValidateCursorResourceBinding(t *testing.T) {
	t.Run("validates resource identity and update time", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })
		rawSession, err := rawQueryCursorSessions.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 60)
		require.NoError(t, err)
		err = validateCursorResourceBinding(context.Background(), rawSession, &interfaces.RawQueryRequest{ResourceDataResourceID: "logic-1"})
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		require.NoError(t, validateCursorResourceBinding(context.Background(), rawSession, &interfaces.RawQueryRequest{}))

		session, err := rawQueryCursorSessions.create("account-1", "catalog-1", nil, "SELECT 1", 1, 60, 60)
		require.NoError(t, err)
		bindCursorResource(session, &interfaces.RawQueryRequest{ResourceDataResourceID: "logic-1", ResourceDataUpdateTime: 10})

		err = validateCursorResourceBinding(context.Background(), session, &interfaces.RawQueryRequest{ResourceDataResourceID: "logic-2", ResourceDataUpdateTime: 10})
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		_, exists := rawQueryCursorSessions.acquire(session.ID)
		assert.True(t, exists)
		if exists {
			rawQueryCursorSessions.release(session)
		}

		err = validateCursorResourceBinding(context.Background(), session, &interfaces.RawQueryRequest{ResourceDataResourceID: "logic-1", ResourceDataUpdateTime: 11})
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		_, exists = rawQueryCursorSessions.acquire(session.ID)
		assert.False(t, exists)
	})
}

func requireRawQuerySQLGlotRuntime(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-c",
		"import sqlglot; assert callable(getattr(sqlglot, 'parse_one', None))").Run(); err != nil {
		t.Skip("sqlglot Python runtime is not installed")
	}
}

func TestRawQueryServiceExecuteSQL(t *testing.T) {
	t.Run("single page uses connector paging wrapper", func(t *testing.T) {
		catalog := rawQuerySQLServerCatalog()
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		gomock.InOrder(
			connector.EXPECT().Connect(gomock.Any()).Return(nil),
			connector.EXPECT().BuildPagedSQL("SELECT id FROM dbo.orders", 20, 10).Return("TSQL SINGLE PAGE"),
			connector.EXPECT().ExecuteRawSQL(gomock.Any(), "TSQL SINGLE PAGE").Return(&interfaces.RawQueryResponse{
				Entries: []map[string]any{{"id": 1}},
			}, nil),
			connector.EXPECT().Close(gomock.Any()).Return(nil),
		)

		result, err := svc.executeSQL(context.Background(), catalog,
			"SELECT id FROM dbo.orders", interfaces.PagingModeSingle, &rawSQLBuildOptions{offset: 20, limit: 10})

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 1}}, result.Entries)
	})

	t.Run("does not expose MySQL driver details for an unknown column", func(t *testing.T) {
		catalog := &interfaces.Catalog{ID: "catalog-1", Name: "mariadb-catalog", ConnectorType: interfaces.ConnectorTypeMariaDB}
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteRawSQL(gomock.Any(), "SELECT no_such_column FROM brands").
			Return(nil, fmt.Errorf("execute query failed: %w", interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, errors.New("Unknown column 'no_such_column' in 'field list'"))))

		_, err := svc.executeSQL(context.Background(), catalog,
			"SELECT no_such_column FROM brands", interfaces.PagingModeSingle, nil)

		assertHTTPError(t, err, http.StatusBadRequest)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
		assert.Equal(t, "query references an unknown column", httpErr.ErrorDetails)
	})

	t.Run("returns source read forbidden for connector privilege errors", func(t *testing.T) {
		catalog := rawQuerySQLServerCatalog()
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteRawSQL(gomock.Any(), "SELECT id FROM brands").
			Return(nil, fmt.Errorf("execute query failed: %w",
				interfaces.NewSourceReadForbiddenError(errors.New("source driver privilege details"))))

		result, err := svc.executeSQL(context.Background(), catalog,
			"SELECT id FROM brands", interfaces.PagingModeSingle, nil)

		assert.Nil(t, result)
		assertHTTPError(t, err, http.StatusForbidden)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Resource_SourceReadForbidden, httpErr.ErrorCode)
		assert.Empty(t, httpErr.ErrorDetails)
	})

	t.Run("connects before executing unpaged SQL", func(t *testing.T) {
		catalog := rawQuerySQLServerCatalog()
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().Connect(gomock.Any()).Return(errors.New("connection refused"))

		_, err := svc.executeSQL(context.Background(), catalog,
			"SELECT id FROM dbo.orders", interfaces.PagingModeSingle, nil)

		assertHTTPError(t, err, http.StatusInternalServerError)
	})

	t.Run("does not expose PostgreSQL driver details for an unknown column", func(t *testing.T) {
		catalog := &interfaces.Catalog{ID: "catalog-1", Name: "postgresql-catalog", ConnectorType: interfaces.ConnectorTypePostgreSQL}
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteRawSQL(gomock.Any(), "SELECT no_such_column FROM brands").
			Return(nil, fmt.Errorf("execute query failed: %w", interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, errors.New("column \"no_such_column\" does not exist"))))

		_, err := svc.executeSQL(context.Background(), catalog,
			"SELECT no_such_column FROM brands", interfaces.PagingModeSingle, nil)

		assertHTTPError(t, err, http.StatusBadRequest)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
		assert.Equal(t, "query references an unknown column", httpErr.ErrorDetails)
	})

	t.Run("uses generic detail for other source query parameter failures", func(t *testing.T) {
		catalog := rawQuerySQLServerCatalog()
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteRawSQL(gomock.Any(), "SELECT id FROM brands").
			Return(nil, fmt.Errorf("execute query failed: %w",
				interfaces.NewSourceQueryInvalidParameterError("type_mismatch", errors.New("driver type mismatch details"))))

		result, err := svc.executeSQL(context.Background(), catalog,
			"SELECT id FROM brands", interfaces.PagingModeSingle, nil)

		assert.Nil(t, result)
		assertHTTPError(t, err, http.StatusBadRequest)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
		assert.Equal(t, "invalid source query parameter", httpErr.ErrorDetails)
	})

	t.Run("preserves internal errors for other database failures", func(t *testing.T) {
		catalog := rawQuerySQLServerCatalog()
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().ExecuteRawSQL(gomock.Any(), "SELECT id FROM brands").
			Return(nil, errors.New("database connection lost"))

		_, err := svc.executeSQL(context.Background(), catalog,
			"SELECT id FROM brands", interfaces.PagingModeSingle, nil)

		assertHTTPError(t, err, http.StatusInternalServerError)
	})
}

func TestRawQueryServiceExecuteSQLCursorPage(t *testing.T) {
	t.Run("cursor page requests one lookahead row", func(t *testing.T) {
		catalog := rawQuerySQLServerCatalog()
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })
		session, err := rawQueryCursorSessions.create("account-1", catalog.ID, []string{"resource-1"},
			"SELECT id FROM dbo.orders", 2, 60, 0)
		require.NoError(t, err)
		session.PageOffset = 4

		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		connector.EXPECT().Close(gomock.Any()).Return(nil)
		connector.EXPECT().Connect(gomock.Any()).Return(nil)
		connector.EXPECT().BuildPagedSQL("SELECT id FROM dbo.orders", 4, 3).Return("TSQL CURSOR PAGE")
		connector.EXPECT().ExecuteRawSQL(gomock.Any(), "TSQL CURSOR PAGE").Return(&interfaces.RawQueryResponse{
			Entries: []map[string]any{{"id": 5}, {"id": 6}, {"id": 7}},
		}, nil)

		result, err := svc.executeSQLCursorPage(context.Background(), session, catalog, nil)

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 5}, {"id": 6}}, result.Entries)
		require.NotNil(t, result.Paging)
		assert.NotNil(t, result.Paging.NextCursor)
		assert.Equal(t, 6, session.PageOffset)
	})
}

func TestRawQueryServiceExecuteSQLTotalCount(t *testing.T) {
	t.Run("total count is not paged", func(t *testing.T) {
		catalog := rawQuerySQLServerCatalog()
		ctrl := gomock.NewController(t)
		connector := vmock.NewMockTableConnector(ctrl)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), catalog.ConnectorType, catalog.ConnectorCfg).
			Return(connector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		gomock.InOrder(
			connector.EXPECT().Connect(gomock.Any()).Return(nil),
			connector.EXPECT().BuildCountSQL("SELECT id FROM dbo.orders").Return(
				"SELECT COUNT(*) AS _raw_query_total_count FROM (SELECT id FROM dbo.orders) AS _raw_query_total"),
			connector.EXPECT().ExecuteRawSQL(gomock.Any(),
				"SELECT COUNT(*) AS _raw_query_total_count FROM (SELECT id FROM dbo.orders) AS _raw_query_total").
				Return(&interfaces.RawQueryResponse{
					Entries: []map[string]any{{rawQueryTotalCountColumn: int64(42)}},
				}, nil),
			connector.EXPECT().Close(gomock.Any()).Return(nil),
		)

		count, err := svc.executeSQLTotalCount(context.Background(), catalog, "SELECT id FROM dbo.orders")

		require.NoError(t, err)
		assert.Equal(t, int64(42), count)
	})
}

func rawQuerySQLServerCatalog() *interfaces.Catalog {
	return &interfaces.Catalog{
		ID: "catalog-1", Name: "sqlserver-catalog", ConnectorType: interfaces.ConnectorTypeSQLServer,
	}
}

func TestRawQueryServiceExecuteInitialDSLQuery(t *testing.T) {
	t.Run("applies timeout totals and aggregation paging", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		expectIndexConnectorClose(indexConnector)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: mockCS, rs: mockRS}

		mockRS.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).
			Return(nil).Times(4)
		mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").DoAndReturn(
			func(ctx context.Context, _ *sql.Tx, _ string) (*interfaces.Resource, error) {
				_, hasDeadline := ctx.Deadline()
				assert.True(t, hasDeadline)
				return &interfaces.Resource{
					ID:               "resource-1",
					Enabled:          true,
					CatalogID:        "catalog-1",
					SourceIdentifier: "events",
					SchemaDefinition: []*interfaces.Property{{Name: "id"}},
				}, nil
			}).Times(4)
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).DoAndReturn(
			func(ctx context.Context, _ string, _ bool) (*interfaces.Catalog, error) {
				_, hasDeadline := ctx.Deadline()
				assert.True(t, hasDeadline)
				return &interfaces.Catalog{
					ID:            "catalog-1",
					Enabled:       true,
					ConnectorType: interfaces.ConnectorTypeOpenSearch,
				}, nil
			}).Times(4)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil).Times(4)
		svc.cf = connectorFactory
		indexConnector.EXPECT().Connect(gomock.Any()).Return(nil).Times(4)

		callCount := 0
		indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
			DoAndReturn(func(ctx context.Context, _ string, query map[string]any) (*interfaces.RawQueryResponse, error) {
				callCount++
				_, ok := ctx.Deadline()
				assert.True(t, ok)
				switch callCount {
				case 1:
					assert.NotContains(t, query, "track_total_hits")
				case 2:
					assert.Equal(t, true, query["track_total_hits"])
				default:
					assert.Equal(t, 0, query["size"])
					assert.NotContains(t, query, "from")
				}
				if callCount == 3 {
					return &interfaces.RawQueryResponse{Entries: []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}}, nil
				}
				if callCount == 4 {
					return nil, interfaces.NewRawAggregationValidationError("aggs", "exactly one aggregation is required")
				}
				return &interfaces.RawQueryResponse{}, nil
			}).Times(4)

		_, err := svc.executeInitialDSLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:           map[string]any{"resource_id": "resource-1", "track_total_hits": true},
			QueryTimeoutSec: 1,
			Paging:          interfaces.PagingRequest{Limit: 10},
		})
		require.NoError(t, err)

		_, err = svc.executeInitialDSLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:           map[string]any{"resource_id": "resource-1", "track_total_hits": false},
			QueryTimeoutSec: 1,
			NeedTotal:       true,
			Paging:          interfaces.PagingRequest{Limit: 10},
		})
		require.NoError(t, err)

		result, err := svc.executeInitialDSLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query: map[string]any{
				"resource_id": "resource-1",
				"aggs":        map[string]any{"by_status": map[string]any{"terms": map[string]any{"field": "status"}}},
			},
			QueryTimeoutSec: 1,
			Paging:          interfaces.PagingRequest{Limit: 1, Offset: 1},
		})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 2}}, result.Entries)

		_, err = svc.executeInitialDSLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query: map[string]any{
				"resource_id": "resource-1",
				"aggs": map[string]any{
					"by_status": map[string]any{"terms": map[string]any{"field": "status"}},
					"by_type":   map[string]any{"terms": map[string]any{"field": "type"}},
				},
			},
			QueryTimeoutSec: 1,
			Paging:          interfaces.PagingRequest{Limit: 2},
		})
		assertHTTPError(t, err, http.StatusBadRequest)
	})

	t.Run("fails before executing DSL when connector connection fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		expectIndexConnectorClose(indexConnector)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: mockCS, rs: mockRS}
		resource := &interfaces.Resource{
			ID:               "resource-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			SourceIdentifier: "events",
			Status:           interfaces.ResourceStatusActive,
			SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		}
		catalog := &interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeOpenSearch}
		expectRawQueryResource(mockRS, "resource-1", resource)
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(catalog, nil)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil)
		svc.cf = connectorFactory
		indexConnector.EXPECT().Connect(gomock.Any()).Return(errors.New("connection refused"))

		_, err := svc.executeInitialDSLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:  map[string]any{"resource_id": "resource-1"},
			Paging: interfaces.PagingRequest{Limit: 10},
		})

		assertHTTPError(t, err, http.StatusInternalServerError)
	})
}

func TestRawQueryServicePrepareOpenSearchCursorQuery(t *testing.T) {
	t.Run("requires a stable sort", func(t *testing.T) {
		svc := &rawQueryService{}
		_, _, _, _, err := svc.prepareOpenSearchCursorQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:  map[string]any{"resource_id": "resource-1"},
			Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 10},
		})

		assertHTTPError(t, err, http.StatusBadRequest)
		assert.ErrorContains(t, err, "sort is required")
	})

	t.Run("requires query data permission independently from view detail", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: mockRS}
		permissionErr := httperrors.NewHTTPError(context.Background(), http.StatusForbidden, httperrors.PublicError_Forbidden)

		mockRS.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).
			Return(permissionErr)

		_, _, _, _, err := svc.prepareOpenSearchCursorQuery(context.Background(), &interfaces.RawQueryRequest{
			Query: map[string]any{
				"resource_id": "resource-1",
				"sort":        []any{"timestamp"},
			},
			Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 10},
		})

		require.ErrorIs(t, err, permissionErr)
	})

	t.Run("drops client search after and freezes first page paging", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: mockCS, rs: mockRS}
		clientSearchAfter := []any{"client-cursor"}
		requestQuery := map[string]any{
			"resource_id":  "resource-1",
			"sort":         []any{"timestamp"},
			"search_after": clientSearchAfter,
			"size":         999,
		}

		mockRS.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").Return(&interfaces.Resource{
			ID:               "resource-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			SourceIdentifier: "events",
			SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		}, nil)
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
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

func TestRawQueryServiceExecuteInitialOpenSearchCursor(t *testing.T) {
	t.Run("closes cursor when initial page reaches total", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		expectIndexConnectorClose(indexConnector)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: mockCS, rs: mockRS}

		mockRS.EXPECT().CheckResourcePermission(gomock.Any(), "resource-1", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").DoAndReturn(
			func(ctx context.Context, _ *sql.Tx, _ string) (*interfaces.Resource, error) {
				_, hasDeadline := ctx.Deadline()
				assert.True(t, hasDeadline)
				return &interfaces.Resource{
					ID:               "resource-1",
					Enabled:          true,
					CatalogID:        "catalog-1",
					SourceIdentifier: "events",
					SchemaDefinition: []*interfaces.Property{{Name: "id"}},
				}, nil
			})
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).DoAndReturn(
			func(ctx context.Context, _ string, _ bool) (*interfaces.Catalog, error) {
				_, hasDeadline := ctx.Deadline()
				assert.True(t, hasDeadline)
				return &interfaces.Catalog{
					ID:            "catalog-1",
					Enabled:       true,
					ConnectorType: interfaces.ConnectorTypeOpenSearch,
				}, nil
			})

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil)
		svc.cf = connectorFactory
		indexConnector.EXPECT().Connect(gomock.Any()).Return(nil)

		indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, query map[string]any) (*interfaces.RawQueryResponse, error) {
				assert.Equal(t, 90, query["from"])
				return &interfaces.RawQueryResponse{
					Entries:     []map[string]any{{"id": "91"}, {"id": "92"}},
					SearchAfter: []any{"page-92"},
					TotalCount:  int64Pointer(92),
				}, nil
			})

		result, err := svc.executeInitialOpenSearchCursor(context.Background(), &interfaces.RawQueryRequest{
			Query: map[string]any{
				"resource_id": "resource-1",
				"sort":        []any{"timestamp"},
			},
			NeedTotal:       true,
			QueryTimeoutSec: 1,
			Paging:          interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: 90, Limit: 2},
		})

		require.NoError(t, err)
		assert.Nil(t, result.Paging.NextCursor)
		require.NotNil(t, result.TotalCount)
		assert.Equal(t, int64(92), *result.TotalCount)
	})
}

func TestRawQueryServiceExecuteOpenSearchCursorPage(t *testing.T) {
	t.Run("continues and closes", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		indexConnector.EXPECT().Close(gomock.Any()).Return(nil).Times(2)
		indexConnector.EXPECT().Connect(gomock.Any()).Return(nil).Times(2)
		manager := newCursorSessionManager(10, 9)
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = manager
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 2, 60, 0)
		require.NoError(t, err)
		session.QueryFormat = interfaces.QueryFormatDSL
		session.OpenSearchIndex = "events"
		session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "from": 10, "size": 2, "track_total_hits": true}
		session.NeedTotal = true

		callCount := 0
		indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, query map[string]any) (*interfaces.RawQueryResponse, error) {
				callCount++
				if callCount == 1 {
					assert.Equal(t, 10, query["from"])
					assert.NotContains(t, query, "search_after")
					assert.Equal(t, true, query["track_total_hits"])
					return &interfaces.RawQueryResponse{
						Entries:     []map[string]any{{"id": "1"}, {"id": "2"}},
						SearchAfter: []any{"page-1"},
						TotalCount:  int64Pointer(3),
					}, nil
				}
				assert.NotContains(t, query, "from")
				assert.NotContains(t, query, "track_total_hits")
				assert.Equal(t, []any{"page-1"}, query["search_after"])
				return &interfaces.RawQueryResponse{Entries: []map[string]any{{"id": "3"}}, TotalCount: int64Pointer(99)}, nil
			}).Times(2)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil).Times(2)
		svc := &rawQueryService{cf: connectorFactory}
		catalog := &interfaces.Catalog{ID: "catalog-1", ConnectorType: interfaces.ConnectorTypeOpenSearch}
		first, err := svc.executeOpenSearchCursorPage(context.Background(), session, catalog, nil)
		require.NoError(t, err)
		require.NotNil(t, first.Paging)
		require.NotNil(t, first.Paging.NextCursor)
		assert.Equal(t, session.ID, *first.Paging.NextCursor)
		require.NotNil(t, first.TotalCount)
		assert.Equal(t, int64(3), *first.TotalCount)

		last, err := svc.executeOpenSearchCursorPage(context.Background(), session, catalog, nil)
		require.NoError(t, err)
		require.NotNil(t, last.Paging)
		assert.Nil(t, last.Paging.NextCursor)
		assert.Nil(t, last.Paging.ExpiresAtSec)
		require.NotNil(t, last.TotalCount)
		assert.Equal(t, int64(3), *last.TotalCount)
		_, ok := manager.acquire(session.ID)
		assert.False(t, ok)
	})

	t.Run("does not create empty page for exact multiple", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		expectIndexConnectorClose(indexConnector)
		indexConnector.EXPECT().Connect(gomock.Any()).Return(nil)
		manager := newCursorSessionManager(10, 9)
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = manager
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 2, 60, 0)
		require.NoError(t, err)
		session.QueryFormat = interfaces.QueryFormatDSL
		session.OpenSearchIndex = "events"
		session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 2}
		session.NeedTotal = true

		indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).Return(&interfaces.RawQueryResponse{
			Entries:     []map[string]any{{"id": "1"}, {"id": "2"}},
			SearchAfter: []any{"page-2"},
			TotalCount:  int64Pointer(2),
		}, nil)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		result, err := svc.executeOpenSearchCursorPage(context.Background(), session,
			&interfaces.Catalog{ID: "catalog-1", ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)
		require.NoError(t, err)
		assert.Nil(t, result.Paging.NextCursor)
		require.NotNil(t, result.TotalCount)
		assert.Equal(t, int64(2), *result.TotalCount)
		_, ok := manager.acquire(session.ID)
		assert.False(t, ok)
	})

	t.Run("failure does not refresh expiry", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		expectIndexConnectorClose(indexConnector)
		indexConnector.EXPECT().Connect(gomock.Any()).Return(nil)
		manager := newCursorSessionManager(10, 9)
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = manager
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 1, 60, 0)
		require.NoError(t, err)
		session.OpenSearchIndex = "events"
		session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 1}
		expiresAt := time.Now().Add(30 * time.Second).Unix()
		atomic.StoreInt64(&session.ExpiresAtSec, expiresAt)

		indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).Return(nil, errors.New("backend unavailable"))
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		_, err = svc.executeOpenSearchCursorPage(context.Background(), session,
			&interfaces.Catalog{ID: "catalog-1", ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)
		require.Error(t, err)
		assert.Equal(t, expiresAt, atomic.LoadInt64(&session.ExpiresAtSec))
	})

	t.Run("fails before executing when connector connection fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		expectIndexConnectorClose(indexConnector)
		indexConnector.EXPECT().Connect(gomock.Any()).Return(errors.New("connection refused"))
		manager := newCursorSessionManager(10, 9)
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = manager
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 1, 60, 0)
		require.NoError(t, err)
		session.OpenSearchIndex = "events"
		session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 1}
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil)
		svc := &rawQueryService{cf: connectorFactory}

		_, err = svc.executeOpenSearchCursorPage(context.Background(), session,
			&interfaces.Catalog{ID: "catalog-1", ConnectorType: interfaces.ConnectorTypeOpenSearch}, nil)

		assertHTTPError(t, err, http.StatusInternalServerError)
	})
}

func TestRawQueryServiceExecuteSQLCursorContinuation(t *testing.T) {
	t.Run("rejects concurrent continuation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		indexConnector := vmock.NewMockIndexConnector(ctrl)
		expectIndexConnectorClose(indexConnector)
		indexConnector.EXPECT().Connect(gomock.Any()).Return(nil)
		mockCS := vmock.NewMockCatalogService(ctrl)
		mockRS := vmock.NewMockResourceService(ctrl)
		manager := newCursorSessionManager(10, 9)
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = manager
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, "", 1, 60, 0)
		require.NoError(t, err)
		session.QueryFormat = interfaces.QueryFormatDSL
		session.OpenSearchIndex = "events"
		session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 1}
		session.NeedTotal = true

		resource := &interfaces.Resource{
			ID:               "resource-1",
			Enabled:          true,
			CatalogID:        "catalog-1",
			Status:           interfaces.ResourceStatusActive,
			SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		}
		catalog := &interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: interfaces.ConnectorTypeOpenSearch}
		expectRawQueryResources(mockRS, []string{"resource-1"}, resource)
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(catalog, nil)

		started := make(chan struct{})
		finish := make(chan struct{})
		indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, query map[string]any) (*interfaces.RawQueryResponse, error) {
				assert.NotContains(t, query, "search_after")
				close(started)
				<-finish
				return &interfaces.RawQueryResponse{Entries: []map[string]any{}}, nil
			})

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeOpenSearch, gomock.Any()).
			Return(indexConnector, nil)
		svc := &rawQueryService{cs: mockCS, rs: mockRS, cf: connectorFactory}
		ctx := context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "account-1"})
		req := &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Cursor: session.ID}}
		firstErr := make(chan error, 1)
		go func() { _, err := svc.executeSQLCursorContinuation(ctx, req); firstErr <- err }()
		<-started

		_, err = svc.executeSQLCursorContinuation(ctx, req)
		require.Error(t, err)
		close(finish)
		require.NoError(t, <-firstErr)
		_, ok := manager.acquire(session.ID)
		assert.False(t, ok)
	})
}

func TestRawQueryServiceExtractResourceIDs(t *testing.T) {
	t.Run("supports hyphenated IDs", func(t *testing.T) {
		previousPolicy := rawQueryPolicy
		rawQueryPolicy = &recordingPolicy{resourceIDs: []string{"orders-2026", "customer_data"}}
		t.Cleanup(func() { rawQueryPolicy = previousPolicy })

		ids, err := (&rawQueryService{}).extractResourceIDs(context.Background(), "SELECT * FROM {{orders-2026}} JOIN {{.customer_data}} ON true", "postgres")

		require.NoError(t, err)
		assert.Equal(t, []string{"orders-2026", "customer_data"}, ids)
	})
	t.Run("raw query service extract resource ids", func(t *testing.T) {
		svc := &rawQueryService{}
		previousPolicy := rawQueryPolicy
		rawQueryPolicy = &recordingPolicy{resourceIDs: []string{"r1", "r2"}}
		t.Cleanup(func() { rawQueryPolicy = previousPolicy })

		got, err := svc.extractResourceIDs(context.Background(), "select * from {{.r1}} join {{r2}} on x where id in (select id from {{.r1}})", "postgres")

		require.NoError(t, err)
		assert.Equal(t, []string{"r1", "r2"}, got)

		got, err = svc.extractResourceIDs(context.Background(), map[string]any{"query": map[string]any{"match_all": map[string]any{}}}, "postgres")
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestHasOpenSearchAggregation(t *testing.T) {
	tests := []struct {
		name  string
		query map[string]any
		want  bool
	}{
		{name: "aggs", query: map[string]any{"aggs": map[string]any{"by_category": map[string]any{}}}, want: true},
		{name: "aggregations", query: map[string]any{"aggregations": map[string]any{"by_category": map[string]any{}}}, want: true},
		{name: "without aggregation", query: map[string]any{"query": map[string]any{"match_all": map[string]any{}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, hasOpenSearchAggregation(test.query))
		})
	}
}

func TestRawQueryServiceReplaceResourceIDWithSchemaTable(t *testing.T) {
	t.Run("preserves Doris catalog reference", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		reference := "`hive`.`sales.eu`.`o``rders`"
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(&interfaces.Resource{Schema: "sales.eu", SourceIdentifier: reference}, nil)
		got, err := (&rawQueryService{rs: rs}).replaceResourceIDWithSchemaTable(context.Background(), "SELECT * FROM {{r1}}", []string{"r1"}, &interfaces.Catalog{ConnectorType: interfaces.ConnectorTypeDoris}, "doris")
		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM "+reference, got)
	})

	t.Run("preserves already quoted source reference", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		reference := `"public"."orders.part"`
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(&interfaces.Resource{Schema: "public", SourceIdentifier: reference}, nil)
		got, err := (&rawQueryService{rs: rs}).replaceResourceIDWithSchemaTable(context.Background(), "SELECT * FROM {{r1}}", []string{"r1"}, &interfaces.Catalog{ConnectorType: interfaces.ConnectorTypePostgreSQL}, "postgres")
		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM "+reference, got)
	})

	t.Run("raw query service replace resource idwith schema table", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}

		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(&interfaces.Resource{
			ID:               "r1",
			Schema:           "schema",
			SourceIdentifier: `"schema"."table_one"`,
		}, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r2").Return(&interfaces.Resource{
			ID:               "r2",
			Schema:           "schema",
			SourceIdentifier: `"schema"."table_two"`,
		}, nil)

		got, err := svc.replaceResourceIDWithSchemaTable(context.Background(),
			"select * from {{.r1}} join {{r2}} on {{.r1}}.id = {{r2}}.id",
			[]string{"r1", "r2"},
			&interfaces.Catalog{Name: "catalog", ConnectorType: interfaces.ConnectorTypePostgreSQL},
			"postgres",
		)

		require.NoError(t, err)
		assert.Equal(t, `select * from "schema"."table_one" join "schema"."table_two" on "schema"."table_one".id = "schema"."table_two".id`, got)
	})

	t.Run("quotes tsql special identifiers", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(&interfaces.Resource{
			ID:               "r1",
			Schema:           "sales data",
			SourceIdentifier: "[sales data].[Order.Archive]]]",
		}, nil)

		got, err := svc.replaceResourceIDWithSchemaTable(context.Background(),
			"SELECT * FROM {{r1}}",
			[]string{"r1"},
			&interfaces.Catalog{Name: "catalog", ConnectorType: interfaces.ConnectorTypeSQLServer},
			"tsql",
		)

		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM [sales data].[Order.Archive]]]", got)
	})

	t.Run("quotes schema containing dots", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "r1").Return(&interfaces.Resource{
			ID:               "r1",
			Schema:           "sales.archive",
			SourceIdentifier: "[sales.archive].[orders]",
		}, nil)

		got, err := svc.replaceResourceIDWithSchemaTable(context.Background(),
			"SELECT * FROM {{r1}}", []string{"r1"}, &interfaces.Catalog{Name: "catalog", ConnectorType: interfaces.ConnectorTypeSQLServer}, "tsql")

		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM [sales.archive].[orders]", got)
	})
}

func TestRawQueryServiceCheckSameDataSource(t *testing.T) {
	t.Run("raw query service check same data source", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		cs := vmock.NewMockCatalogService(ctrl)
		rs := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{cs: cs, rs: rs}

		resources := []*interfaces.Resource{
			{ID: "r1", CatalogID: "catalog-1", Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
			{ID: "r2", CatalogID: "catalog-1", Enabled: true, Status: interfaces.ResourceStatusDeprecated, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
		}
		expectRawQueryResources(rs, []string{"r1", "r2"}, resources...)
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(&interfaces.Catalog{
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
		rs := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}
		expectRawQueryResources(rs, []string{"r1", "missing"},
			&interfaces.Resource{ID: "r1", CatalogID: "catalog-1", Enabled: true, Status: interfaces.ResourceStatusActive})

		catalog, warnings, err := svc.checkSameDataSource(context.Background(), []string{"r1", "missing"})

		assertHTTPError(t, err, http.StatusNotFound)
		assert.Nil(t, catalog)
		assert.Nil(t, warnings)
	})

	t.Run("rejects multi catalog resources", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		rs := vmock.NewMockResourceService(ctrl)
		svc := &rawQueryService{rs: rs}
		for range 2 {
			expectRawQueryResources(rs, []string{"r1", "r2"},
				&interfaces.Resource{ID: "r1", CatalogID: "catalog-1", Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id"}}},
				&interfaces.Resource{ID: "r2", CatalogID: "catalog-2", Enabled: true, Status: interfaces.ResourceStatusActive, SchemaDefinition: []*interfaces.Property{{Name: "id"}}})
		}

		tests := []struct {
			name     string
			language i18n.Language
			detail   string
		}{
			{name: "English", language: i18n.AmericanEnglish, detail: "Cross-catalog JOIN is not supported yet; Trino or DuckDB support is planned."},
			{name: "Chinese", language: i18n.SimplifiedChinese, detail: "暂不支持多数据源 JOIN，计划使用 Trino/DuckDB 实现。"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				ctx := i18n.WithLanguage(context.Background(), test.language)
				catalog, warnings, err := svc.checkSameDataSource(ctx, []string{"r1", "r2"})

				assertHTTPError(t, err, http.StatusNotImplemented)
				var httpErr *httperrors.HTTPError
				require.ErrorAs(t, err, &httpErr)
				assert.Equal(t, verrors.VegaBackend_Query_MultiCatalogNotSupported, httpErr.ErrorCode)
				assert.Equal(t, test.detail, httpErr.ErrorDetails)
				assert.Nil(t, catalog)
				assert.Nil(t, warnings)
			})
		}
	})
}

func TestEnsureCatalogEnabled(t *testing.T) {
	t.Run("ensure catalog enabled", func(t *testing.T) {
		require.NoError(t, ensureCatalogEnabled(context.Background(), nil))
		require.NoError(t, ensureCatalogEnabled(context.Background(), &interfaces.Catalog{Enabled: true}))

		err := ensureCatalogEnabled(context.Background(), &interfaces.Catalog{Enabled: false})

		assertHTTPError(t, err, http.StatusConflict)
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.ErrorCode)
	})
}

func assertHTTPError(t *testing.T, err error, status int) {
	t.Helper()
	require.Error(t, err)

	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, status, httpErr.HTTPCode)
}

func TestCursorSessionCreateError(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		err     error
		details string
	}{
		{"account", errCursorAccountLimitReached, "per-account cursor session limit reached"},
		{"global", errCursorSessionLimitReached, "global cursor session limit reached"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := cursorSessionCreateError(context.Background(), testCase.err)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusTooManyRequests, httpErr.HTTPCode)
			assert.Equal(t, verrors.VegaBackend_Query_CursorSessionLimitExceeded, httpErr.ErrorCode)
			assert.Equal(t, testCase.details, httpErr.ErrorDetails)
		})
	}
}

func (c *builderConnector) ExecuteSQLBuilder(ctx context.Context, q *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
	return c.run(ctx, q)
}
func (c *builderConnector) OpenSQLBuilder(ctx context.Context, q *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
	return c.open(ctx, q)
}
func (unavailableSQLPolicy) ExtractTableResourceIDs(context.Context, string, string) ([]string, error) {
	return nil, errors.New("SQLGlot must not be called")
}
func (unavailableSQLPolicy) ValidateSQL(context.Context, string, string) error {
	return errors.New("SQLGlot must not be called")
}
func (unavailableSQLPolicy) ValidateDerivedTable(context.Context, string, string) error {
	return errors.New("SQLGlot must not be called")
}
func (unavailableSQLPolicy) ValidateTableReferences(context.Context, string, string, []string) error {
	return errors.New("SQLGlot must not be called")
}

func compileTestContext() context.Context {
	return context.WithValue(context.Background(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "caller", Type: "user"})
}

func builderTestResource() *interfaces.Resource {
	return &interfaces.Resource{ID: "orders", CatalogID: "catalog", Name: "display label", Category: interfaces.ResourceCategoryTable,
		Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 11, SourceIdentifier: "`sales.eu`.`order.items`",
		SchemaDefinition: []*interfaces.Property{
			{Name: "id", Type: interfaces.DataType_Integer, OriginalName: "order.id", OriginalType: "BIGINT"},
			{Name: "created", Type: interfaces.DataType_Datetime, OriginalName: "created at", OriginalType: "TIMESTAMP(6)"},
		}}
}

func mysqlRawServices(t *testing.T) (*rawQueryService, *vmock.MockResourceService, *vmock.MockCatalogService, *vmock.MockConnectorFactory) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rs, cs, cf := vmock.NewMockResourceService(ctrl), vmock.NewMockCatalogService(ctrl), vmock.NewMockConnectorFactory(ctrl)
	oldPolicy := rawQueryPolicy
	rawQueryPolicy = unavailableSQLPolicy{}
	oldSessions := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10, 9)
	t.Cleanup(func() {
		for id := range rawQueryCursorSessions.sessions {
			rawQueryCursorSessions.remove(id)
		}
		rawQueryPolicy = oldPolicy
		rawQueryCursorSessions = oldSessions
	})
	return &rawQueryService{rs: rs, cs: cs, cf: cf}, rs, cs, cf
}
func mysqlRawMetadata(rs *vmock.MockResourceService, cs *vmock.MockCatalogService, resource *interfaces.Resource, individual bool, connectorTypes ...string) {
	connectorType := interfaces.ConnectorTypeMySQL
	if len(connectorTypes) > 0 {
		connectorType = connectorTypes[0]
	}

	expectRawQueryResources(rs, []string{"orders"}, resource)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: connectorType, Enabled: true, UpdateTime: 7}, nil)
	if individual {
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(resource, nil)
	}
}

// nativeSQLConnectorMocks 仅替换数据库打开边界，服务、解析器和连接器均执行生产实现。
func nativeSQLConnectorMocks(t *testing.T, cf *vmock.MockConnectorFactory, connectorType string, count int) []sqlmock.Sqlmock {
	t.Helper()
	dbs := make([]*sql.DB, count)
	mocks := make([]sqlmock.Sqlmock, count)
	for i := range dbs {
		var err error
		dbs[i], mocks[i], err = sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual), sqlmock.MonitorPingsOption(true))
		require.NoError(t, err)
		mocks[i].ExpectPing()
	}
	opened := 0
	patch := gomonkey.ApplyFunc(sql.Open, func(driver, _ string) (*sql.DB, error) {
		require.Equal(t, "mysql", driver)
		require.Less(t, opened, len(dbs), "no extra data query or connection may be opened")
		db := dbs[opened]
		opened++
		return db, nil
	})
	t.Cleanup(func() {
		for id := range rawQueryCursorSessions.sessions {
			rawQueryCursorSessions.remove(id)
		}
		patch.Reset()
		assert.Equal(t, count, opened)
		for i, db := range dbs {
			assert.NoError(t, mocks[i].ExpectationsWereMet())
			_ = db.Close()
		}
	})
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).DoAndReturn(
		func(context.Context, string, interfaces.ConnectorConfig) (interfaces.Connector, error) {
			builder := mysql.NewMySQLConnector()
			if connectorType == interfaces.ConnectorTypeMariaDB {
				builder = mysql.NewMariaDBConnector()
			}
			return builder.New(interfaces.ConnectorConfig{"host": "db.test", "port": 3306, "username": "test", "password": "test-only"})
		}).Times(count)
	return mocks
}

func mysqlRawRead(t *testing.T, cf *vmock.MockConnectorFactory, statement string, args []any, entries []map[string]any, failure error, connectorTypes ...string) {
	t.Helper()
	connectorType := interfaces.ConnectorTypeMySQL
	if len(connectorTypes) > 0 {
		connectorType = connectorTypes[0]
	}

	base := vmock.NewMockTableConnector(gomock.NewController(t))
	base.EXPECT().Close(gomock.Any()).Return(nil)
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).Return(&builderConnector{Connector: base, run: func(ctx context.Context, q *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
		text, bound, err := mysql.ToSql(q)
		require.NoError(t, err)
		assert.Equal(t, statement, text)
		assert.Equal(t, args, bound)
		if failure != nil {
			return nil, failure
		}
		return &interfaces.RawQueryResponse{Entries: entries}, nil
	}}, nil)
}

// mysqlRawCursorRows 只允许一次源端查询，返回真实 sql.Rows；续页再次创建连接器会直接使测试失败。
func mysqlRawCursorRows(t *testing.T, cf *vmock.MockConnectorFactory, statement string, source *sqlmock.Rows, failure error, connectorTypes ...string) <-chan context.Context {
	t.Helper()
	connectorType := interfaces.ConnectorTypeMySQL
	if len(connectorTypes) > 0 {
		connectorType = connectorTypes[0]
	}

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	expected := mock.ExpectQuery(statement)
	if failure != nil {
		expected.WillReturnError(failure)
	} else {
		expected.WillReturnRows(source).RowsWillBeClosed()
	}
	mock.ExpectClose()
	t.Cleanup(func() { assert.NoError(t, mock.ExpectationsWereMet()) })
	base := vmock.NewMockTableConnector(gomock.NewController(t))
	base.EXPECT().Close(gomock.Any()).DoAndReturn(func(context.Context) error { return db.Close() })
	opened := make(chan context.Context, 1)
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).Return(&builderConnector{Connector: base, open: func(ctx context.Context, query *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
		opened <- ctx
		text, args, err := mysql.ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, statement, text)
		assert.Empty(t, args)
		rows, err := db.QueryContext(ctx, text)
		return rows, []interfaces.ColumnInfo{{Name: "id", Type: "integer"}}, err
	}}, nil)
	// 最后注册的清理先执行：检查 mock 前释放尚未读完的测试会话。
	manager := rawQueryCursorSessions
	t.Cleanup(func() {
		for id := range manager.sessions {
			manager.remove(id)
		}
	})
	return opened
}

func TestRawQueryServiceMySQLSQLBuilder(t *testing.T) {
	t.Run("single page and total preserve logical limit and parameters without SQLGlot", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		base := "SELECT `a`.`id` FROM `sales.eu`.`order.items` AS `a` WHERE (`a`.`id` > ?) ORDER BY `a`.`id` ASC LIMIT 5 OFFSET 2"
		mysqlRawRead(t, cf, "SELECT COUNT(*) AS `_raw_query_total_count` FROM ("+base+") AS `_raw_query_total`", []any{json.Number("1")}, []map[string]any{{rawQueryTotalCountColumn: int64(5)}}, nil)
		mysqlRawRead(t, cf, "SELECT `a`.`id` FROM `sales.eu`.`order.items` AS `a` WHERE (`a`.`id` > ?) ORDER BY `a`.`id` ASC LIMIT 2 OFFSET 3", []any{json.Number("1")}, []map[string]any{{"id": 4}, {"id": 5}}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT a.id FROM {{ orders }} a WHERE a.id>1 ORDER BY a.id LIMIT 5 OFFSET 2", NeedTotal: true, Paging: interfaces.PagingRequest{Limit: 2, Offset: 1}})
		require.NoError(t, err)
		require.NotNil(t, result.TotalCount)
		assert.EqualValues(t, 5, *result.TotalCount)
		assert.Equal(t, []string{"orders"}, result.ResourceIDs)
		assert.Len(t, result.Entries, 2)
		require.NotNil(t, result.Paging)
		assert.Nil(t, result.Paging.NextCursor)
	})
	t.Run("self join authorizes once and preserves wildcard and physical field names", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		mysqlRawRead(t, cf, "SELECT `a`.*, `b`.`id` AS `other_id` FROM `sales.eu`.`order.items` AS `a` INNER JOIN `sales.eu`.`order.items` AS `b` ON (`a`.`id` = `b`.`id`) LIMIT 2", nil, []map[string]any{{"id": 1, "other_id": 1}}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT a.*,b.id AS other_id FROM {{orders}} a JOIN {{orders}} b ON a.id=b.id", Paging: interfaces.PagingRequest{Limit: 2}})
		require.NoError(t, err)
		assert.Equal(t, []string{"orders"}, result.ResourceIDs)
		assert.Equal(t, []map[string]any{{"id": 1, "other_id": 1}}, result.Entries)
	})
	t.Run("subquery keeps its own limit through the production preparation path", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		mysqlRawRead(t, cf, "SELECT `q`.`public_id` FROM (SELECT `id` AS `public_id` FROM `sales.eu`.`order.items` LIMIT 1) AS `q` LIMIT 2", nil, []map[string]any{{"public_id": 1}}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT q.public_id FROM (SELECT id AS public_id FROM {{orders}} LIMIT 1) q", Paging: interfaces.PagingRequest{Limit: 2}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"public_id": 1}}, result.Entries)
	})

	t.Run("strings and comments do not become resource bindings", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		mysqlRawRead(t, cf, "SELECT ? AS `label` FROM `sales.eu`.`order.items` WHERE (`id` = ?) LIMIT 2", []any{"{{hidden}}", json.Number("1")}, []map[string]any{{"label": "{{hidden}}"}}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT '{{hidden}}' AS label FROM {{orders}} WHERE id=1 /* {{ignored}} */", Paging: interfaces.PagingRequest{Limit: 2}})
		require.NoError(t, err)
		assert.Nil(t, result.TotalCount)
		assert.Equal(t, []string{"orders"}, result.ResourceIDs)
	})
	t.Run("cursor retains one Rows and counts once with bounded lookahead", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawRead(t, cf, "SELECT COUNT(*) AS `_raw_query_total_count` FROM (SELECT `id` FROM `sales.eu`.`order.items` ORDER BY `id` ASC LIMIT 5) AS `_raw_query_total`", nil, []map[string]any{{rawQueryTotalCountColumn: int64(5)}}, nil)
		openedContexts := mysqlRawCursorRows(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` ORDER BY `id` ASC LIMIT 5", sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3).AddRow(4).AddRow(5), nil)
		firstCtx, cancelFirst := context.WithCancel(compileTestContext())
		defer cancelFirst()
		result, err := svc.Execute(firstCtx, &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}} ORDER BY id LIMIT 5", NeedTotal: true, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
		require.NoError(t, err)
		require.NotNil(t, result.Paging.NextCursor)
		token := *result.Paging.NextCursor
		cancelFirst()
		opened := <-openedContexts
		assert.NoError(t, opened.Err())
		assert.Equal(t, []map[string]any{{"id": int64(1)}, {"id": int64(2)}}, result.Entries)
		session, ok := rawQueryCursorSessions.acquire(token)
		require.True(t, ok)
		assert.Empty(t, session.CompiledSQL)
		retainedRows := session.SQLRows
		require.NotNil(t, retainedRows)
		assert.Equal(t, map[string]any{"id": int64(3)}, session.SQLPendingRow)
		require.NotNil(t, session.SQLQuery)
		assert.EqualValues(t, 5, *session.SQLQuery.LimitPart)
		assert.Zero(t, session.SQLQuery.OffsetPart)
		rawQueryCursorSessions.release(session)
		mysqlRawMetadata(rs, cs, resource, true)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Len(t, result.Entries, 2)
		assert.Equal(t, token, *result.Paging.NextCursor)
		mysqlRawMetadata(rs, cs, resource, true)
		assert.Same(t, retainedRows, session.SQLRows)
		assert.Equal(t, []map[string]any{{"id": int64(3)}, {"id": int64(4)}}, result.Entries)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Nil(t, result.Paging.NextCursor)
		require.NotNil(t, result.TotalCount)
		assert.EqualValues(t, 5, *result.TotalCount)
		assert.Equal(t, []map[string]any{{"id": int64(5)}}, result.Entries)
		assert.EqualValues(t, 5, *session.SQLQuery.LimitPart)
		assert.Zero(t, session.SQLQuery.OffsetPart)
		_, ok = rawQueryCursorSessions.acquire(token)
		assert.False(t, ok)
	})
	for _, sql := range []string{"SELECT SLEEP(1) FROM {{orders}}", "SELECT custom_udf(id) FROM {{orders}}", "SELECT * FROM {{orders}}; DELETE FROM {{orders}}", "SELECT * FROM {{orders}} JOIN hidden_table ON 1=1", "SELECT * FROM {{orders}} FOR UPDATE", "WITH t AS (SELECT * FROM {{orders}}) SELECT * FROM t"} {
		t.Run("reject before connector: "+sql, func(t *testing.T) {
			svc, rs, cs, _ := mysqlRawServices(t)
			mysqlRawMetadata(rs, cs, builderTestResource(), false)
			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: sql})
			assert.Nil(t, result)
			assertHTTPError(t, err, http.StatusBadRequest)
		})
	}
	t.Run("view expansion is not bypassed", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.Category = interfaces.ResourceCategoryLogicalView
		mysqlRawMetadata(rs, cs, resource, true)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT * FROM {{orders}}"})
		assert.Nil(t, result)
		assertHTTPError(t, err, http.StatusBadRequest)
	})
	t.Run("permission denial stops before reading metadata", func(t *testing.T) {
		svc, rs, _, _ := mysqlRawServices(t)
		denial := httperrors.NewHTTPError(compileTestContext(), http.StatusForbidden, httperrors.PublicError_Forbidden)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denial)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT * FROM {{orders}}"})
		assert.Nil(t, result)
		assert.ErrorIs(t, err, denial)
	})
	t.Run("initial cursor execution failure releases session", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		mysqlRawCursorRows(t, cf, "SELECT `id` FROM `sales.eu`.`order.items`", nil, errors.New("private source error"))
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
		assert.Nil(t, result)
		assertHTTPError(t, err, http.StatusInternalServerError)
		assert.NotContains(t, err.Error(), "private source error")
		assert.Empty(t, rawQueryCursorSessions.sessions)
	})
}

func TestRawQueryServiceValidateSQLBuilderCursor(t *testing.T) {
	for _, change := range []string{"resource version", "source identifier", "catalog version", "connector type", "permission"} {
		t.Run(change, func(t *testing.T) {
			svc, rs, cs, cf := mysqlRawServices(t)
			resource := builderTestResource()
			mysqlRawMetadata(rs, cs, resource, true)
			mysqlRawCursorRows(t, cf, "SELECT `id` FROM `sales.eu`.`order.items`", sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3), nil)
			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
			require.NoError(t, err)
			token := *result.Paging.NextCursor
			session, ok := rawQueryCursorSessions.acquire(token)
			require.True(t, ok)
			rawQueryCursorSessions.release(session)
			if change == "permission" {
				rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(httperrors.NewHTTPError(compileTestContext(), http.StatusForbidden, httperrors.PublicError_Forbidden))
			} else {
				if change == "resource version" {
					resource.UpdateTime++
				}
				if change == "source identifier" {
					resource.SourceIdentifier = "`other`"
				}
				expectRawQueryResources(rs, []string{"orders"}, resource)
				catalog := &interfaces.Catalog{ID: "catalog", ConnectorType: interfaces.ConnectorTypeMySQL, Enabled: true, UpdateTime: 7}
				if change == "catalog version" {
					catalog.UpdateTime++
				}
				if change == "connector type" {
					catalog.ConnectorType = interfaces.ConnectorTypePostgreSQL
				}
				cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(catalog, nil)
				if change == "resource version" || change == "source identifier" {
					rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(resource, nil)
				}
			}
			result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
			assert.Nil(t, result)
			code := http.StatusConflict
			if change == "permission" {
				code = http.StatusForbidden
			}
			assertHTTPError(t, err, code)
			assert.Equal(t, 2, session.PageOffset)
			assert.NotContains(t, rawQueryCursorSessions.sessions, token)
			assert.Error(t, session.SQLRows.Scan(new(any)))
		})
	}
}

func TestRawQueryServiceExecuteSQLBuilder(t *testing.T) {
	t.Run("invalid windows fail before connector creation", func(t *testing.T) {
		svc, _, _, _ := mysqlRawServices(t)
		query := sb.Select(&sb.Param{Value: 1})
		for _, options := range []*rawSQLBuildOptions{{offset: -1, limit: 2}, {offset: 0, limit: -1}} {
			_, err := svc.executeSQLBuilder(compileTestContext(), &interfaces.Catalog{}, query, options)
			assertHTTPError(t, err, http.StatusBadRequest)
		}
		session := &interfaces.CursorSession{SQLQuery: query, PageOffset: math.MaxInt, PageLimit: 2}
		_, err := svc.executeSQLCursorPage(compileTestContext(), session, &interfaces.Catalog{}, nil)
		assertHTTPError(t, err, http.StatusBadRequest)
		assert.Equal(t, math.MaxInt, session.PageOffset)
	})

	t.Run("API offset beyond logical limit reads an empty window", func(t *testing.T) {
		svc, _, _, cf := mysqlRawServices(t)
		mysqlRawRead(t, cf, "SELECT `id` FROM `orders` ORDER BY `id` ASC LIMIT 0 OFFSET 7", nil, []map[string]any{}, nil)
		query := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"}).OrderBy(sb.Order{Expr: &sb.Column{Name: "id"}}).Limit(5).Offset(2)
		_, err := svc.executeSQLBuilder(compileTestContext(), &interfaces.Catalog{ConnectorType: interfaces.ConnectorTypeMySQL}, query, &rawSQLBuildOptions{offset: 5, limit: 2})
		require.NoError(t, err)
		assert.EqualValues(t, 5, *query.LimitPart)
		assert.EqualValues(t, 2, query.OffsetPart)
	})
	t.Run("Rows read failure terminates cursor without returning a partial page", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawCursorRows(t, cf, "SELECT `id` FROM `sales.eu`.`order.items`", sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3).AddRow(4).RowError(3, errors.New("private read failure")), nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
		require.NoError(t, err)
		token := *result.Paging.NextCursor
		mysqlRawMetadata(rs, cs, resource, true)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		assert.Nil(t, result)
		assertHTTPError(t, err, http.StatusInternalServerError)
		assert.NotContains(t, err.Error(), "private read failure")
		assert.NotContains(t, rawQueryCursorSessions.sessions, token)
		_, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		assertHTTPError(t, err, http.StatusNotFound)
	})
}

func TestRawQueryServiceOpenSQLBuilderCursor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		apiOffset int
		expected  string
		values    []any
	}{
		{"logical offset and API offset apply only when opening", 1, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 4 OFFSET 3", []any{int64(4), int64(5), int64(6)}},
		{"API offset beyond logical limit", 5, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 0 OFFSET 7", nil},
		{"empty result", 0, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 5 OFFSET 2", nil},
		{"exact page boundary closes without an empty continuation", 0, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 5 OFFSET 2", []any{int64(1), int64(2)}},
		{"bytes and null retain raw query representation", 0, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 5 OFFSET 2", []any{[]byte("value"), nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, rs, cs, cf := mysqlRawServices(t)
			resource := builderTestResource()
			mysqlRawMetadata(rs, cs, resource, true)
			source := sqlmock.NewRows([]string{"id"})
			for _, value := range tc.values {
				source.AddRow(value)
			}
			openedContexts := mysqlRawCursorRows(t, cf, tc.expected, source, nil)
			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}} LIMIT 5 OFFSET 2", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2, Offset: tc.apiOffset}})
			require.NoError(t, err)
			opened := <-openedContexts
			assert.Len(t, result.Entries, min(2, len(tc.values)))
			if len(tc.values) > 2 {
				token := *result.Paging.NextCursor
				mysqlRawMetadata(rs, cs, resource, true)
				result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
				require.NoError(t, err)
				assert.Equal(t, []map[string]any{{"id": int64(6)}}, result.Entries)
			}
			assert.Nil(t, result.Paging.NextCursor)
			assert.Empty(t, rawQueryCursorSessions.sessions)
			assert.ErrorIs(t, opened.Err(), context.Canceled)
			if tc.name == "bytes and null retain raw query representation" {
				assert.Equal(t, []map[string]any{{"id": "value"}, {"id": nil}}, result.Entries)
			}
		})
	}
	t.Run("active initial request cancellation terminates open and releases connector", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		entered := make(chan context.Context, 1)
		base := vmock.NewMockTableConnector(gomock.NewController(t))
		base.EXPECT().Close(gomock.Any()).Return(nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeMySQL, gomock.Any()).Return(&builderConnector{Connector: base, open: func(ctx context.Context, _ *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
			entered <- ctx
			<-ctx.Done()
			return nil, nil, ctx.Err()
		}}, nil)
		requestCtx, cancel := context.WithCancel(compileTestContext())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := svc.Execute(requestCtx, &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
			done <- err
		}()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("open did not start")
		}
		cancel()
		select {
		case err := <-done:
			assertHTTPError(t, err, http.StatusInternalServerError)
		case <-time.After(5 * time.Second):
			t.Fatal("request cancellation did not interrupt open")
		}
		assert.Empty(t, rawQueryCursorSessions.sessions)
	})
}

func TestRawQueryServiceSQLRowsCursorContinuation(t *testing.T) {
	t.Run("foreign owner does not close Rows and transient metadata failure can retry", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true)
		openedContexts := mysqlRawCursorRows(t, cf, "SELECT `id` FROM `sales.eu`.`order.items`", sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3), nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
		require.NoError(t, err)
		token := *result.Paging.NextCursor
		opened := <-openedContexts
		foreign := context.WithValue(compileTestContext(), interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: "other-account"})
		_, err = svc.Execute(foreign, &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		assertHTTPError(t, err, http.StatusForbidden)
		assert.NoError(t, opened.Err())
		failure := httperrors.NewHTTPError(compileTestContext(), http.StatusInternalServerError, httperrors.PublicError_InternalServerError)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(failure)
		_, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		assert.ErrorIs(t, err, failure)
		assert.NoError(t, opened.Err())
		assert.Equal(t, map[string]any{"id": int64(3)}, rawQueryCursorSessions.sessions[token].SQLPendingRow)
		mysqlRawMetadata(rs, cs, resource, true)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": int64(3)}}, result.Entries)
		assert.Nil(t, result.Paging.NextCursor)
	})
}

func TestRawQueryServiceMariaDBSQLBuilder(t *testing.T) {
	t.Run("MariaDB uses the native builder for the mariadb input alias", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true, interfaces.ConnectorTypeMariaDB)
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 2", nil, []map[string]any{{"id": 1}}, nil, interfaces.ConnectorTypeMariaDB)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, InputDialect: "mariadb", Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Limit: 2}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 1}}, result.Entries)
	})
	t.Run("MariaDB cursor retains Rows on the same native path", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true, interfaces.ConnectorTypeMariaDB)
		mysqlRawCursorRows(t, cf, "SELECT `id` FROM `sales.eu`.`order.items`", sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2), nil, interfaces.ConnectorTypeMariaDB)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}})
		require.NoError(t, err)
		token := *result.Paging.NextCursor
		mysqlRawMetadata(rs, cs, resource, true, interfaces.ConnectorTypeMariaDB)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": int64(2)}}, result.Entries)
		assert.Nil(t, result.Paging.NextCursor)
	})
}
