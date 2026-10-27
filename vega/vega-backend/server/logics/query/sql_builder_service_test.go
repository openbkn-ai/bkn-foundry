// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	vmock "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/mock"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	mysql "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/connector/local/table/mariadb"
)

type builderConnector struct {
	interfaces.Connector
	run  func(context.Context, *sb.SelectBuilder) (*interfaces.RawQueryResponse, error)
	open func(context.Context, *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error)
}

type unavailableSQLPolicy struct{}

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
func mysqlRawMetadata(rs *vmock.MockResourceService, cs *vmock.MockCatalogService, resource *interfaces.Resource, individual bool) {
	expectRawQueryResources(rs, []string{"orders"}, resource)
	cs.EXPECT().InternalGetByID(gomock.Any(), "catalog", true).Return(&interfaces.Catalog{ID: "catalog", ConnectorType: interfaces.ConnectorTypeMySQL, Enabled: true, UpdateTime: 7}, nil)
	if individual {
		rs.EXPECT().InternalGetByID(gomock.Any(), nil, "orders").Return(resource, nil)
	}
}
func mysqlRawRead(t *testing.T, cf *vmock.MockConnectorFactory, statement string, args []any, entries []map[string]any, failure error) {
	t.Helper()
	base := vmock.NewMockTableConnector(gomock.NewController(t))
	base.EXPECT().Close(gomock.Any()).Return(nil)
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeMySQL, gomock.Any()).Return(&builderConnector{Connector: base, run: func(ctx context.Context, q *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
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
func mysqlRawCursorRows(t *testing.T, cf *vmock.MockConnectorFactory, statement string, source *sqlmock.Rows, failure error) <-chan context.Context {
	t.Helper()
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
	cf.EXPECT().CreateConnectorInstance(gomock.Any(), interfaces.ConnectorTypeMySQL, gomock.Any()).Return(&builderConnector{Connector: base, open: func(ctx context.Context, query *sb.SelectBuilder) (*sql.Rows, []interfaces.ColumnInfo, error) {
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
					catalog.ConnectorType = interfaces.ConnectorTypeMariaDB
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
