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
	"math"
	"net/http"
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
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table"
	mysql "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/postgresql"
	resourcelogic "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/resource"
)

type builderConnector struct {
	interfaces.TableConnector
	run  func(context.Context, *sb.SelectBuilder) (*interfaces.RawQueryResponse, error)
	open func(context.Context, *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error)
	scan func(*sql.Rows, []interfaces.ColumnInfo) (map[string]any, error)
}

type sqlTestPreparer struct {
	interfaces.LogicalViewService
	prepare func(context.Context, *sb.SelectBuilder, []*interfaces.Resource) (*interfaces.SQLViewQuery, error)
}

type sqlTestCursorValidator struct {
	validate func(context.Context, []*interfaces.Resource) error
}

type sqlExtensionCursorTest struct {
	svc               *rawQueryService
	rs                *vmock.MockResourceService
	cs                *vmock.MockCatalogService
	cf                *vmock.MockConnectorFactory
	virtual, physical *interfaces.Resource
	catalog           *interfaces.Catalog
	token             string
	session           *interfaces.CursorSession
	opened            context.Context
	validatorCalls    int
	validatorErr      error
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

func TestRawQueryServiceExecute(t *testing.T) {
	for _, connectorType := range []string{"mysql", "mariadb"} {
		t.Run(connectorType+" actual connector", func(t *testing.T) {
			for _, phase := range []string{"open", "count", "rows"} {
				for _, mode := range []interfaces.PagingMode{interfaces.PagingModeSingle, interfaces.PagingModeCursor} {
					t.Run("UNION collation "+phase+" "+string(mode)+" returns 400 and releases resources", func(t *testing.T) {
						svc, rs, cs, cf := mysqlRawServices(t)
						resource := builderTestResource()
						resource.SchemaDefinition = append(resource.SchemaDefinition, &interfaces.Property{
							Name: "label", OriginalName: "label", OriginalType: "VARCHAR(32)", Type: interfaces.DataType_String,
						})
						mysqlRawMetadata(rs, cs, resource, true, connectorType)
						mocks := nativeSQLConnectorMocks(t, cf, connectorType, 1)
						cause := &mysqldriver.MySQLError{Number: 1271, Message: "private UNION collations and source columns"}
						statement := "SELECT * FROM (SELECT `label` FROM `sales.eu`.`order.items` UNION SELECT `label` FROM `sales.eu`.`order.items`) AS `_union_result`"
						if phase == "count" {
							statement = "SELECT COUNT(*) AS `_raw_query_total_count` FROM (" + statement + ") AS `_raw_query_total`"
						} else if mode == interfaces.PagingModeSingle {
							statement += " LIMIT 1"
						}
						if phase == "rows" {
							failedRow := 0
							if mode == interfaces.PagingModeCursor {
								failedRow = 2 // 首页读取一行并预读一行，续页遇到源错误。
							}
							mocks[0].ExpectQuery(statement).WithoutArgs().WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
								sqlmock.NewColumn("label").OfType("VARCHAR", ""),
							).AddRow("first").AddRow("pending").AddRow("failed").RowError(failedRow, cause)).RowsWillBeClosed()
						} else {
							mocks[0].ExpectQuery(statement).WithoutArgs().WillReturnError(cause)
						}
						mocks[0].ExpectClose()
						result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{
							QueryFormat: interfaces.QueryFormatSQL,
							Query:       "SELECT label FROM {{orders}} UNION SELECT label FROM {{orders}}",
							NeedTotal:   phase == "count", Paging: interfaces.PagingRequest{Mode: mode, Limit: 1},
						})
						if phase == "rows" && mode == interfaces.PagingModeCursor {
							require.NoError(t, err)
							require.NotNil(t, result.Paging.NextCursor)
							assert.Equal(t, []map[string]any{{"label": "first"}}, result.Entries)
							token := *result.Paging.NextCursor
							session := rawQueryCursorSessions.sessions[token]
							mysqlRawMetadata(rs, cs, resource, true, connectorType)
							rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
							result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
							assert.Error(t, session.SQLRows.Scan(new(any)))
						}
						assert.Nil(t, result)
						assertHTTPError(t, err, http.StatusBadRequest)
						var httpErr *httperrors.HTTPError
						require.ErrorAs(t, err, &httpErr)
						assert.Equal(t, verrors.VegaBackend_Query_InvalidParameter, httpErr.ErrorCode)
						assert.Equal(t, "invalid source query parameter", httpErr.ErrorDetails)
						assert.NotContains(t, err.Error(), "private")
						assert.Empty(t, rawQueryCursorSessions.sessions)
					})
				}
			}
			for _, mode := range []interfaces.PagingMode{interfaces.PagingModeSingle, interfaces.PagingModeCursor} {
				t.Run(string(mode)+" preserves binary bytes and text through JSON encoding", func(t *testing.T) {
					svc, rs, cs, cf := mysqlRawServices(t)
					resource := builderTestResource()
					mysqlRawMetadata(rs, cs, resource, true, connectorType)
					mocks := nativeSQLConnectorMocks(t, cf, connectorType, 1)
					statement := "SELECT `bits`, `payload`, `label`, `flag` FROM `sales.eu`.`order.items` ORDER BY `id` ASC"
					limit := 2
					if mode == interfaces.PagingModeSingle {
						statement += " LIMIT 3"
						limit = 3
					}
					mocks[0].ExpectQuery(statement).WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
						sqlmock.NewColumn("bits").OfType("BIT", []byte{}),
						sqlmock.NewColumn("payload").OfType("VARBINARY", []byte{}),
						sqlmock.NewColumn("label").OfType("TEXT", ""),
						sqlmock.NewColumn("flag").OfType("TINYINT", int64(0)),
					).AddRow([]byte{0xff, 0}, []byte{0, 0xff}, []byte("text"), int64(127)).
						AddRow([]byte{}, []byte{}, []byte{}, int64(0)).AddRow(nil, nil, nil, nil)).RowsWillBeClosed()
					mocks[0].ExpectClose()
					result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{
						QueryFormat: interfaces.QueryFormatSQL,
						Query:       "SELECT bits, payload, label, flag FROM {{orders}} ORDER BY id",
						Paging:      interfaces.PagingRequest{Mode: mode, Limit: limit},
					})
					require.NoError(t, err)
					assert.Equal(t, []interfaces.ColumnInfo{{Name: "bits", Type: interfaces.DataType_Binary}, {Name: "payload", Type: interfaces.DataType_Binary}, {Name: "label", Type: interfaces.DataType_Text}, {Name: "flag", Type: interfaces.DataType_Integer}}, result.Columns)
					entries := result.Entries
					if mode == interfaces.PagingModeCursor {
						require.Len(t, entries, 2)
						require.NotNil(t, result.Paging.NextCursor)
						mysqlRawMetadata(rs, cs, resource, true, connectorType)
						result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: *result.Paging.NextCursor}})
						require.NoError(t, err)
						assert.Nil(t, result.Paging.NextCursor)
						entries = append(entries, result.Entries...)
					}
					require.Len(t, entries, 3)
					assert.Equal(t, []byte{0xff, 0}, entries[0]["bits"])
					assert.Equal(t, []byte{0, 0xff}, entries[0]["payload"])
					wire, err := json.Marshal(entries)
					require.NoError(t, err)
					assert.JSONEq(t, `[{"bits":"/wA=","payload":"AP8=","label":"text","flag":127},{"bits":"","payload":"","label":"","flag":0},{"bits":null,"payload":null,"label":null,"flag":null}]`, string(wire))
					assert.Empty(t, rawQueryCursorSessions.sessions)
				})
			}
			t.Run("single page preserves exact literals and counts the logical query", func(t *testing.T) {
				svc, rs, cs, cf := mysqlRawServices(t)
				mysqlRawMetadata(rs, cs, builderTestResource(), true, connectorType)
				mocks := nativeSQLConnectorMocks(t, cf, connectorType, 2)
				base := "SELECT `o`.`id`, CAST(9007199254740993.125 AS DECIMAL(19,3)) AS `amount`, NULL AS `optional` FROM `sales.eu`.`order.items` AS `o` WHERE (`o`.`id` > 1) ORDER BY `o`.`id` ASC"
				mocks[0].ExpectQuery("SELECT COUNT(*) AS `_raw_query_total_count` FROM (" + base + " LIMIT 5 OFFSET 2) AS `_raw_query_total`").
					WithoutArgs().
					WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn(rawQueryTotalCountColumn).OfType("BIGINT", int64(0))).AddRow(5)).RowsWillBeClosed()
				mocks[0].ExpectClose()
				mocks[1].ExpectQuery(base + " LIMIT 2 OFFSET 3").WithoutArgs().
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
				mocks[0].ExpectQuery("SELECT `id`, CAST(9007199254740993.125 AS DECIMAL(19,3)) AS `amount` FROM `sales.eu`.`order.items` WHERE (`id` > 0) ORDER BY `id` ASC LIMIT 3 OFFSET 2").
					WithoutArgs().
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
		service := &rawQueryService{cs: mockCS, rs: mockRS}

		expectRawQueryResource(mockRS, "resource-1", &interfaces.Resource{
			ID: "resource-1", CatalogID: "catalog-1", Enabled: true, SchemaDefinition: []*interfaces.Property{{Name: "id"}},
		})
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).
			Return(&interfaces.Catalog{ID: "catalog-1", Enabled: false, ConnectorType: "opensearch"}, nil)

		_, err := service.Execute(context.Background(), &interfaces.RawQueryRequest{
			Query:       map[string]any{"resource_id": "resource-1"},
			QueryFormat: interfaces.QueryFormatDSL,
		})
		assertCatalogDisabledError(t, err)
	})
}

func assertCatalogDisabledError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var httpErr *httperrors.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
	assert.Equal(t, verrors.VegaBackend_Catalog_IsDisabled, httpErr.ErrorCode)
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
			name: "SQL contract",
			req: &interfaces.RawQueryRequest{
				Query:       "select * from {{r1}}",
				QueryFormat: interfaces.QueryFormatSQL,
			},
		},
		{
			name: "new opensearch DSL contract",
			req: &interfaces.RawQueryRequest{
				Query:       map[string]any{"resource_id": "r1"},
				QueryFormat: interfaces.QueryFormatDSL,
			},
		},
	}

	for _, tt := range successTests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, svc.validateRequest(context.Background(), tt.req))
		})
	}
}

func TestRawQueryServicePrepareSQLQuery(t *testing.T) {
	t.Run("UNION walks all branches and binds repeated resource instances once", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		orders := builderTestResource()
		archive := builderTestResource()
		archive.ID, archive.SourceIdentifier, archive.UpdateTime = "archive", "`archive`", 19
		expectRawQueryResources(rs, []string{"orders", "archive"}, orders, archive)
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: "mysql", Enabled: true}, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(orders, nil)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "archive").Return(archive, nil)

		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query: "SELECT a.id FROM {{orders}} a UNION ALL SELECT b.id FROM {{archive}} b UNION SELECT c.id FROM {{orders}} c"})

		require.NoError(t, err)
		assert.Equal(t, []string{"orders", "archive"}, prepared.resourceIDs)
		assert.Equal(t, map[string]int64{"orders": 11, "archive": 19}, prepared.resourceVersions)
		tables := sqlBuilderTables(prepared.builder)
		require.Len(t, tables, 3)
		assert.Equal(t, []string{"orders", "archive", "orders"}, []string{tables[0].ResourceID, tables[1].ResourceID, tables[2].ResourceID})
		assert.Equal(t, []string{"a", "b", "c"}, []string{tables[0].Alias, tables[1].Alias, tables[2].Alias})
		assert.NotSame(t, tables[0], tables[2])
		assert.Equal(t, orders.SourceIdentifier, tables[0].SourceIdentifier)
		assert.Equal(t, archive.SourceIdentifier, tables[1].SourceIdentifier)
		assert.Equal(t, orders.SourceIdentifier, tables[2].SourceIdentifier)
		assert.Equal(t, tables[0].ColumnOriginalTypes, tables[2].ColumnOriginalTypes)
	})
	t.Run("UNION later branch permission denial stops before metadata and connector calls", func(t *testing.T) {
		svc, rs, _, _ := mysqlRawServices(t)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		denial := httperrors.NewHTTPError(compileTestContext(), http.StatusForbidden, httperrors.PublicError_Forbidden)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "archive", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denial)

		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query: "SELECT id FROM {{orders}} UNION ALL SELECT id FROM {{archive}}"})

		assert.Nil(t, prepared)
		assert.ErrorIs(t, err, denial)
	})
	for _, failure := range []string{"different Catalog", "disabled", "missing"} {
		t.Run("UNION rejects later branch "+failure+" before connector creation", func(t *testing.T) {
			svc, rs, _, _ := mysqlRawServices(t)
			orders := builderTestResource()
			archive := builderTestResource()
			archive.ID = "archive"
			resources := []*interfaces.Resource{orders, archive}
			status := http.StatusConflict
			switch failure {
			case "different Catalog":
				archive.CatalogID, status = "other-catalog", http.StatusNotImplemented
			case "disabled":
				archive.Enabled = false
			case "missing":
				resources, status = resources[:1], http.StatusNotFound
			}
			expectRawQueryResources(rs, []string{"orders", "archive"}, resources...)

			prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
				Query: "SELECT id FROM {{orders}} UNION SELECT id FROM {{archive}}"})

			assert.Nil(t, prepared)
			assertHTTPError(t, err, status)
		})
	}
	for _, language := range []struct {
		name   string
		locale i18n.Language
		detail string
	}{
		{"English", i18n.AmericanEnglish, "Cross-catalog queries are not supported."},
		{"Chinese", i18n.SimplifiedChinese, "暂不支持跨 Catalog 查询。"},
	} {
		t.Run("UNION cross-Catalog rejection uses query-wide "+language.name+" diagnostics", func(t *testing.T) {
			svc, rs, _, _ := mysqlRawServices(t)
			orders, archive := builderTestResource(), builderTestResource()
			archive.ID, archive.CatalogID = "archive", "other-catalog"
			expectRawQueryResources(rs, []string{"orders", "archive"}, orders, archive)

			prepared, err := svc.prepareSQLQuery(i18n.WithLanguage(compileTestContext(), language.locale), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
				Query: "SELECT id FROM {{orders}} UNION SELECT id FROM {{archive}}"})

			assert.Nil(t, prepared)
			assertHTTPError(t, err, http.StatusNotImplemented)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, verrors.VegaBackend_Query_MultiCatalogNotSupported, httpErr.ErrorCode)
			require.NoError(t, httpErr.Render())
			assert.Equal(t, language.detail, httpErr.Description)
			assert.Equal(t, language.detail, httpErr.ErrorDetails)
		})
	}
	t.Run("money projections preserve implicit physical table qualifiers", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.Name = "Display label"
		resource.SourceIdentifier = `"public"."payments"`
		resource.SourceMetadata = map[string]any{"name": "payments"}
		resource.SchemaDefinition = []*interfaces.Property{{Name: "amount", OriginalName: "source_amount", OriginalType: "money", Type: interfaces.DataType_Decimal}, {Name: "id", OriginalType: "int4", Type: interfaces.DataType_Integer}}
		mysqlRawMetadata(rs, cs, resource, true, "postgresql")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT payments.* FROM {{orders}} WHERE payments.source_amount > 1 ORDER BY payments.source_amount", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		statement, args, err := postgresql.ToSql(prepared.builder)
		require.NoError(t, err)
		assert.Equal(t, `SELECT "payments".* FROM (SELECT CAST("source_amount" AS NUMERIC) AS "source_amount", "id" FROM "public"."payments") AS "payments" WHERE ("payments"."source_amount" > CAST(1 AS BIGINT)) ORDER BY "payments"."source_amount" ASC NULLS FIRST`, statement)
		assert.Empty(t, args)
		assert.Equal(t, "Display label", resource.Name)
		assert.Equal(t, "payments", sqlBuilderTables(prepared.builder)[0].SourceName)
	})

	t.Run("PostgreSQL receives source types for lossless money projection", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = `"public"."payments"`
		resource.SourceMetadata = map[string]any{"name": "payments"}
		resource.SchemaDefinition = []*interfaces.Property{{Name: "amount", OriginalName: "source_amount", OriginalType: "money", Type: interfaces.DataType_Decimal}, {Name: "id", OriginalType: "int4", Type: interfaces.DataType_Integer}}
		mysqlRawMetadata(rs, cs, resource, true, "postgresql")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT * FROM {{orders}}", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		tables := sqlBuilderTables(prepared.builder)
		require.Len(t, tables, 1)
		assert.Equal(t, []string{"source_amount", "id"}, tables[0].ColumnNames)
		assert.Equal(t, map[string]string{"source_amount": "money", "id": "int4"}, tables[0].ColumnOriginalTypes)
		statement, _, err := postgresql.ToSql(prepared.builder)
		require.NoError(t, err)
		assert.Equal(t, `SELECT * FROM (SELECT CAST("source_amount" AS NUMERIC) AS "source_amount", "id" FROM "public"."payments") AS "payments"`, statement)
		assert.Equal(t, "amount", resource.SchemaDefinition[0].Name)
	})

	t.Run("joined group authorizes and binds every nested source", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		ids := []string{"orders", "members", "projects"}
		resources := make([]*interfaces.Resource, 0, len(ids))
		for _, id := range ids {
			resource := builderTestResource()
			resource.ID, resource.SourceIdentifier = id, "`"+id+"`"
			resources = append(resources, resource)
			rs.EXPECT().InternalGetByID(gomock.Any(), nil, id).Return(resource, nil)
		}
		expectRawQueryResources(rs, ids, resources...)
		cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: "mysql", Enabled: true}, nil)
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{
			Query: "SELECT a.id FROM {{orders}} a LEFT JOIN ({{members}} m JOIN ({{projects}} p) ON m.id=p.id) ON a.id=m.id", QueryFormat: interfaces.QueryFormatSQL,
		})
		require.NoError(t, err)
		tables := sqlBuilderTables(prepared.builder)
		require.Len(t, tables, 3)
		for i, table := range tables {
			assert.Equal(t, ids[i], table.ResourceID)
			assert.Equal(t, resources[i].SourceIdentifier, table.SourceIdentifier)
			assert.NotNil(t, table.ColumnNames)
		}
		assert.Len(t, prepared.resourceVersions, 3)
	})
	t.Run("joined group permission denial stops before metadata", func(t *testing.T) {
		svc, rs, _, _ := mysqlRawServices(t)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		denial := httperrors.NewHTTPError(compileTestContext(), http.StatusForbidden, httperrors.PublicError_Forbidden)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "members", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denial)
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{
			Query: "SELECT a.id FROM {{orders}} a LEFT JOIN ({{members}} m JOIN {{orders}} b ON m.id=b.id) ON a.id=m.id", QueryFormat: interfaces.QueryFormatSQL,
		})
		assert.Nil(t, prepared)
		assertHTTPError(t, err, http.StatusForbidden)
	})
	t.Run("SQL Server receives physical source column names without rewriting query fields", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SchemaDefinition = []*interfaces.Property{{Name: "amount", OriginalName: "AMOUNT"}, {Name: "id"}}
		mysqlRawMetadata(rs, cs, resource, true, "sqlserver")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{
			Query: "SELECT ROUND(amount,0) AS amount,COUNT(*) AS n FROM {{orders}} GROUP BY amount", QueryFormat: interfaces.QueryFormatSQL,
		})
		require.NoError(t, err)
		tables := sqlBuilderTables(prepared.builder)
		require.Len(t, tables, 1)
		assert.Equal(t, []string{"AMOUNT", "id"}, tables[0].ColumnNames)
		assert.Equal(t, "amount", prepared.builder.GroupByPart[0].(*sb.Column).Name)
		assert.Equal(t, "amount", resource.SchemaDefinition[0].Name)
	})
	for _, connectorType := range []string{"mysql", "mariadb", "postgresql"} {
		t.Run(connectorType+" composite key statistics use the native parser and builder", func(t *testing.T) {
			svc, rs, cs, _ := mysqlRawServices(t)
			resource := builderTestResource()
			mysqlRawMetadata(rs, cs, resource, true, connectorType)
			input := "SELECT COUNT(*) AS row_count, COUNT(CASE WHEN `a` IS NOT NULL AND `b` IS NOT NULL THEN 1 END) AS keyed_rows, COUNT(DISTINCT CASE WHEN `a` IS NOT NULL AND `b` IS NOT NULL THEN CONCAT_WS(CHAR(31), `a`, `b`) END) AS primary_key_distinct FROM {{orders}}"
			prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: input, QueryFormat: interfaces.QueryFormatSQL})
			require.NoError(t, err)
			require.NotNil(t, prepared.builder)
			var text string
			var args []any
			if connectorType == "postgresql" {
				text, args, err = postgresql.ToSql(prepared.builder)
			} else {
				text, args, err = mysql.ToSql(prepared.builder)
			}
			require.NoError(t, err)
			assert.Contains(t, text, "CONCAT_WS(")
			assert.Contains(t, text, "COUNT(DISTINCT CASE WHEN")
			assert.Empty(t, args)
			if connectorType == "postgresql" {
				assert.Contains(t, text, "CAST(31 AS BIGINT)")
			} else {
				assert.Contains(t, text, "CHAR(31)")
			}
		})
	}
	t.Run("Doris prepares MySQL SQL with Builder", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = "`ext.catalog`.`sales.eu`.`order.items`"
		mysqlRawMetadata(rs, cs, resource, true, "doris")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT a.id FROM {{ orders }} a WHERE a.id>1 ORDER BY a.id LIMIT 3", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		require.NotNil(t, prepared.builder)
		tables := sqlBuilderTables(prepared.builder)
		require.Len(t, tables, 1)
		assert.Equal(t, resource.SourceIdentifier, tables[0].SourceIdentifier)
	})
	t.Run("Doris parse failures stop before resource lookup", func(t *testing.T) {
		svc, _, _, _ := mysqlRawServices(t)

		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "WITH r AS (SELECT * FROM {{orders}}) SELECT * FROM r", QueryFormat: interfaces.QueryFormatSQL})
		assert.Nil(t, prepared)
		assertHTTPError(t, err, http.StatusBadRequest)
	})
	t.Run("Oracle prepares MySQL SQL with Builder", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = `"sales.eu"."order.items"`
		mysqlRawMetadata(rs, cs, resource, true, "oracle")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT a.id FROM {{ orders }} a WHERE a.id>1 ORDER BY a.id LIMIT 3", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		require.NotNil(t, prepared.builder)
		tables := sqlBuilderTables(prepared.builder)
		require.Len(t, tables, 1)
		assert.Equal(t, resource.SourceIdentifier, tables[0].SourceIdentifier)
	})
	t.Run("Oracle parse failures stop before resource lookup", func(t *testing.T) {
		svc, _, _, _ := mysqlRawServices(t)

		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "WITH r AS (SELECT * FROM {{orders}}) SELECT * FROM r", QueryFormat: interfaces.QueryFormatSQL})
		assert.Nil(t, prepared)
		assertHTTPError(t, err, http.StatusBadRequest)
	})
	t.Run("HANA prepares MySQL SQL and preserves source identifiers", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		source := builderTestResource()
		source.SourceIdentifier = `"SALES.EU"."order.items"`
		mysqlRawMetadata(rs, cs, source, true, "hana")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT a.id FROM {{ orders }} a WHERE a.id>1 ORDER BY a.id LIMIT 3", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		require.NotNil(t, prepared.builder)
		tables := sqlBuilderTables(prepared.builder)
		require.Len(t, tables, 1)
		assert.Equal(t, source.SourceIdentifier, tables[0].SourceIdentifier)
		assert.Equal(t, "a", tables[0].Alias)
	})
	t.Run("HANA parse failures stop before resource lookup", func(t *testing.T) {
		svc, _, _, _ := mysqlRawServices(t)

		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "WITH r AS (SELECT * FROM {{orders}}) SELECT * FROM r", QueryFormat: interfaces.QueryFormatSQL})
		assert.Nil(t, prepared)
		assertHTTPError(t, err, http.StatusBadRequest)
	})

	t.Run("star grouping positions are left to the source without schema expansion", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = `"public"."orders"`
		mysqlRawMetadata(rs, cs, resource, true, "postgresql")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT o.*,COUNT(*) AS n FROM {{orders}} o GROUP BY 1,2", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		text, args, err := postgresql.ToSql(prepared.builder)
		require.NoError(t, err)
		assert.Equal(t, `SELECT "o".*, COUNT(*) AS "n" FROM "public"."orders" AS "o" GROUP BY 1, 2`, text)
		assert.Empty(t, args)
	})

	t.Run("PG binds spaced MySQL placeholders with Builder", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = `"public"."orders"`
		mysqlRawMetadata(rs, cs, resource, true, "postgresql")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT '{{ orders }}' AS label FROM {{ \torders\r\n }} a JOIN {{orders}} b ON a.id=b.id /* {{orders}} */", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		require.NotNil(t, prepared.builder)
		text, args, err := postgresql.ToSql(prepared.builder)
		require.NoError(t, err)
		assert.Equal(t, `SELECT '{{ orders }}' AS "label" FROM "public"."orders" AS "a" INNER JOIN "public"."orders" AS "b" ON ("a"."id" = "b"."id")`, text)
		assert.Empty(t, args)
		assert.Equal(t, []string{"orders"}, prepared.resourceIDs)
	})
	t.Run("PG invalid input stops before resource lookup", func(t *testing.T) {
		svc, _, _, _ := mysqlRawServices(t)

		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "WITH r AS (SELECT * FROM {{ orders }}) SELECT * FROM r", QueryFormat: interfaces.QueryFormatSQL})
		assert.Nil(t, prepared)
		assertHTTPError(t, err, http.StatusBadRequest)
	})

	t.Run("SQL Server prepares Unicode MySQL SQL with Builder", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = "[Sales.eu].[ord]]ers]"
		mysqlRawMetadata(rs, cs, resource, true, "sqlserver")
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT id FROM {{orders}} WHERE id=1 AND name='刘🙂备' ORDER BY id LIMIT 3", QueryFormat: interfaces.QueryFormatSQL})
		require.NoError(t, err)
		require.NotNil(t, prepared.builder)
		assert.Equal(t, "[Sales.eu].[ord]]ers]", prepared.builder.FromPart.(*sb.Table).SourceIdentifier)
	})

	for _, sql := range []string{
		"SELECT * FROM public.orders /* {{resource-1}} */",
		"SELECT * FROM public.orders -- {{resource-1}}\n",
		"SELECT '{{resource-1}}' FROM public.orders",
	} {
		t.Run("rejects placeholder outside table reference: "+sql, func(t *testing.T) {
			svc := &rawQueryService{}

			_, err := svc.prepareSQLQuery(context.Background(), &interfaces.RawQueryRequest{
				Query:       sql,
				QueryFormat: interfaces.QueryFormatSQL,
			})
			assertHTTPError(t, err, http.StatusBadRequest)
			var httpErr *httperrors.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Contains(t, httpErr.ErrorDetails, "invalid SQL syntax")
		})
	}
	t.Run("SQL Server parse failures stop before resource lookup", func(t *testing.T) {
		svc, _, _, _ := mysqlRawServices(t)

		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT TOP (3) id FROM {{orders}}", QueryFormat: interfaces.QueryFormatSQL})
		assert.Nil(t, prepared)
		assertHTTPError(t, err, http.StatusBadRequest)
	})
}

func TestSQLBuilderTables(t *testing.T) {
	first := &sb.Table{ResourceID: "orders", Alias: "a"}
	repeated := &sb.Table{ResourceID: "orders", Alias: "b"}
	archive := &sb.Table{ResourceID: "archive"}
	joined := &sb.Table{ResourceID: "customers"}
	query := sb.UnionQuery(
		sb.Select(&sb.Star{}).From(&sb.JoinedTable{From: first, Joins: []sb.Join{{Source: joined}}}),
		sb.Select(&sb.Star{}).FromSelect(sb.UnionQuery(sb.Select(&sb.Star{}).From(archive), sb.Select(&sb.Star{}).From(repeated), true), "nested"), false)

	assert.Equal(t, []*sb.Table{first, joined, archive, repeated}, sqlBuilderTables(query))
	assert.Nil(t, sqlBuilderTables(&sb.Literal{Value: "not a source"}))
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
			ID: "catalog-1", Enabled: true, ConnectorType: "sqlserver",
		}, nil)

		sawDeadline := false
		mockRS.EXPECT().InternalGetByID(gomock.Any(), nil, "resource-1").DoAndReturn(func(ctx context.Context, _ *sql.Tx, _ string) (*interfaces.Resource, error) {
			deadline, ok := ctx.Deadline()
			remaining := time.Until(deadline)
			sawDeadline = ok && remaining > 0 && remaining <= time.Second
			return nil, errors.New("metadata unavailable")
		})

		_, err := svc.executeInitialSQLQuery(context.Background(), &interfaces.RawQueryRequest{
			Query:           "SELECT * FROM {{resource-1}}",
			QueryFormat:     interfaces.QueryFormatSQL,
			QueryTimeoutSec: 1,
			Paging:          interfaces.PagingRequest{Limit: 1},
		})
		require.Error(t, err)
		assert.True(t, sawDeadline)
	})
}

func TestValidateCursorResourceBinding(t *testing.T) {
	t.Run("validates resource identity and update time", func(t *testing.T) {
		previousManager := rawQueryCursorSessions
		rawQueryCursorSessions = newCursorSessionManager(10, 9)
		t.Cleanup(func() { rawQueryCursorSessions = previousManager })
		rawSession, err := rawQueryCursorSessions.create("account-1", "catalog-1", nil, 1, 60, 60)
		require.NoError(t, err)
		err = validateCursorResourceBinding(context.Background(), rawSession, &interfaces.RawQueryRequest{ResourceDataResourceID: "logic-1"})
		var httpErr *httperrors.HTTPError
		require.ErrorAs(t, err, &httpErr)
		assert.Equal(t, http.StatusConflict, httpErr.HTTPCode)
		require.NoError(t, validateCursorResourceBinding(context.Background(), rawSession, &interfaces.RawQueryRequest{}))

		session, err := rawQueryCursorSessions.create("account-1", "catalog-1", nil, 1, 60, 60)
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
					ConnectorType: "opensearch",
				}, nil
			}).Times(4)

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
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
		catalog := &interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: "opensearch"}
		expectRawQueryResource(mockRS, "resource-1", resource)
		mockCS.EXPECT().InternalGetByID(gomock.Any(), "catalog-1", true).Return(catalog, nil)
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
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
			ConnectorType: "opensearch",
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
					ConnectorType: "opensearch",
				}, nil
			})

		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
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

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, 2, 60, 0)
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
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
			Return(indexConnector, nil).Times(2)
		svc := &rawQueryService{cf: connectorFactory}
		catalog := &interfaces.Catalog{ID: "catalog-1", ConnectorType: "opensearch"}
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

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, 2, 60, 0)
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
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
			Return(indexConnector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		result, err := svc.executeOpenSearchCursorPage(context.Background(), session,
			&interfaces.Catalog{ID: "catalog-1", ConnectorType: "opensearch"}, nil)
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

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, 1, 60, 0)
		require.NoError(t, err)
		session.OpenSearchIndex = "events"
		session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 1}
		expiresAt := time.Now().Add(30 * time.Second).Unix()
		atomic.StoreInt64(&session.ExpiresAtSec, expiresAt)

		indexConnector.EXPECT().ExecuteRawQuery(gomock.Any(), "events", gomock.Any()).Return(nil, errors.New("backend unavailable"))
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
			Return(indexConnector, nil)
		svc := &rawQueryService{cf: connectorFactory}
		_, err = svc.executeOpenSearchCursorPage(context.Background(), session,
			&interfaces.Catalog{ID: "catalog-1", ConnectorType: "opensearch"}, nil)
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

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, 1, 60, 0)
		require.NoError(t, err)
		session.OpenSearchIndex = "events"
		session.OpenSearchQuery = map[string]any{"sort": []any{"timestamp"}, "size": 1}
		connectorFactory := vmock.NewMockConnectorFactory(ctrl)
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
			Return(indexConnector, nil)
		svc := &rawQueryService{cf: connectorFactory}

		_, err = svc.executeOpenSearchCursorPage(context.Background(), session,
			&interfaces.Catalog{ID: "catalog-1", ConnectorType: "opensearch"}, nil)

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

		session, err := manager.create("account-1", "catalog-1", []string{"resource-1"}, 1, 60, 0)
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
		catalog := &interfaces.Catalog{ID: "catalog-1", Enabled: true, ConnectorType: "opensearch"}
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
		connectorFactory.EXPECT().CreateConnectorInstance(gomock.Any(), "opensearch", gomock.Any()).
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
			{name: "English", language: i18n.AmericanEnglish, detail: "Cross-catalog queries are not supported."},
			{name: "Chinese", language: i18n.SimplifiedChinese, detail: "暂不支持跨 Catalog 查询。"},
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
func (c *builderConnector) ScanSQLBuilderRow(rows *sql.Rows, columns []interfaces.ColumnInfo) (map[string]any, error) {
	if c.scan != nil {
		return c.scan(rows, columns)
	}
	return table.ScanSQLRow(rows, columns)
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
	oldSessions := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10, 9)
	t.Cleanup(func() {
		for id := range rawQueryCursorSessions.sessions {
			rawQueryCursorSessions.remove(id)
		}
		rawQueryCursorSessions = oldSessions
	})
	return &rawQueryService{rs: rs, cs: cs, cf: cf}, rs, cs, cf
}
func mysqlRawMetadata(rs *vmock.MockResourceService, cs *vmock.MockCatalogService, resource *interfaces.Resource, individual bool, connectorTypes ...string) {
	connectorType := "mysql"
	if len(connectorTypes) > 0 {
		connectorType = connectorTypes[0]
	}

	expectRawQueryResources(rs, []string{"orders"}, resource)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: connectorType, Enabled: true, UpdateTime: 7}, nil)
	if individual {
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(resource, nil)
	}
}

func unionRawMetadata(rs *vmock.MockResourceService, cs *vmock.MockCatalogService, resources ...*interfaces.Resource) {
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		ids = append(ids, resource.ID)
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, resource.ID).Return(resource, nil)
	}
	expectRawQueryResources(rs, ids, resources...)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: "mysql", Enabled: true, UpdateTime: 7}, nil)
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
		if connectorType == "postgresql" {
			mocks[i].ExpectQuery(`SELECT 1 FROM (SELECT ARRAY[1] AS items) AS base JOIN LATERAL generate_subscripts(base.items, 1) AS probe(position) ON true`).WillReturnRows(sqlmock.NewRows([]string{"probe"}).AddRow(1))
			mocks[i].ExpectQuery(`SELECT ordinal_position FROM (SELECT ARRAY[1] AS items) AS base JOIN LATERAL unnest(base.items) WITH ORDINALITY AS probe(value, ordinal_position) ON true`).WillReturnRows(sqlmock.NewRows([]string{"probe"}).AddRow(1))
			mocks[i].ExpectQuery(`SELECT pg_catalog.length(pg_catalog.split_part(CAST(CAST(CAST(0 AS money) AS numeric) AS text), '.', 2))`).WillReturnRows(sqlmock.NewRows([]string{"scale"}).AddRow(2))
		}
	}
	opened := 0
	patch := gomonkey.ApplyFunc(sql.Open, func(driver, _ string) (*sql.DB, error) {
		expectedDriver := "mysql"
		if connectorType == "postgresql" {
			expectedDriver = "postgres"
		}
		require.Equal(t, expectedDriver, driver)
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
			if connectorType == "postgresql" {
				return postgresql.NewPostgresqlConnector().New(interfaces.ConnectorConfig{"host": "db.test", "port": 5432, "username": "test", "password": "test-only", "database": "fixture"})
			}
			builder := mysql.NewMySQLConnector()
			if connectorType == "mariadb" {
				builder = mysql.NewMariaDBConnector()
			}
			return builder.New(interfaces.ConnectorConfig{"host": "db.test", "port": 3306, "username": "test", "password": "test-only"})
		}).Times(count)
	return mocks
}

func mysqlRawRead(t *testing.T, cf *vmock.MockConnectorFactory, statement string, args []any, entries []map[string]any, failure error, connectorTypes ...string) {
	t.Helper()
	connectorType := "mysql"
	if len(connectorTypes) > 0 {
		connectorType = connectorTypes[0]
	}

	base := vmock.NewMockTableConnector(gomock.NewController(t))
	base.EXPECT().Close(gomock.Any()).Return(nil)
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).Return(&builderConnector{TableConnector: base, run: func(ctx context.Context, q *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
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
	connectorType := "mysql"
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
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), connectorType, gomock.Any()).Return(&builderConnector{TableConnector: base, open: func(ctx context.Context, query *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
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
	t.Run("UNION ALL counts the logical result and pages only the compound root", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		orders, archive := builderTestResource(), builderTestResource()
		archive.ID, archive.SourceIdentifier = "archive", "`archive`"
		unionRawMetadata(rs, cs, orders, archive)
		base := "SELECT * FROM (SELECT * FROM (SELECT `id` FROM `sales.eu`.`order.items` ORDER BY `id` ASC LIMIT 2) AS `_union_left` UNION ALL SELECT * FROM (SELECT `id` FROM `archive` LIMIT 3) AS `_union_right`) AS `_union_result` ORDER BY `id` ASC"
		mysqlRawRead(t, cf, "SELECT COUNT(*) AS `_raw_query_total_count` FROM ("+base+" LIMIT 4 OFFSET 1) AS `_raw_query_total`", nil,
			[]map[string]any{{rawQueryTotalCountColumn: int64(4)}}, nil)
		mysqlRawRead(t, cf, base+" LIMIT 2 OFFSET 2", nil, []map[string]any{{"id": 3}, {"id": 3}}, nil)

		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query:     "(SELECT id FROM {{orders}} ORDER BY id LIMIT 2) UNION ALL (SELECT id FROM {{archive}} LIMIT 3) ORDER BY id LIMIT 4 OFFSET 1",
			NeedTotal: true, Paging: interfaces.PagingRequest{Limit: 2, Offset: 1}})

		require.NoError(t, err)
		require.NotNil(t, result.TotalCount)
		assert.EqualValues(t, 4, *result.TotalCount)
		assert.Equal(t, []string{"orders", "archive"}, result.ResourceIDs)
		assert.Equal(t, []map[string]any{{"id": 3}, {"id": 3}}, result.Entries)
		assert.Nil(t, result.Paging.NextCursor)
	})
	t.Run("UNION reuses one resource authorization and renders duplicate elimination", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		mysqlRawRead(t, cf, "SELECT * FROM (SELECT `id` FROM `sales.eu`.`order.items` UNION SELECT `id` FROM `sales.eu`.`order.items`) AS `_union_result` LIMIT 2", nil,
			[]map[string]any{{"id": 1}}, nil)

		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query: "SELECT id FROM {{orders}} UNION DISTINCT SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Limit: 2}})

		require.NoError(t, err)
		assert.Equal(t, []string{"orders"}, result.ResourceIDs)
		assert.Equal(t, []map[string]any{{"id": 1}}, result.Entries)
	})
	for _, window := range []struct {
		name, suffix, expected string
		offset                 int
	}{
		{"zero logical limit", " LIMIT 0", " LIMIT 0", 0},
		{"API offset beyond logical limit", " LIMIT 3 OFFSET 1", " LIMIT 0 OFFSET 5", 4},
	} {
		t.Run("UNION "+window.name+" preserves the empty compound window", func(t *testing.T) {
			svc, rs, cs, cf := mysqlRawServices(t)
			mysqlRawMetadata(rs, cs, builderTestResource(), true)
			base := "SELECT * FROM (SELECT `id` FROM `sales.eu`.`order.items` UNION ALL SELECT `id` FROM `sales.eu`.`order.items`) AS `_union_result`"
			mysqlRawRead(t, cf, base+window.expected, nil, []map[string]any{}, nil)

			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
				Query:  "SELECT id FROM {{orders}} UNION ALL SELECT id FROM {{orders}}" + window.suffix,
				Paging: interfaces.PagingRequest{Limit: 2, Offset: window.offset}})

			require.NoError(t, err)
			assert.Empty(t, result.Entries)
			assert.Nil(t, result.Paging.NextCursor)
		})
	}
	t.Run("UNION cursor retains one Rows over all branches and counts once", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		orders, archive := builderTestResource(), builderTestResource()
		archive.ID, archive.SourceIdentifier, archive.UpdateTime = "archive", "`archive`", 19
		unionRawMetadata(rs, cs, orders, archive)
		statement := "SELECT * FROM (SELECT `id` FROM `sales.eu`.`order.items` UNION ALL SELECT `id` FROM `archive`) AS `_union_result` ORDER BY `id` ASC LIMIT 4 OFFSET 1"
		mysqlRawRead(t, cf, "SELECT COUNT(*) AS `_raw_query_total_count` FROM ("+statement+") AS `_raw_query_total`", nil,
			[]map[string]any{{rawQueryTotalCountColumn: int64(4)}}, nil)
		openedContexts := mysqlRawCursorRows(t, cf, statement,
			sqlmock.NewRows([]string{"id"}).AddRow(2).AddRow(2).AddRow(3).AddRow(4), nil)

		first, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query:     "SELECT id FROM {{orders}} UNION ALL SELECT id FROM {{archive}} ORDER BY id LIMIT 4 OFFSET 1",
			NeedTotal: true, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": int64(2)}, {"id": int64(2)}}, first.Entries)
		require.NotNil(t, first.Paging.NextCursor)
		token := *first.Paging.NextCursor
		session := rawQueryCursorSessions.sessions[token]
		assert.Equal(t, map[string]int64{"orders": 11, "archive": 19}, session.SQLResourceVersions)
		retainedRows := session.SQLRows
		unionRawMetadata(rs, cs, orders, archive)
		second, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})

		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": int64(3)}, {"id": int64(4)}}, second.Entries)
		assert.Nil(t, second.Paging.NextCursor)
		require.NotNil(t, second.TotalCount)
		assert.EqualValues(t, 4, *second.TotalCount)
		assert.Same(t, retainedRows, session.SQLRows)
		assert.EqualValues(t, 4, *session.SQLQuery.LimitPart)
		assert.EqualValues(t, 1, session.SQLQuery.OffsetPart)
		assert.Empty(t, rawQueryCursorSessions.sessions)
		assert.ErrorIs(t, (<-openedContexts).Err(), context.Canceled)
	})
	t.Run("UNION explicit cursor close cancels and releases the complete result", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		statement := "SELECT * FROM (SELECT `id` FROM `sales.eu`.`order.items` UNION ALL SELECT `id` FROM `sales.eu`.`order.items`) AS `_union_result`"
		openedContexts := mysqlRawCursorRows(t, cf, statement, sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3), nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query: "SELECT id FROM {{orders}} UNION ALL SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
		require.NoError(t, err)
		require.NotNil(t, result.Paging.NextCursor)
		token := *result.Paging.NextCursor
		session := rawQueryCursorSessions.sessions[token]

		require.NoError(t, CloseCursorSession(compileTestContext(), "caller", token))

		assert.Empty(t, rawQueryCursorSessions.sessions)
		assert.ErrorIs(t, (<-openedContexts).Err(), context.Canceled)
		assert.Error(t, session.SQLRows.Scan(new(any)))
	})
	for _, cause := range []string{"column_count", "type_mismatch"} {
		t.Run("UNION source "+cause+" failure is sanitized and releases initial cursor", func(t *testing.T) {
			svc, rs, cs, cf := mysqlRawServices(t)
			mysqlRawMetadata(rs, cs, builderTestResource(), true)
			statement := "SELECT * FROM (SELECT * FROM `sales.eu`.`order.items` UNION SELECT `id` FROM `sales.eu`.`order.items`) AS `_union_result`"
			mysqlRawCursorRows(t, cf, statement, nil,
				interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterReason(cause), errors.New("private source schema and column details")))

			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
				Query: "SELECT * FROM {{orders}} UNION SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})

			assert.Nil(t, result)
			assertHTTPError(t, err, http.StatusBadRequest)
			assert.NotContains(t, err.Error(), "private source schema")
			assert.Empty(t, rawQueryCursorSessions.sessions)
		})
	}
	t.Run("single page and total preserve logical limit and constants", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		base := "SELECT `a`.`id` FROM `sales.eu`.`order.items` AS `a` WHERE (`a`.`id` > 1) ORDER BY `a`.`id` ASC LIMIT 5 OFFSET 2"
		mysqlRawRead(t, cf, "SELECT COUNT(*) AS `_raw_query_total_count` FROM ("+base+") AS `_raw_query_total`", nil, []map[string]any{{rawQueryTotalCountColumn: int64(5)}}, nil)
		mysqlRawRead(t, cf, "SELECT `a`.`id` FROM `sales.eu`.`order.items` AS `a` WHERE (`a`.`id` > 1) ORDER BY `a`.`id` ASC LIMIT 2 OFFSET 3", nil, []map[string]any{{"id": 4}, {"id": 5}}, nil)
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
		mysqlRawRead(t, cf, "SELECT '{{hidden}}' AS `label` FROM `sales.eu`.`order.items` WHERE (`id` = 1) LIMIT 2", nil, []map[string]any{{"label": "{{hidden}}"}}, nil)
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
			svc, _, _, _ := mysqlRawServices(t)

			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: sql})
			assert.Nil(t, result)
			assertHTTPError(t, err, http.StatusBadRequest)
		})
	}
	t.Run("unavailable View expansion is not bypassed", func(t *testing.T) {
		svc, rs, _, _ := mysqlRawServices(t)
		resource := builderTestResource()
		resource.Category = interfaces.ResourceCategoryLogicalView
		previous := resourcelogic.GetLogicalViewService()
		resourcelogic.SetLogicalViewService(nil)
		t.Cleanup(func() { resourcelogic.SetLogicalViewService(previous) })
		expectRawQueryResources(rs, []string{resource.ID}, resource)
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
	for _, change := range []string{"permission", "resource version", "source identifier", "disabled", "missing", "Catalog"} {
		t.Run("UNION later branch "+change+" invalidates and releases retained Rows", func(t *testing.T) {
			svc, rs, cs, cf := mysqlRawServices(t)
			orders, archive := builderTestResource(), builderTestResource()
			archive.ID, archive.SourceIdentifier, archive.UpdateTime = "archive", "`archive`", 19
			unionRawMetadata(rs, cs, orders, archive)
			statement := "SELECT * FROM (SELECT `id` FROM `sales.eu`.`order.items` UNION ALL SELECT `id` FROM `archive`) AS `_union_result`"
			openedContexts := mysqlRawCursorRows(t, cf, statement, sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3), nil)
			first, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
				Query: "SELECT id FROM {{orders}} UNION ALL SELECT id FROM {{archive}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
			require.NoError(t, err)
			require.NotNil(t, first.Paging.NextCursor)
			token := *first.Paging.NextCursor
			session := rawQueryCursorSessions.sessions[token]
			opened := <-openedContexts
			status := http.StatusConflict
			switch change {
			case "permission":
				status = http.StatusForbidden
				rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
				rs.EXPECT().CheckResourcePermission(gomock.Any(), "archive", interfaces.OPERATION_TYPE_QUERY_DATA).
					Return(httperrors.NewHTTPError(compileTestContext(), status, httperrors.PublicError_Forbidden))
			case "missing":
				status = http.StatusNotFound
				expectRawQueryResources(rs, []string{"orders", "archive"}, orders)
			case "disabled":
				archive.Enabled = false
				expectRawQueryResources(rs, []string{"orders", "archive"}, orders, archive)
			case "Catalog":
				status = http.StatusNotImplemented
				archive.CatalogID = "other-catalog"
				expectRawQueryResources(rs, []string{"orders", "archive"}, orders, archive)
			default:
				if change == "resource version" {
					archive.UpdateTime++
				} else {
					archive.SourceIdentifier = "`replacement_archive`"
				}
				unionRawMetadata(rs, cs, orders, archive)
			}

			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})

			assert.Nil(t, result)
			assertHTTPError(t, err, status)
			assert.Equal(t, 2, session.PageOffset)
			assert.Empty(t, rawQueryCursorSessions.sessions)
			assert.ErrorIs(t, opened.Err(), context.Canceled)
			assert.Error(t, session.SQLRows.Scan(new(any)))
		})
	}
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
				catalog := &interfaces.Catalog{ID: "catalog", ConnectorType: "mysql", Enabled: true, UpdateTime: 7}
				if change == "catalog version" {
					catalog.UpdateTime++
				}
				if change == "connector type" {
					catalog.ConnectorType = "postgresql"
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
	for _, test := range []struct {
		name    string
		failure error
		status  int
		detail  string
	}{
		{name: "source read forbidden", failure: interfaces.NewSourceReadForbiddenError(errors.New("private source detail")), status: http.StatusForbidden},
		{name: "unknown column", failure: interfaces.NewSourceQueryInvalidParameterError(interfaces.SourceQueryInvalidParameterUnknownColumn, errors.New("private source detail")), status: http.StatusBadRequest, detail: "query references an unknown column"},
		{name: "other source parameter", failure: interfaces.NewSourceQueryInvalidParameterError("type_mismatch", errors.New("private source detail")), status: http.StatusBadRequest, detail: "invalid source query parameter"},
		{name: "source unavailable", failure: errors.New("private source detail"), status: http.StatusInternalServerError, detail: "query execution failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _, _, cf := mysqlRawServices(t)
			connector := vmock.NewMockTableConnector(gomock.NewController(t))
			connector.EXPECT().Close(gomock.Any()).Return(nil)
			query := sb.Select(&sb.Column{Name: "id"})
			executor := &builderConnector{TableConnector: connector, run: func(_ context.Context, got *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
				assert.Same(t, query, got)
				return nil, test.failure
			}}
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "custom-sql", gomock.Any()).Return(executor, nil)
			result, err := svc.executeSQLBuilder(compileTestContext(), &interfaces.Catalog{ConnectorType: "custom-sql"}, query, nil)
			assert.Nil(t, result)
			assertHTTPError(t, err, test.status)
			var failure *httperrors.HTTPError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, test.detail, failure.ErrorDetails)
			assert.NotContains(t, err.Error(), "private source detail")
		})
	}

	t.Run("invalid windows fail before connector creation", func(t *testing.T) {
		svc, _, _, _ := mysqlRawServices(t)
		query := sb.Select(&sb.Param{Value: 1})
		for _, options := range []*rawSQLBuildOptions{{offset: -1, limit: 2}, {offset: 0, limit: -1}} {
			_, err := svc.executeSQLBuilder(compileTestContext(), &interfaces.Catalog{}, query, options)
			assertHTTPError(t, err, http.StatusBadRequest)
		}
		session := &interfaces.CursorSession{SQLQuery: query, PageOffset: math.MaxInt, PageLimit: 2}
		_, err := svc.executeSQLCursorPage(compileTestContext(), session, nil)
		assertHTTPError(t, err, http.StatusBadRequest)
		assert.Equal(t, math.MaxInt, session.PageOffset)
	})

	t.Run("API offset beyond logical limit reads an empty window", func(t *testing.T) {
		svc, _, _, cf := mysqlRawServices(t)
		mysqlRawRead(t, cf, "SELECT `id` FROM `orders` ORDER BY `id` ASC LIMIT 0 OFFSET 7", nil, []map[string]any{}, nil)
		query := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"}).OrderBy(sb.Order{Expr: &sb.Column{Name: "id"}}).Limit(5).Offset(2)
		_, err := svc.executeSQLBuilder(compileTestContext(), &interfaces.Catalog{ConnectorType: "mysql"}, query, &rawSQLBuildOptions{offset: 5, limit: 2})
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
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(&builderConnector{TableConnector: base, open: func(ctx context.Context, _ *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
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
	t.Run("connector row conversion preserves decimal and NULL across pages", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true)
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		mock.ExpectQuery("SELECT amount").WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow("12.5000").AddRow(nil)).RowsWillBeClosed()
		rows, err := db.Query("SELECT amount")
		require.NoError(t, err)
		calls := 0
		base := vmock.NewMockTableConnector(gomock.NewController(t))
		base.EXPECT().Close(gomock.Any()).Return(nil)
		opened := 0
		connector := &builderConnector{TableConnector: base, open: func(context.Context, *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
			opened++
			return rows, []interfaces.ColumnInfo{{Name: "amount", Type: interfaces.DataType_Decimal}}, nil
		}, scan: func(rows *sql.Rows, columns []interfaces.ColumnInfo) (map[string]any, error) {
			calls++
			if !rows.Next() {
				return nil, rows.Err()
			}
			var value any
			if err := rows.Scan(&value); err != nil {
				return nil, err
			}
			if value != nil {
				value = json.Number(value.(string))
			}
			return map[string]any{columns[0].Name: value}, nil
		}}
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(connector, nil)
		first, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT amount FROM {{orders}}", QueryFormat: interfaces.QueryFormatSQL, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"amount": json.Number("12.5000")}}, first.Entries)
		require.NotNil(t, first.Paging.NextCursor)
		mysqlRawMetadata(rs, cs, resource, true)
		second, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: *first.Paging.NextCursor}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"amount": nil}}, second.Entries)
		assert.Nil(t, second.Paging.NextCursor)
		assert.Empty(t, rawQueryCursorSessions.sessions)
		assert.Equal(t, 1, opened)
		assert.Equal(t, 3, calls)
		require.NoError(t, rows.Err())
		require.NoError(t, mock.ExpectationsWereMet())
	})

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
		mysqlRawMetadata(rs, cs, builderTestResource(), true, "mariadb")
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 2", nil, []map[string]any{{"id": 1}}, nil, "mariadb")
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Limit: 2}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 1}}, result.Entries)
	})
	t.Run("MariaDB cursor retains Rows on the same native path", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true, "mariadb")
		mysqlRawCursorRows(t, cf, "SELECT `id` FROM `sales.eu`.`order.items`", sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2), nil, "mariadb")
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}})
		require.NoError(t, err)
		token := *result.Paging.NextCursor
		mysqlRawMetadata(rs, cs, resource, true, "mariadb")
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": int64(2)}}, result.Entries)
		assert.Nil(t, result.Paging.NextCursor)
	})
}

// TestRawQueryServicePostgreSQLSQLBuilder 覆盖生产 parser、PG 连接器、COUNT 及保留 Rows 的服务闭环。
func TestRawQueryServicePostgreSQLSQLBuilder(t *testing.T) {
	t.Run("mysql input generates PG constants and independent logical count", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = `"public"."orders"`
		mysqlRawMetadata(rs, cs, resource, true, "postgresql")
		mocks := nativeSQLConnectorMocks(t, cf, "postgresql", 2)
		base := `SELECT "id" FROM "public"."orders" WHERE ("id" > CAST(1 AS BIGINT)) ORDER BY "id" ASC NULLS FIRST LIMIT 5`
		mocks[0].ExpectQuery(`SELECT COUNT(*) AS "_raw_query_total_count" FROM (` + base + `) AS "_raw_query_total"`).WithoutArgs().WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn(rawQueryTotalCountColumn).OfType("INT8", int64(0))).AddRow(5)).RowsWillBeClosed()
		mocks[0].ExpectClose()
		mocks[1].ExpectQuery(`SELECT "id" FROM "public"."orders" WHERE ("id" > CAST(1 AS BIGINT)) ORDER BY "id" ASC NULLS FIRST LIMIT 2 OFFSET 1`).WithoutArgs().WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("id").OfType("INT8", int64(0))).AddRow(3).AddRow(4)).RowsWillBeClosed()
		mocks[1].ExpectClose()
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}} WHERE id>1 ORDER BY id LIMIT 5", NeedTotal: true, Paging: interfaces.PagingRequest{Offset: 1, Limit: 2}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": int64(3)}, {"id": int64(4)}}, result.Entries)
		require.NotNil(t, result.TotalCount)
		assert.Equal(t, int64(5), *result.TotalCount)
	})
	t.Run("PG cursor keeps one Rows across continuation with native binary values", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		resource.SourceIdentifier = `"public"."orders"`
		mysqlRawMetadata(rs, cs, resource, true, "postgresql")
		mocks := nativeSQLConnectorMocks(t, cf, "postgresql", 1)
		mocks[0].ExpectQuery(`SELECT "id", "payload" FROM "public"."orders" ORDER BY "id" ASC NULLS FIRST`).WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("id").OfType("INT8", int64(0)), sqlmock.NewColumn("payload").OfType("BYTEA", []byte{})).AddRow(1, []byte{0, 255}).AddRow(2, []byte{}).AddRow(3, nil)).RowsWillBeClosed()
		mocks[0].ExpectClose()
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id,payload FROM {{orders}} ORDER BY id", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 1}})
		require.NoError(t, err)
		require.NotNil(t, result.Paging.NextCursor)
		assert.Equal(t, []byte{0, 255}, result.Entries[0]["payload"])
		for _, want := range []struct {
			id      int64
			payload any
		}{{2, []byte{}}, {3, nil}} {
			token := *result.Paging.NextCursor
			mysqlRawMetadata(rs, cs, resource, true, "postgresql")
			result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
			require.NoError(t, err)
			require.Len(t, result.Entries, 1)
			assert.Equal(t, want.id, result.Entries[0]["id"])
			assert.Equal(t, want.payload, result.Entries[0]["payload"])
		}
		assert.Nil(t, result.Paging.NextCursor)
	})
}

func TestRawQueryServiceHANAUnlimitedCursor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int
		want   []map[string]any
		next   bool
	}{
		{"push offset then continue", 1, []map[string]any{{"id": int64(2)}}, true},
		{"offset beyond end", 5, []map[string]any{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, rs, cs, cf := mysqlRawServices(t)
			source := builderTestResource()
			source.SourceIdentifier = `"APP"."ORDERS"`
			mysqlRawMetadata(rs, cs, source, true, "hana")
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			resultRows := sqlmock.NewRows([]string{"id"})
			if tc.offset == 1 {
				resultRows.AddRow(int64(2)).AddRow(int64(3))
			}
			mock.ExpectQuery("SELECT retained").WillReturnRows(resultRows).RowsWillBeClosed()
			opened := 0
			connector := vmock.NewMockTableConnector(gomock.NewController(t))
			connector.EXPECT().Close(gomock.Any()).Return(nil).Times(1)
			bc := &builderConnector{TableConnector: connector, open: func(ctx context.Context, q *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
				opened++
				assert.EqualValues(t, tc.offset, q.OffsetPart, "API offset must be passed to the source renderer")
				assert.Nil(t, q.LimitPart, "the service leaves the source-specific sentinel to the renderer")
				rows, err := db.QueryContext(ctx, "SELECT retained")
				return rows, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}}, err
			}}
			cf.EXPECT().CreateConnectorInstance(gomock.Any(), "hana", gomock.Any()).Return(bc, nil)
			result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT id FROM {{orders}} ORDER BY id", QueryFormat: interfaces.QueryFormatSQL, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: tc.offset, Limit: 1}})
			require.NoError(t, err)
			assert.Equal(t, tc.want, result.Entries)
			if tc.next {
				require.NotEmpty(t, result.Paging.NextCursor)
				mysqlRawMetadata(rs, cs, source, true, "hana")
				result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: *result.Paging.NextCursor}})
				require.NoError(t, err)
				assert.Equal(t, []map[string]any{{"id": int64(3)}}, result.Entries)
			}
			assert.Empty(t, result.Paging.NextCursor)
			assert.Equal(t, 1, opened)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestRawQueryServiceDorisSQLBuilder checks that Doris retains one Rows and its metadata across pages.
func TestRawQueryServiceDorisSQLBuilder(t *testing.T) {
	svc, rs, cs, cf := mysqlRawServices(t)
	resource := builderTestResource()
	resource.SourceIdentifier = "`external`.`sales.eu`.`orders`"
	mysqlRawMetadata(rs, cs, resource, true, "doris")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT retained").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2).AddRow(3)).RowsWillBeClosed()
	base := vmock.NewMockTableConnector(gomock.NewController(t))
	base.EXPECT().Close(gomock.Any()).Return(nil)
	opened := 0
	connector := &builderConnector{TableConnector: base, open: func(ctx context.Context, q *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
		opened++
		assert.EqualValues(t, 1, q.OffsetPart)
		assert.Nil(t, q.LimitPart)
		assert.Equal(t, resource.SourceIdentifier, q.FromPart.(*sb.Table).SourceIdentifier)
		rows, err := db.QueryContext(ctx, "SELECT retained")
		return rows, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}}, err
	}}
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), "doris", gomock.Any()).Return(connector, nil)
	first, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT id FROM {{orders}} ORDER BY id", QueryFormat: interfaces.QueryFormatSQL, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: 1, Limit: 1}})
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	assert.Equal(t, []map[string]any{{"id": int64(2)}}, first.Entries)
	mysqlRawMetadata(rs, cs, resource, true, "doris")
	second, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: *first.Paging.NextCursor}})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"id": int64(3)}}, second.Entries)
	assert.Equal(t, first.Columns, second.Columns)
	assert.Nil(t, second.Paging.NextCursor)
	assert.Equal(t, 1, opened)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRawQueryServiceOracleSQLBuilder checks that Oracle retains one Rows and its metadata across pages.
func TestRawQueryServiceOracleSQLBuilder(t *testing.T) {
	svc, rs, cs, cf := mysqlRawServices(t)
	resource := builderTestResource()
	resource.SourceIdentifier = `"APP"."ORDERS"`
	mysqlRawMetadata(rs, cs, resource, true, "oracle")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("SELECT retained").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2).AddRow(3)).RowsWillBeClosed()
	base := vmock.NewMockTableConnector(gomock.NewController(t))
	base.EXPECT().Close(gomock.Any()).Return(nil)
	opened := 0
	connector := &builderConnector{TableConnector: base, open: func(ctx context.Context, q *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
		opened++
		assert.EqualValues(t, 1, q.OffsetPart)
		assert.Nil(t, q.LimitPart)
		assert.Equal(t, resource.SourceIdentifier, q.FromPart.(*sb.Table).SourceIdentifier)
		rows, err := db.QueryContext(ctx, "SELECT retained")
		return rows, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}}, err
	}}
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), "oracle", gomock.Any()).Return(connector, nil)
	first, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Query: "SELECT id FROM {{orders}} ORDER BY id", QueryFormat: interfaces.QueryFormatSQL, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Offset: 1, Limit: 1}})
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	assert.Equal(t, []map[string]any{{"id": int64(2)}}, first.Entries)
	mysqlRawMetadata(rs, cs, resource, true, "oracle")
	second, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: *first.Paging.NextCursor}})
	require.NoError(t, err)
	assert.Equal(t, []map[string]any{{"id": int64(3)}}, second.Entries)
	assert.Equal(t, first.Columns, second.Columns)
	assert.Nil(t, second.Paging.NextCursor)
	assert.Equal(t, 1, opened)
	require.NoError(t, mock.ExpectationsWereMet())
}

func (p *sqlTestPreparer) PrepareSQLQuery(ctx context.Context, query *sb.SelectBuilder, direct []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
	return p.prepare(ctx, query, direct)
}

func (v *sqlTestCursorValidator) ValidateSQLCursor(ctx context.Context, direct []*interfaces.Resource) error {
	if v.validate != nil {
		return v.validate(ctx, direct)
	}
	return nil
}

func registerSQLTestPreparer(t *testing.T, prepare func(context.Context, *sb.SelectBuilder, []*interfaces.Resource) (*interfaces.SQLViewQuery, error)) {
	t.Helper()
	previous := resourcelogic.GetLogicalViewService()
	resourcelogic.SetLogicalViewService(&sqlTestPreparer{prepare: prepare})
	t.Cleanup(func() { resourcelogic.SetLogicalViewService(previous) })
}

func sqlExtensionTestResources() (*interfaces.Resource, *interfaces.Resource) {
	virtual := &interfaces.Resource{ID: "virtual", CatalogID: "logical", Category: interfaces.ResourceCategoryLogicalView,
		Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 21,
		SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer}}}
	physical := builderTestResource()
	physical.ID, physical.SourceIdentifier = "hidden", "`hidden`"
	return virtual, physical
}

// sqlTestPrepare 仅模拟来源改写；业务定义及字段策略由扩展自己的测试验证。
func sqlTestPrepare(source *interfaces.Resource, validator interfaces.SQLQueryCursorValidator) func(context.Context, *sb.SelectBuilder, []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
	return func(_ context.Context, query *sb.SelectBuilder, _ []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
		for _, table := range sqlBuilderTables(query) {
			if table.ResourceID == "virtual" {
				table.ResourceID = source.ID
			}
		}
		return &interfaces.SQLViewQuery{Sources: []*interfaces.Resource{source}, CursorValidator: validator}, nil
	}
}

func TestRawQueryServicePrepareExtendedSQLBuilder(t *testing.T) {
	t.Run("public preparation diagnostics survive the query response", func(t *testing.T) {
		svc, rs, _, _ := mysqlRawServices(t)
		virtual, _ := sqlExtensionTestResources()
		expectRawQueryResources(rs, []string{virtual.ID}, virtual)
		failure := httperrors.NewHTTPError(compileTestContext(), http.StatusBadRequest, verrors.VegaBackend_LogicalView_DefinitionInvalid).
			WithMetadata(map[string]any{"Fields": "customer_id"}).
			WithErrorDetails(map[string]any{"fields": []string{"customer_id"}, "reason": "source_field_mismatch"})
		registerSQLTestPreparer(t, func(context.Context, *sb.SelectBuilder, []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
			return nil, failure
		})
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{
			QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{virtual}}",
		})
		assert.Nil(t, result)
		assertHTTPError(t, err, http.StatusBadRequest)
		var response map[string]any
		require.NoError(t, json.Unmarshal([]byte(err.Error()), &response))
		assert.Equal(t, failure.ErrorCode, response["error_code"])
		assert.Equal(t, map[string]any{"Fields": "customer_id"}, response["metadata"])
		assert.Equal(t, map[string]any{"fields": []any{"customer_id"}, "reason": "source_field_mismatch"}, response["error_details"])
		assert.Contains(t, response["description"], "customer_id")
	})
	for _, tc := range []struct {
		name, input string
		directTable bool
		count       int
	}{
		{"single source", "SELECT id FROM {{virtual}}", false, 1},
		{"repeated sources", "SELECT a.id,b.id FROM {{virtual}} a JOIN {{virtual}} b ON a.id=b.id", false, 2},
		{"joined direct Table", "SELECT v.id,o.id FROM {{virtual}} v JOIN {{orders}} o ON v.id=o.id", true, 2},
		{"UNION direct Table", "SELECT id FROM {{virtual}} UNION ALL SELECT id FROM {{orders}}", true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, rs, cs, _ := mysqlRawServices(t)
			virtual, hidden := sqlExtensionTestResources()
			physical := builderTestResource()
			physical.SourceMetadata = map[string]any{"name": "orders"}
			validator := &sqlTestCursorValidator{}
			ids, resources := []string{virtual.ID}, []*interfaces.Resource{virtual}
			if tc.directTable {
				ids, resources = append(ids, physical.ID), append(resources, physical)
			}
			expectRawQueryResources(rs, ids, resources...)
			cs.EXPECT().InternalGetByID(gomock.Any(), hidden.CatalogID, true).Return(&interfaces.Catalog{
				ID: hidden.CatalogID, Enabled: true, ConnectorType: "mysql",
			}, nil)
			registerSQLTestPreparer(t, func(ctx context.Context, query *sb.SelectBuilder, got []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
				assert.Equal(t, resources, got)
				if tc.directTable {
					for _, table := range sqlBuilderTables(query) {
						if table.ResourceID == physical.ID {
							assert.Equal(t, "orders", table.SourceName)
							assert.Equal(t, []string{"order.id", "created at"}, table.ColumnNames)
							assert.Equal(t, physical.SourceIdentifier, table.SourceIdentifier)
						}
					}
				}
				return sqlTestPrepare(hidden, validator)(ctx, query, got)
			})
			prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: tc.input})
			require.NoError(t, err)
			assert.Equal(t, ids, prepared.resourceIDs)
			assert.Equal(t, hidden.CatalogID, prepared.catalog.ID)
			assert.Same(t, validator, prepared.sourceValidator)
			assert.NotContains(t, prepared.resourceVersions, virtual.ID)
			assert.EqualValues(t, hidden.UpdateTime, prepared.resourceVersions[hidden.ID])
			tables := sqlBuilderTables(prepared.builder)
			require.Len(t, tables, tc.count)
			for _, table := range tables {
				assert.NotEmpty(t, table.SourceIdentifier)
			}
			if tc.name == "repeated sources" {
				assert.NotSame(t, tables[0], tables[1])
				assert.Equal(t, hidden.SourceIdentifier, tables[0].SourceIdentifier)
				assert.Equal(t, hidden.SourceIdentifier, tables[1].SourceIdentifier)
			}
		})
	}
	t.Run("extension can reuse a direct Table without declaring it as a hidden source", func(t *testing.T) {
		svc, rs, cs, _ := mysqlRawServices(t)
		virtual, _ := sqlExtensionTestResources()
		physical := builderTestResource()
		validator := &sqlTestCursorValidator{}
		expectRawQueryResources(rs, []string{virtual.ID, physical.ID}, virtual, physical)
		registerSQLTestPreparer(t, func(ctx context.Context, query *sb.SelectBuilder, direct []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
			prepared, err := sqlTestPrepare(physical, validator)(ctx, query, direct)
			prepared.Sources = nil
			return prepared, err
		})
		cs.EXPECT().InternalGetByID(gomock.Any(), physical.CatalogID, true).Return(&interfaces.Catalog{ID: physical.CatalogID, Enabled: true, ConnectorType: "mysql"}, nil)
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query: "SELECT a.id,b.id FROM {{virtual}} a JOIN {{orders}} b ON a.id=b.id"})
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{physical.ID: physical.UpdateTime}, prepared.resourceVersions)
		assert.Same(t, validator, prepared.sourceValidator)
		assert.Len(t, sqlBuilderTables(prepared.builder), 2)
	})

	t.Run("all direct permissions precede metadata and extension invocation", func(t *testing.T) {
		svc, rs, _, _ := mysqlRawServices(t)
		registerSQLTestPreparer(t, func(context.Context, *sb.SelectBuilder, []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
			t.Fatal("permission denial must stop before the extension")
			return nil, errors.New("unexpected extension invocation")
		})
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "orders", interfaces.OPERATION_TYPE_QUERY_DATA).Return(nil)
		denial := httperrors.NewHTTPError(compileTestContext(), http.StatusForbidden, httperrors.PublicError_Forbidden)
		rs.EXPECT().CheckResourcePermission(gomock.Any(), "virtual", interfaces.OPERATION_TYPE_QUERY_DATA).Return(denial)
		prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
			Query: "SELECT id FROM {{orders}} UNION SELECT id FROM {{virtual}}"})
		assert.Nil(t, prepared)
		assert.Same(t, denial, err)
	})
	for _, invalid := range []string{"nil result", "nil validator", "duplicate source", "undeclared source", "non Table", "disabled source", "built in source", "cross Catalog"} {
		t.Run("reject extension "+invalid, func(t *testing.T) {
			svc, rs, _, _ := mysqlRawServices(t)
			virtual, hidden := sqlExtensionTestResources()
			physical := builderTestResource()
			input := "SELECT id FROM {{virtual}}"
			status := http.StatusBadRequest
			ids, resources := []string{virtual.ID}, []*interfaces.Resource{virtual}
			if invalid == "cross Catalog" {
				hidden.CatalogID = "other"
				input += " UNION SELECT id FROM {{orders}}"
				ids, resources, status = append(ids, physical.ID), append(resources, physical), http.StatusNotImplemented
			}
			expectRawQueryResources(rs, ids, resources...)
			registerSQLTestPreparer(t, func(ctx context.Context, query *sb.SelectBuilder, direct []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
				prepared, err := sqlTestPrepare(hidden, &sqlTestCursorValidator{})(ctx, query, direct)
				switch invalid {
				case "nil result":
					return nil, nil //nolint:nilnil // 模拟扩展违反非空结果契约，验证执行器拒绝。
				case "nil validator":
					prepared.CursorValidator = nil
				case "duplicate source":
					prepared.Sources = append(prepared.Sources, hidden)
				case "undeclared source":
					prepared.Sources = nil
				case "non Table":
					hidden.Category = interfaces.ResourceCategoryIndex
				case "disabled source":
					hidden.Enabled = false
					status = http.StatusConflict
				case "built in source":
					hidden.Builtin = true
					status = http.StatusForbidden
				}
				return prepared, err
			})
			prepared, err := svc.prepareSQLQuery(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: input})
			assert.Nil(t, prepared)
			assertHTTPError(t, err, status)
		})
	}
	t.Run("hidden source warnings are not public response warnings", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		virtual, hidden := sqlExtensionTestResources()
		hidden.Name, hidden.Status = "private physical source", interfaces.ResourceStatusDeprecated
		registerSQLTestPreparer(t, sqlTestPrepare(hidden, &sqlTestCursorValidator{}))
		expectRawQueryResources(rs, []string{virtual.ID}, virtual)
		cs.EXPECT().InternalGetByID(gomock.Any(), hidden.CatalogID, true).Return(&interfaces.Catalog{ID: hidden.CatalogID, Enabled: true, ConnectorType: "mysql"}, nil)
		base := vmock.NewMockTableConnector(gomock.NewController(t))
		base.EXPECT().Close(gomock.Any()).Return(nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(&builderConnector{
			TableConnector: base,
			run: func(context.Context, *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
				return &interfaces.RawQueryResponse{Entries: []map[string]any{{"id": int64(1)}}}, nil
			},
		}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{virtual}}"})
		require.NoError(t, err)
		assert.Equal(t, []string{virtual.ID}, result.ResourceIDs)
		assert.Empty(t, result.Warnings)
	})
}

func newSQLExtensionCursorTest(t *testing.T) *sqlExtensionCursorTest {
	t.Helper()
	state := &sqlExtensionCursorTest{}
	state.svc, state.rs, state.cs, state.cf = mysqlRawServices(t)
	state.virtual, state.physical = sqlExtensionTestResources()
	state.catalog = &interfaces.Catalog{ID: state.physical.CatalogID, Enabled: true, ConnectorType: "mysql", UpdateTime: 7}
	validator := &sqlTestCursorValidator{validate: func(_ context.Context, direct []*interfaces.Resource) error {
		state.validatorCalls++
		require.Len(t, direct, 1)
		assert.Equal(t, state.virtual.ID, direct[0].ID)
		return state.validatorErr
	}}
	registerSQLTestPreparer(t, sqlTestPrepare(state.physical, validator))
	expectRawQueryResources(state.rs, []string{state.virtual.ID}, state.virtual)
	state.cs.EXPECT().InternalGetByID(gomock.Any(), state.catalog.ID, true).Return(state.catalog, nil)
	opened := mysqlRawCursorRows(t, state.cf, "SELECT `id` FROM `hidden`", sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).AddRow(3), nil)
	first, err := state.svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
		Query: "SELECT id FROM {{virtual}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
	require.NoError(t, err)
	require.NotNil(t, first.Paging.NextCursor)
	assert.Equal(t, []string{state.virtual.ID}, first.ResourceIDs)
	assert.Equal(t, 0, state.validatorCalls)
	state.token = *first.Paging.NextCursor
	state.session = rawQueryCursorSessions.sessions[state.token]
	state.opened = <-opened
	return state
}

func assertSQLExtensionCursorReleased(t *testing.T, state *sqlExtensionCursorTest) {
	t.Helper()
	assert.Empty(t, rawQueryCursorSessions.sessions)
	assert.ErrorIs(t, state.opened.Err(), context.Canceled)
	assert.Error(t, state.session.SQLRows.Scan(new(any)))
	assert.Equal(t, 2, state.session.PageOffset)
}

func expectSQLExtensionRetry(state *sqlExtensionCursorTest) {
	expectRawQueryResources(state.rs, []string{state.virtual.ID}, state.virtual)
	state.cs.EXPECT().InternalGetByID(gomock.Any(), state.catalog.ID, true).Return(state.catalog, nil)
	state.cf.EXPECT().GetConnectorAvailability(state.catalog.ConnectorType).Return(interfaces.ConnectorAvailability{Available: true})
	state.rs.EXPECT().InternalGetByID(gomock.Any(), nil, state.physical.ID).Return(state.physical, nil)
}

func TestRawQueryServiceSQLSourceCursorValidator(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			state := newSQLExtensionCursorTest(t)
			state.validatorErr = httperrors.NewHTTPError(compileTestContext(), status, verrors.VegaBackend_Query_ExecuteFailed).
				WithErrorDetails("private extension source diagnostics").
				WithMetadata(map[string]any{"source": "private hidden source"})
			expectRawQueryResources(state.rs, []string{state.virtual.ID}, state.virtual)
			result, err := state.svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: state.token}})
			assert.Nil(t, result)
			assertHTTPError(t, err, status)
			assert.NotContains(t, err.Error(), "private")
			assert.Equal(t, 1, state.validatorCalls)
			if status != http.StatusInternalServerError {
				assertSQLExtensionCursorReleased(t, state)
				return
			}
			assert.Contains(t, rawQueryCursorSessions.sessions, state.token)
			assert.NoError(t, state.opened.Err())
			state.validatorErr = nil
			expectSQLExtensionRetry(state)
			result, err = state.svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: state.token}})
			require.NoError(t, err)
			assert.Equal(t, []map[string]any{{"id": int64(3)}}, result.Entries)
			assert.Equal(t, 2, state.validatorCalls)
			assertSQLExtensionCursorReleased(t, state)
		})
	}
	t.Run("direct permission revocation precedes validator and metadata", func(t *testing.T) {
		state := newSQLExtensionCursorTest(t)
		denial := httperrors.NewHTTPError(compileTestContext(), http.StatusForbidden, httperrors.PublicError_Forbidden)
		state.rs.EXPECT().CheckResourcePermission(gomock.Any(), state.virtual.ID, interfaces.OPERATION_TYPE_QUERY_DATA).Return(denial)
		result, err := state.svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: state.token}})
		assert.Nil(t, result)
		assert.Same(t, denial, err)
		assert.Equal(t, 0, state.validatorCalls)
		assertSQLExtensionCursorReleased(t, state)
	})
	for _, phase := range []string{"Catalog", "physical Table"} {
		t.Run(phase+" temporary read error is safe and retryable", func(t *testing.T) {
			state := newSQLExtensionCursorTest(t)
			expectRawQueryResources(state.rs, []string{state.virtual.ID}, state.virtual)
			if phase == "Catalog" {
				failure := httperrors.NewHTTPError(compileTestContext(), http.StatusInternalServerError, verrors.VegaBackend_Catalog_InternalError_GetFailed).
					WithErrorDetails("private physical catalog database diagnostics")
				state.cs.EXPECT().InternalGetByID(gomock.Any(), state.catalog.ID, true).Return(nil, failure)
			} else {
				state.cs.EXPECT().InternalGetByID(gomock.Any(), state.catalog.ID, true).Return(state.catalog, nil)
				state.cf.EXPECT().GetConnectorAvailability(state.catalog.ConnectorType).Return(interfaces.ConnectorAvailability{Available: true})
				state.rs.EXPECT().InternalGetByID(gomock.Any(), nil, state.physical.ID).Return(nil, errors.New("private physical Table metadata failure"))
			}
			result, err := state.svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: state.token}})
			assert.Nil(t, result)
			assertHTTPError(t, err, http.StatusInternalServerError)
			assert.NotContains(t, err.Error(), "private")
			assert.Contains(t, rawQueryCursorSessions.sessions, state.token)
			assert.NoError(t, state.opened.Err())
			expectSQLExtensionRetry(state)
			result, err = state.svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: state.token}})
			require.NoError(t, err)
			assert.Equal(t, []map[string]any{{"id": int64(3)}}, result.Entries)
			assertSQLExtensionCursorReleased(t, state)
		})
	}
}

func TestRawQueryServiceSQLSourceAvailability(t *testing.T) {
	for _, change := range []string{"unavailable", "disabled", "nil factory", "physical version", "physical category", "physical missing", "Catalog version"} {
		t.Run(change+" invalidates retained Rows", func(t *testing.T) {
			state := newSQLExtensionCursorTest(t)
			expectRawQueryResources(state.rs, []string{state.virtual.ID}, state.virtual)
			status, code := http.StatusConflict, verrors.VegaBackend_Query_ExecuteFailed
			availability := interfaces.ConnectorAvailability{Available: true}
			switch change {
			case "unavailable":
				availability.Available = false
				status, code = http.StatusForbidden, verrors.VegaBackend_Connector_EntitlementDenied
			case "disabled":
				availability.Disabled = true
				code = verrors.VegaBackend_Connector_Disabled
			case "nil factory":
				state.svc.cf = nil
				status, code = http.StatusForbidden, verrors.VegaBackend_Connector_EntitlementDenied
			case "physical version":
				state.physical.UpdateTime++
			case "physical category":
				state.physical.Category = interfaces.ResourceCategoryIndex
			case "Catalog version":
				state.catalog.UpdateTime++
			}
			state.cs.EXPECT().InternalGetByID(gomock.Any(), state.catalog.ID, true).Return(state.catalog, nil)
			if change != "nil factory" {
				state.cf.EXPECT().GetConnectorAvailability(state.catalog.ConnectorType).Return(availability)
			}
			if change == "physical version" || change == "physical category" || change == "physical missing" {
				physical := state.physical
				if change == "physical missing" {
					physical = nil
				}
				state.rs.EXPECT().InternalGetByID(gomock.Any(), nil, state.physical.ID).Return(physical, nil)
			}
			result, err := state.svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: state.token}})
			assert.Nil(t, result)
			var failure *httperrors.HTTPError
			require.ErrorAs(t, err, &failure)
			assert.Equal(t, status, failure.HTTPCode)
			assert.Equal(t, code, failure.ErrorCode)
			assertSQLExtensionCursorReleased(t, state)
		})
	}
}

func TestSQLSourceReadError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		failure := httperrors.NewHTTPError(compileTestContext(), status, verrors.VegaBackend_Query_ExecuteFailed).
			WithErrorDetails("private source metadata")
		converted := sqlSourceReadError(compileTestContext(), failure)
		var result *httperrors.HTTPError
		require.ErrorAs(t, converted, &result)
		assert.Equal(t, status, result.HTTPCode)
		assert.Equal(t, failure.ErrorCode, result.ErrorCode)
		assert.NotContains(t, result.ErrorDetails, "private")
	}
	failure := errors.New("private raw metadata failure")
	converted := sqlSourceReadError(compileTestContext(), failure)
	assertHTTPError(t, converted, http.StatusInternalServerError)
	assert.NotContains(t, converted.Error(), "private")
}

func TestRawQueryServiceSQLSourceCountAndLogicalLimits(t *testing.T) {
	svc, rs, cs, cf := mysqlRawServices(t)
	virtual, hidden := sqlExtensionTestResources()
	hidden.SchemaDefinition = append(hidden.SchemaDefinition, &interfaces.Property{Name: "tenant", OriginalName: "tenant", Type: interfaces.DataType_String, OriginalType: "VARCHAR(32)"})
	physical := builderTestResource()
	expectRawQueryResources(rs, []string{virtual.ID, physical.ID}, virtual, physical)
	cs.EXPECT().InternalGetByID(gomock.Any(), hidden.CatalogID, true).Return(&interfaces.Catalog{
		ID: hidden.CatalogID, ConnectorType: "mysql", Enabled: true,
	}, nil)
	registerSQLTestPreparer(t, func(_ context.Context, query *sb.SelectBuilder, _ []*interfaces.Resource) (*interfaces.SQLViewQuery, error) {
		union := query.FromPart.(*sb.Union)
		union.Left.FromPart = &sb.Subquery{Alias: "prepared", Query: sb.Select(
			&sb.Alias{Expr: &sb.Column{Name: "order.id"}, Name: "id"},
		).From(&sb.Table{ResourceID: hidden.ID}).Where(
			&sb.Eq{Left: &sb.Column{Name: "tenant"}, Right: &sb.Param{Value: "bound"}},
		)}
		return &interfaces.SQLViewQuery{Sources: []*interfaces.Resource{hidden}, CursorValidator: &sqlTestCursorValidator{}}, nil
	})
	for _, count := range []bool{true, false} {
		base := vmock.NewMockTableConnector(gomock.NewController(t))
		base.EXPECT().Close(gomock.Any()).Return(nil)
		cf.EXPECT().CreateConnectorInstance(gomock.Any(), "mysql", gomock.Any()).Return(&builderConnector{
			TableConnector: base,
			run: func(_ context.Context, query *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
				text, args, err := mysql.ToSql(query)
				require.NoError(t, err)
				assert.Equal(t, []any{"bound"}, args)
				assert.Contains(t, text, "UNION ALL")
				assert.Contains(t, text, "WHERE (`tenant` = ?)")
				if count {
					assert.True(t, strings.HasPrefix(text, "SELECT COUNT(*)"), text)
					inner := query.FromPart.(*sb.Subquery)
					assert.Equal(t, "_raw_query_total", inner.Alias)
					require.NotNil(t, inner.Query.LimitPart)
					assert.EqualValues(t, 3, *inner.Query.LimitPart)
					assert.Zero(t, inner.Query.OffsetPart)
					assert.Contains(t, text, "LIMIT 3")
					return &interfaces.RawQueryResponse{Entries: []map[string]any{{rawQueryTotalCountColumn: int64(3)}}}, nil
				}
				assert.True(t, strings.HasSuffix(text, "LIMIT 1 OFFSET 1"), text)
				return &interfaces.RawQueryResponse{Entries: []map[string]any{{"id": int64(2)}}}, nil
			},
		}, nil)
	}
	result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL,
		Query:     "SELECT id FROM {{virtual}} UNION ALL SELECT `order.id` AS id FROM {{orders}} ORDER BY id LIMIT 3",
		NeedTotal: true, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeSingle, Limit: 1, Offset: 1}})
	require.NoError(t, err)
	require.NotNil(t, result.TotalCount)
	assert.EqualValues(t, 3, *result.TotalCount)
	assert.Equal(t, []map[string]any{{"id": int64(2)}}, result.Entries)
	assert.Equal(t, []string{virtual.ID, physical.ID}, result.ResourceIDs)
}
