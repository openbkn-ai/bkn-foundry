// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package query

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"testing"

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
	run func(context.Context, *sb.SelectBuilder) (*interfaces.RawQueryResponse, error)
}

type unavailableSQLPolicy struct{}

func (c *builderConnector) ExecuteSQLBuilder(ctx context.Context, q *sb.SelectBuilder) (*interfaces.RawQueryResponse, error) {
	return c.run(ctx, q)
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

func mysqlRawServices(t *testing.T) (*rawQueryService, *vmock.MockResourceService, *vmock.MockCatalogService, *vmock.MockConnectorFactory) {
	t.Helper()
	ctrl := gomock.NewController(t)
	rs, cs, cf := vmock.NewMockResourceService(ctrl), vmock.NewMockCatalogService(ctrl), vmock.NewMockConnectorFactory(ctrl)
	oldPolicy := rawQueryPolicy
	rawQueryPolicy = unavailableSQLPolicy{}
	oldSessions := rawQueryCursorSessions
	rawQueryCursorSessions = newCursorSessionManager(10, 9)
	t.Cleanup(func() { rawQueryPolicy = oldPolicy; rawQueryCursorSessions = oldSessions })
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
	t.Run("strings and comments do not become resource bindings", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		mysqlRawMetadata(rs, cs, builderTestResource(), true)
		mysqlRawRead(t, cf, "SELECT ? AS `label` FROM `sales.eu`.`order.items` WHERE (`id` = ?) LIMIT 2", []any{"{{hidden}}", json.Number("1")}, []map[string]any{{"label": "{{hidden}}"}}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT '{{hidden}}' AS label FROM {{orders}} WHERE id=1 /* {{ignored}} */", Paging: interfaces.PagingRequest{Limit: 2}})
		require.NoError(t, err)
		assert.Nil(t, result.TotalCount)
		assert.Equal(t, []string{"orders"}, result.ResourceIDs)
	})
	t.Run("cursor keeps builder and counts once with bounded lookahead", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawRead(t, cf, "SELECT COUNT(*) AS `_raw_query_total_count` FROM (SELECT `id` FROM `sales.eu`.`order.items` ORDER BY `id` ASC LIMIT 5) AS `_raw_query_total`", nil, []map[string]any{{rawQueryTotalCountColumn: int64(5)}}, nil)
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` ORDER BY `id` ASC LIMIT 3", nil, []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}} ORDER BY id LIMIT 5", NeedTotal: true, Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
		require.NoError(t, err)
		require.NotNil(t, result.Paging.NextCursor)
		token := *result.Paging.NextCursor
		session, ok := rawQueryCursorSessions.acquire(token)
		require.True(t, ok)
		assert.Empty(t, session.CompiledSQL)
		require.NotNil(t, session.SQLQuery)
		assert.EqualValues(t, 5, *session.SQLQuery.LimitPart)
		assert.Zero(t, session.SQLQuery.OffsetPart)
		rawQueryCursorSessions.release(session)
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` ORDER BY `id` ASC LIMIT 3 OFFSET 2", nil, []map[string]any{{"id": 3}, {"id": 4}, {"id": 5}}, nil)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Len(t, result.Entries, 2)
		assert.Equal(t, token, *result.Paging.NextCursor)
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` ORDER BY `id` ASC LIMIT 1 OFFSET 4", nil, []map[string]any{{"id": 5}}, nil)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Nil(t, result.Paging.NextCursor)
		require.NotNil(t, result.TotalCount)
		assert.EqualValues(t, 5, *result.TotalCount)
		assert.Equal(t, []map[string]any{{"id": 5}}, result.Entries)
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
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 3", nil, nil, errors.New("private source error"))
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
			mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 3", nil, []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}, nil)
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
			assert.Equal(t, session, rawQueryCursorSessions.sessions[token])
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
	t.Run("cursor failure does not consume the page and can retry", func(t *testing.T) {
		svc, rs, cs, cf := mysqlRawServices(t)
		resource := builderTestResource()
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 3", nil, []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}, nil)
		result, err := svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{QueryFormat: interfaces.QueryFormatSQL, Query: "SELECT id FROM {{orders}}", Paging: interfaces.PagingRequest{Mode: interfaces.PagingModeCursor, Limit: 2}})
		require.NoError(t, err)
		token := *result.Paging.NextCursor
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 3 OFFSET 2", nil, nil, errors.New("query failed"))
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		assert.Nil(t, result)
		assertHTTPError(t, err, http.StatusInternalServerError)
		assert.Equal(t, 2, rawQueryCursorSessions.sessions[token].PageOffset)
		mysqlRawMetadata(rs, cs, resource, true)
		mysqlRawRead(t, cf, "SELECT `id` FROM `sales.eu`.`order.items` LIMIT 3 OFFSET 2", nil, []map[string]any{{"id": 3}}, nil)
		result, err = svc.Execute(compileTestContext(), &interfaces.RawQueryRequest{Paging: interfaces.PagingRequest{Cursor: token}})
		require.NoError(t, err)
		assert.Equal(t, []map[string]any{{"id": 3}}, result.Entries)
		assert.Nil(t, result.Paging.NextCursor)
	})
}
