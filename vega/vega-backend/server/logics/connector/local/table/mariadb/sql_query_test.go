// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	qm "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/querymodel"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/binding"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/compiler"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/query/planning"
)

type sqlTestResolver struct {
	resource  *interfaces.Resource
	connector string
}

func (r sqlTestResolver) connectorType() string {
	if r.connector != "" {
		return r.connector
	}
	return interfaces.ConnectorTypeMariaDB
}

func (r sqlTestResolver) ResolveSQLQueryResource(context.Context, string, qm.ExecutionIdentity) (*interfaces.Resource, string, error) {
	return r.resource, r.connectorType(), nil
}

func (r sqlTestResolver) AuthorizeSQLQueryResource(_ context.Context, id string, identity qm.ExecutionIdentity, op string) (qm.AuthorizationBasis, error) {
	return qm.AuthorizationBasis{ResourceID: id, Operation: op, Namespace: identity.AuthorizationNamespace}, nil
}

func (r sqlTestResolver) ResolveSQLQuerySource(_ context.Context, scan qm.Scan, identity qm.ExecutionIdentity) (qm.Scan, error) {
	scan.Identifier = qm.PhysicalIdentifier{"sales.eu", "orders`all"}
	scan.Fields = append([]qm.SourceField(nil), scan.Fields...)
	for i := range scan.Fields {
		scan.Fields[i].Identifier = qm.PhysicalIdentifier{scan.Fields[i].LogicalName}
	}
	return scan, nil
}

func sqlTestPlan(t *testing.T, sql string) *qm.SelectBuilder {
	t.Helper()
	return sqlTestPlanWithSource(t, sql, interfaces.ConnectorTypeMariaDB, nil)
}

func sqlTestPlanWithSource(t *testing.T, sql, connector string, properties []*interfaces.Property) *qm.SelectBuilder {
	t.Helper()
	tree, err := compiler.ParseSQL(sql)
	require.NoError(t, err)
	input, err := compiler.ConvertToIR(tree)
	require.NoError(t, err)
	return sqlTestPlanFromIR(t, input, connector, properties)
}
func sqlTestPlanFromIR(t *testing.T, input *qm.SelectBuilder, connector string, properties []*interfaces.Property) *qm.SelectBuilder {
	t.Helper()
	semantics, err := binding.NewSemanticContext("UTC")
	require.NoError(t, err)
	identity := qm.ExecutionIdentity{SubjectID: "caller", SubjectType: "user", AuthorizationNamespace: "resource"}
	resolver := sqlTestResolver{resource: &interfaces.Resource{ID: "orders", CatalogID: "cat", Category: interfaces.ResourceCategoryTable, Enabled: true, Status: interfaces.ResourceStatusActive, UpdateTime: 11, SchemaDefinition: []*interfaces.Property{{Name: "id", Type: interfaces.DataType_Integer, OriginalType: "BIGINT"}, {Name: "name", Type: interfaces.DataType_String, OriginalType: "VARCHAR(100)"}}}}
	resolver.connector = connector
	resolver.resource.SchemaDefinition = append(resolver.resource.SchemaDefinition, properties...)
	bound, err := binding.Bind(context.Background(), input, semantics, identity, resolver, resolver)
	require.NoError(t, err)
	plan, err := planning.Plan(context.Background(), bound, identity, resolver)
	require.NoError(t, err)
	return plan
}

func TestMariaDBConnectorPrepare(t *testing.T) {
	t.Run("validates IR without changing query or connecting", func(t *testing.T) {
		req := sqlTestPlan(t, "SELECT a.id FROM {{ orders }} a LEFT JOIN {{ orders }} b ON a.id=b.id WHERE a.name='x'' OR 1=1 --' ORDER BY b.id DESC LIMIT 3 OFFSET 2")
		before, err := json.Marshal(req)
		require.NoError(t, err)
		c := &MariaDBConnector{}
		require.NoError(t, c.Prepare(context.Background(), req))
		assert.False(t, c.connected)
		after, err := json.Marshal(req)
		require.NoError(t, err)
		assert.JSONEq(t, string(before), string(after))
	})
	for _, text := range []string{"SELECT SUM(id) FROM {{ orders }}", "SELECT id+1 FROM {{ orders }}", "SELECT DISTINCT name FROM {{ orders }}", "SELECT NOW() FROM {{ orders }}"} {
		t.Run(text, func(t *testing.T) {
			req := sqlTestPlan(t, text)
			c := &MariaDBConnector{}
			err := c.Prepare(context.Background(), req)
			var unsupported *interfaces.UnsupportedOperationError
			require.ErrorAs(t, err, &unsupported)
			assert.False(t, c.connected)
		})
	}
	for name, change := range map[string]func(*qm.SelectBuilder){
		"missing table":    func(q *qm.SelectBuilder) { q.FromPart.(*qm.Scan).Identifier = nil },
		"missing instance": func(q *qm.SelectBuilder) { q.FromPart.(*qm.Scan).Instance = 0 },
		"empty field name": func(q *qm.SelectBuilder) { q.FromPart.(*qm.Scan).Fields[0].Identifier = nil },
		"duplicate field": func(q *qm.SelectBuilder) {
			scan := q.FromPart.(*qm.Scan)
			scan.Fields = append(scan.Fields, scan.Fields[0])
		},
		"wrong native connector": func(q *qm.SelectBuilder) { q.FromPart.(*qm.Scan).Fields[0].NativeType.ConnectorType = "other" },
		"empty output":           func(q *qm.SelectBuilder) { q.ColumnsPart[0].Output = 0 },
		"empty output name":      func(q *qm.SelectBuilder) { q.ColumnsPart[0].Alias = "" },
		"duplicate output":       func(q *qm.SelectBuilder) { q.ColumnsPart = append(q.ColumnsPart, q.ColumnsPart[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			query := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
			change(query)
			c := &MariaDBConnector{}
			var unsupported *interfaces.UnsupportedOperationError
			require.ErrorAs(t, c.Prepare(context.Background(), query), &unsupported)
			assert.False(t, c.connected)
		})
	}
	t.Run("cross catalog IR stays representable but cannot execute here", func(t *testing.T) {
		query := sqlTestPlan(t, "SELECT a.id FROM {{ orders }} a JOIN {{ orders }} b ON a.id=b.id")
		join := query.FromPart.(*qm.Join)
		right := join.Right.(*qm.Scan)
		right.Resource.CatalogID = "another-catalog"
		c := &MariaDBConnector{}
		var unsupported *interfaces.UnsupportedOperationError
		require.ErrorAs(t, c.Prepare(context.Background(), query), &unsupported)
		assert.Equal(t, "another-catalog", right.Resource.CatalogID)
		assert.False(t, c.connected)
	})
}

func TestMariaDBConnectorToSQL(t *testing.T) {
	t.Run("typed aggregate builder shares SQL path", func(t *testing.T) {
		count := qm.Call("COUNT", qm.Star())
		input := qm.Select(qm.As(qm.Field("name"), "name"), qm.As(count, "n")).From(qm.Resource("orders", "")).GroupBy(qm.Field("name")).Having(qm.Compare(">", count, qm.Int64(1)))
		query := sqlTestPlanFromIR(t, input, interfaces.ConnectorTypeMySQL, nil)
		text, args, _, err := (&MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}).ToSQL(context.Background(), query, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(text, "SELECT "))
		assert.Contains(t, text, "COUNT(*) AS")
		assert.Contains(t, text, "HAVING (COUNT(*) > ?)")
		assert.Contains(t, text, "GROUP BY CAST(CONVERT(")
		assert.Equal(t, []any{int64(1)}, args)
	})

	t.Run("typed clauses render one select regardless of method order", func(t *testing.T) {
		predicate, err := binding.ConvertFilterCondition(&interfaces.FilterCondCfg{Name: "id", Operation: ">", Value: 2}, qm.Origin{Kind: qm.OriginDTO})
		require.NoError(t, err)
		a := qm.Select(qm.As(qm.Field("id"), "id")).From(qm.Resource("orders", "")).Limit(3, 0).Where(predicate).OrderBy(qm.SortKey{Expr: qm.Field("id")})
		b := qm.Select(qm.As(qm.Field("id"), "id")).OrderBy(qm.SortKey{Expr: qm.Field("id")}).Where(nil).Where(predicate).From(qm.Resource("orders", "")).Limit(3, 0)
		c := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}
		left := sqlTestPlanFromIR(t, a, interfaces.ConnectorTypeMySQL, nil)
		right := sqlTestPlanFromIR(t, b, interfaces.ConnectorTypeMySQL, nil)
		text, args, _, err := c.ToSQL(context.Background(), left, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		other, values, _, err := c.ToSQL(context.Background(), right, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(text, "SELECT "))
		assert.Equal(t, text, other)
		assert.Equal(t, []any{int64(2), int64(3), int64(0)}, args)
		assert.Equal(t, args, values)
		assert.Less(t, strings.Index(text, " WHERE "), strings.Index(text, " LIMIT "))
	})
	t.Run("direct order expressions bind each occurrence", func(t *testing.T) {
		query := sqlTestPlan(t, "SELECT id FROM {{ orders }} WHERE id>1 ORDER BY 'rank' LIMIT 2")
		text, args, _, err := (&MariaDBConnector{}).ToSQL(context.Background(), query, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(text, "SELECT "))
		assert.Equal(t, []any{int64(1), "rank", "rank", int64(2), int64(0)}, args)
	})
	t.Run("nested clauses collect parameters in final SQL order", func(t *testing.T) {
		query := sqlTestPlanWithSource(t, "SELECT 'outer' AS tag, q.id FROM (SELECT id FROM {{ orders }} WHERE id>1 LIMIT 8) q WHERE q.id<5 LIMIT 2", interfaces.ConnectorTypeMySQL, nil)
		text, args, _, err := (&MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}).ToSQL(context.Background(), query, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		assert.Equal(t, 2, strings.Count(text, "SELECT "))
		assert.Equal(t, []any{"outer", int64(1), int64(8), int64(0), int64(5), int64(2), int64(0)}, args)
	})

	t.Run("final read SQL obeys output budget", func(t *testing.T) {
		req := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		renderer := mysqlSQLRenderer{connector: interfaces.ConnectorTypeMariaDB, ctx: context.Background()}
		relation, err := renderer.selectQuery(req, 1, false)
		require.NoError(t, err)
		original := req.FromPart.(*qm.Scan).Fields[0].Identifier[0]
		req.FromPart.(*qm.Scan).Fields[0].Identifier[0] = strings.Repeat("x", len(original)+qm.MaxSQLBytes-len(relation.text)-1)
		for _, kind := range []qm.SQLReadKind{qm.SQLReadCount, qm.SQLReadPage} {
			text, _, _, err := (&MariaDBConnector{}).ToSQL(context.Background(), req, qm.SQLReadRequest{Kind: kind})
			assert.Error(t, err)
			assert.Empty(t, text)
		}
	})

	t.Run("SQL and configured condition use the same IR builder", func(t *testing.T) {
		predicate, err := binding.ConvertFilterCondition(&interfaces.FilterCondCfg{Name: "id", Operation: ">", Value: 2}, qm.Origin{Kind: qm.OriginDTO})
		require.NoError(t, err)
		ir := qm.Select(qm.As(qm.Field("id"), "id")).From(qm.Resource("orders", "")).Where(predicate).Limit(3, 0)
		structured := sqlTestPlanFromIR(t, ir, interfaces.ConnectorTypeMySQL, nil)
		parsed := sqlTestPlanWithSource(t, "SELECT id FROM {{ orders }} WHERE id>2 LIMIT 3", interfaces.ConnectorTypeMySQL, nil)
		c := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}
		left, args, _, err := c.ToSQL(context.Background(), structured, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		right, other, _, err := c.ToSQL(context.Background(), parsed, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		assert.Equal(t, left, right)
		assert.Equal(t, args, other)
	})
	t.Run("outer condition stays outside SQL view limit", func(t *testing.T) {
		tree, err := compiler.ParseSQL("SELECT id FROM {{ orders }} ORDER BY id LIMIT 10")
		require.NoError(t, err)
		inner, err := compiler.ConvertToIR(tree)
		require.NoError(t, err)
		predicate, err := binding.ConvertFilterCondition(&interfaces.FilterCondCfg{Name: "id", Operation: ">", Value: 2}, qm.Origin{Kind: qm.OriginDTO})
		require.NoError(t, err)
		outer := qm.Select(qm.As(qm.Field("id"), "id")).FromSelect(inner, "view").Where(predicate)
		query := sqlTestPlanFromIR(t, outer, interfaces.ConnectorTypeMySQL, nil)
		c := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}
		text, args, _, err := c.ToSQL(context.Background(), query, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		require.NoError(t, err)
		assert.Less(t, strings.Index(text, " LIMIT ? OFFSET ?"), strings.LastIndex(text, " WHERE "))
		assert.Equal(t, []any{int64(10), int64(0), int64(2)}, args)
	})

	req := sqlTestPlan(t, "SELECT a.id FROM {{ orders }} a LEFT JOIN {{ orders }} b ON a.id=b.id WHERE a.name='x'' OR 1=1 --' ORDER BY b.id DESC LIMIT 3 OFFSET 2")
	c := &MariaDBConnector{}
	before, err := json.Marshal(req)
	require.NoError(t, err)
	text, args, schema, err := c.ToSQL(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadPage, Offset: 1, Limit: new(int64(2))})
	require.NoError(t, err)
	assert.Contains(t, text, "`sales.eu`.`orders``all`")
	assert.Contains(t, text, "LEFT JOIN")
	assert.NotContains(t, text, "OR 1=1 --")
	assert.Equal(t, []any{"x' OR 1=1 --", int64(3), int64(2), int64(3), int64(1)}, args)
	schema[0].Name = "changed"
	assert.NotEqual(t, "changed", req.Schema()[0].Name)
	_, countArgs, _, err := c.ToSQL(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadCount})
	require.NoError(t, err)
	assert.Equal(t, []any{"x' OR 1=1 --", int64(3), int64(2)}, countArgs)
	after, err := json.Marshal(req)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(after))
}

func TestMariaDBConnectorExecute(t *testing.T) {
	t.Run("mysql aggregate empty and precise decimal results", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			lo, hi any
			count  int64
		}{
			{name: "empty aggregate"},
			{name: "exact decimal", lo: "12345678901234567890.123456789012345678", hi: "12345678901234567890.123456789012345679", count: 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req := sqlTestPlanWithSource(t, "SELECT MIN(amount) AS lo, MAX(amount) AS hi, COUNT(*) AS n FROM {{ orders }}", interfaces.ConnectorTypeMySQL, []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL(38,18)"}})
				db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				c := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL, db: db, connected: true}

				require.NoError(t, c.Prepare(context.Background(), req))
				read := qm.SQLReadRequest{Kind: qm.SQLReadRelation}
				text, _, _, err := c.ToSQL(context.Background(), req, read)
				require.NoError(t, err)
				mock.ExpectQuery(text).WillReturnRows(sqlmock.NewRows([]string{"lo", "hi", "n"}).AddRow(tc.lo, tc.hi, tc.count)).RowsWillBeClosed()
				stream, err := c.Execute(context.Background(), req, read)
				require.NoError(t, err)
				batch, err := stream.Next(context.Background())
				require.NoError(t, err)
				require.Len(t, batch.Rows, 1)
				if tc.count == 0 {
					assert.IsType(t, qm.NullValue{}, batch.Rows[0][0].Data)
					assert.IsType(t, qm.NullValue{}, batch.Rows[0][1].Data)
				} else {
					assert.Equal(t, qm.DecimalValue(tc.lo.(string)), batch.Rows[0][0].Data)
					assert.Equal(t, qm.DecimalValue(tc.hi.(string)), batch.Rows[0][1].Data)
				}
				assert.Equal(t, qm.IntegerValue(strconv.FormatInt(tc.count, 10)), batch.Rows[0][2].Data)
				_, err = stream.Next(context.Background())
				assert.ErrorIs(t, err, io.EOF)
				assert.Equal(t, qm.CompletionRelationEnd, stream.Completion().Kind)
				require.NoError(t, stream.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	})

	t.Run("window and count retain logical query boundary", func(t *testing.T) {
		req := sqlTestPlan(t, "SELECT id FROM {{ orders }} ORDER BY id DESC LIMIT 3 OFFSET 2")
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		c := &MariaDBConnector{db: db, connected: true}

		require.NoError(t, c.Prepare(context.Background(), req))

		read := qm.SQLReadRequest{Kind: qm.SQLReadPage, Offset: 1, Limit: new(int64(1))}
		text, _, _, err := c.ToSQL(context.Background(), req, read)
		require.NoError(t, err)
		// 最外层显式重新排序，逻辑 LIMIT 与 API 读取窗口分别占位。
		assert.Contains(t, text, ") AS q ORDER BY")
		assert.Equal(t, 2, strings.Count(text, " LIMIT ? OFFSET ?"))
		mock.ExpectQuery(text).WithArgs(int64(3), int64(2), int64(2), int64(1)).WillReturnRows(sqlmock.NewRows([]string{"f"}).AddRow(8).AddRow(7)).RowsWillBeClosed()
		stream, err := c.Execute(context.Background(), req, read)
		require.NoError(t, err)
		batch, err := stream.Next(context.Background())
		require.NoError(t, err)
		require.Len(t, batch.Rows, 1)
		assert.Equal(t, qm.IntegerValue("8"), batch.Rows[0][0].Data)
		_, err = stream.Next(context.Background())
		assert.ErrorIs(t, err, io.EOF)
		assert.Equal(t, qm.CompletionWindowEnd, stream.Completion().Kind)
		require.NotNil(t, stream.Completion().NextOffset)
		assert.Equal(t, int64(2), *stream.Completion().NextOffset)
		require.NoError(t, stream.Close())
		count := qm.SQLReadRequest{Kind: qm.SQLReadCount}
		text, _, _, err = c.ToSQL(context.Background(), req, count)
		require.NoError(t, err)
		mock.ExpectQuery(text).WithArgs(int64(3), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3)).RowsWillBeClosed()
		stream, err = c.Execute(context.Background(), req, count)
		require.NoError(t, err)
		batch, err = stream.Next(context.Background())
		require.NoError(t, err)
		assert.Equal(t, qm.IntegerValue("3"), batch.Rows[0][0].Data)
		assert.Equal(t, qm.CompletionRelationEnd, stream.Completion().Kind)
		require.NoError(t, stream.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("invalid reads perform no source request", func(t *testing.T) {
		req := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		c := &MariaDBConnector{}

		require.NoError(t, c.Prepare(context.Background(), req))
		for _, read := range []qm.SQLReadRequest{{Kind: qm.SQLReadPage, Offset: -1}, {Kind: qm.SQLReadCount, Limit: new(int64(1))}, {Kind: "invalid"}} {
			stream, err := c.Execute(context.Background(), req, read)
			assert.Nil(t, stream)
			assert.Error(t, err)
		}
		stream, err := c.Execute(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadPage, Limit: new(int64(0))})
		require.NoError(t, err)
		_, err = stream.Next(context.Background())
		assert.ErrorIs(t, err, io.EOF)
		assert.Equal(t, qm.CompletionWindowEnd, stream.Completion().Kind)
		req.FromPart.(*qm.Scan).Identifier = nil
		stream, err = c.Execute(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		assert.Nil(t, stream)
		assert.Error(t, err)
		assert.False(t, c.connected)
	})
	t.Run("mismatched connector rejected", func(t *testing.T) {
		req := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		other := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}
		stream, err := other.Execute(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		assert.Nil(t, stream)
		assert.Error(t, err)
		assert.False(t, other.connected)
	})
	t.Run("driver failure is not EOF", func(t *testing.T) {
		req := sqlTestPlan(t, "SELECT id FROM {{ orders }}")
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		c := &MariaDBConnector{db: db, connected: true}

		require.NoError(t, c.Prepare(context.Background(), req))
		failure := errors.New("source failed")
		mock.ExpectQuery("SELECT").WillReturnError(failure)
		stream, err := c.Execute(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
		assert.Nil(t, stream)
		assert.ErrorIs(t, err, failure)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMySQLSQLRendererRelation(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		args []any
	}{
		{sql: "SELECT 'outer' AS label, a.id FROM (SELECT id FROM {{ orders }} WHERE id>2 ORDER BY id LIMIT 5) a", args: []any{"outer", int64(2), int64(5), int64(0)}},
		{sql: "SELECT id FROM {{ orders }} WHERE name IN ('a', 'a ') ORDER BY name DESC LIMIT 2", args: []any{"a", "a ", int64(2), int64(0)}},
		{sql: "SELECT id FROM {{ orders }} WHERE id BETWEEN 1 AND 3 AND name IS NOT NULL", args: []any{int64(1), int64(3)}},
		{sql: "SELECT id FROM {{ orders }} WHERE id IN (1, NULL)", args: []any{int64(1), sql.NullInt64{}}},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			req := sqlTestPlan(t, tc.sql)
			renderer := mysqlSQLRenderer{connector: interfaces.ConnectorTypeMariaDB, ctx: context.Background()}
			relation, err := renderer.selectQuery(req, 1, false)
			require.NoError(t, err)
			assert.Equal(t, tc.args, relation.args)
			assert.Equal(t, len(relation.args), strings.Count(relation.text, "?"))
		})
	}
	t.Run("cyclic relation bounded before SQL", func(t *testing.T) {
		source := &qm.Subquery{Alias: "cycle"}
		root := qm.Select().From(source)
		source.Query = root
		renderer := mysqlSQLRenderer{connector: interfaces.ConnectorTypeMariaDB, ctx: context.Background()}
		_, err := renderer.selectQuery(root, 1, false)
		assert.Error(t, err)
	})
}

func TestSQLEmitterAggregate(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		want      []string
		args      []any
	}{
		{name: "group having sort", sql: "SELECT name, COUNT(*) AS n, COUNT(DISTINCT id) AS u, MIN(amount) AS lo, MAX(amount) AS hi FROM {{ orders }} WHERE id>2 GROUP BY name HAVING COUNT(*)>1 ORDER BY name LIMIT 3", want: []string{"GROUP BY CAST(CONVERT(", "COUNT(*)", "COUNT(DISTINCT", "MIN(", "MAX(", "USING utf8mb4) AS BINARY)", "ORDER BY"}, args: []any{int64(2), int64(1), int64(3), int64(0)}},
		{name: "string extrema and distinct", sql: "SELECT MIN(name), MAX(name), COUNT(DISTINCT name) FROM {{ orders }}", want: []string{"MIN(CAST(CONVERT(", "MAX(CAST(CONVERT(", "COUNT(DISTINCT CAST(CONVERT("}},
		{name: "exact constant before filtered source", sql: "SELECT COUNT(DISTINCT 1.2) FROM {{ orders }} WHERE id>2", want: []string{"COUNT(DISTINCT CAST(? AS DECIMAL(2,1)))"}, args: []any{"1.2", int64(2)}},
		{name: "decimal comparison is never a float parameter", sql: "SELECT amount FROM {{ orders }} WHERE amount > 1.25", want: []string{"CAST(? AS DECIMAL(3,2))", "CAST(", "DECIMAL(24,4)"}, args: []any{"1.25"}},
		{name: "distinct projects binary string", sql: "SELECT DISTINCT name FROM {{ orders }} ORDER BY name", want: []string{"SELECT DISTINCT CAST(CONVERT(", "ORDER BY"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := sqlTestPlanWithSource(t, tc.sql, interfaces.ConnectorTypeMySQL, []*interfaces.Property{{Name: "amount", Type: interfaces.DataType_Decimal, OriginalType: "DECIMAL(24,4)"}})
			c := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}

			require.NoError(t, c.Prepare(context.Background(), req))

			text, args, schema, err := c.ToSQL(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadRelation})
			require.NoError(t, err)
			for _, part := range tc.want {
				assert.Contains(t, text, part)
			}
			assert.Equal(t, tc.args, args)
			assert.Equal(t, req.Schema(), schema)
			assert.False(t, c.connected)
			count, _, _, err := c.ToSQL(context.Background(), req, qm.SQLReadRequest{Kind: qm.SQLReadCount})
			require.NoError(t, err)
			assert.Contains(t, count, "SELECT COUNT(*) AS `f1` FROM (")
		})
	}
	t.Run("float projection and MariaDB boundary", func(t *testing.T) {
		for _, connector := range []string{interfaces.ConnectorTypeMySQL, interfaces.ConnectorTypeMariaDB} {
			req := sqlTestPlanWithSource(t, "SELECT ratio FROM {{ orders }}", connector, []*interfaces.Property{{Name: "ratio", Type: interfaces.DataType_Float, OriginalType: "DOUBLE"}})
			c := &MariaDBConnector{connectorType: connector}
			err := c.Prepare(context.Background(), req)
			if connector == interfaces.ConnectorTypeMySQL {
				require.NoError(t, err)
				assert.Equal(t, qm.TypeFloat, req.Schema()[0].Type.Kind)
			} else {

				var unsupported *interfaces.UnsupportedOperationError
				require.ErrorAs(t, err, &unsupported)
			}
			assert.False(t, c.connected)
		}
	})

	t.Run("unsupported sum refuses before any source request", func(t *testing.T) {
		req := sqlTestPlanWithSource(t, "SELECT SUM(id) FROM {{ orders }}", interfaces.ConnectorTypeMySQL, nil)
		c := &MariaDBConnector{connectorType: interfaces.ConnectorTypeMySQL}
		err := c.Prepare(context.Background(), req)

		var unsupported *interfaces.UnsupportedOperationError
		require.ErrorAs(t, err, &unsupported)
		assert.False(t, c.connected)
	})
}

func TestMysqlDecimal(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		p, s       int
		want       string
		invalid    bool
	}{
		{name: "38 digits", text: "12345678901234567890.123456789012345678", p: 38, s: 18, want: "12345678901234567890.123456789012345678"},
		{name: "preserve scale", text: "1.2", p: 4, s: 3, want: "1.200"},
		{name: "trailing zero rescale", text: "-001.2300", p: 4, s: 2, want: "-1.23"},
		{name: "fraction only precision", text: "0.012", p: 3, s: 3, want: "0.012"},
		{name: "negative zero", text: "-0.000", p: 3, s: 2, want: "0.00"},
		{name: "integer overflow", text: "100.00", p: 4, s: 2, invalid: true},
		{name: "fraction loss", text: "1.231", p: 4, s: 2, invalid: true},
		{name: "float notation refused", text: "1e2", p: 4, s: 2, invalid: true},
		{name: "malformed sign", text: "-+1", p: 4, s: 2, invalid: true},
		{name: "missing digits", text: "1.", p: 4, s: 2, invalid: true},
		{name: "mysql scale limit", text: "0.0", p: 38, s: 31, invalid: true},
		{name: "platform precision limit", text: "1", p: 39, s: 0, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mysqlDecimal(tc.text, qm.LogicalType{Kind: qm.TypeDecimal, Precision: tc.p, Scale: tc.s})
			if tc.invalid {
				assert.Empty(t, got)
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestMysqlLosslessDecimalCast(t *testing.T) {
	decimal := func(p, s int) qm.LogicalType { return qm.LogicalType{Kind: qm.TypeDecimal, Precision: p, Scale: s} }
	assert.True(t, mysqlLosslessDecimalCast(decimal(10, 2), decimal(12, 4)))
	assert.False(t, mysqlLosslessDecimalCast(decimal(10, 2), decimal(10, 4)))
	assert.False(t, mysqlLosslessDecimalCast(decimal(10, 2), decimal(10, 1)))
	assert.True(t, mysqlLosslessDecimalCast(qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, decimal(21, 2)))
	assert.False(t, mysqlLosslessDecimalCast(qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64, Unsigned: true}, decimal(21, 2)))
	assert.True(t, mysqlLosslessDecimalCast(qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64, Unsigned: true}, decimal(22, 2)))
	assert.False(t, mysqlLosslessDecimalCast(qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}, decimal(22, 2)))
}

func TestSQLResultStreamNext(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rows          *sqlmock.Rows
		limit         *int64
		want          int
		kind          qm.CompletionKind
		failure       error
		decodeFailure bool
	}{
		{name: "empty is relation EOF", rows: sqlmock.NewRows([]string{"id"}), kind: qm.CompletionRelationEnd},
		{name: "exact page EOF", rows: sqlmock.NewRows([]string{"id"}).AddRow(1), limit: new(int64(1)), want: 1, kind: qm.CompletionRelationEnd},
		{name: "probe establishes next page", rows: sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2), limit: new(int64(1)), want: 1, kind: qm.CompletionWindowEnd},
		{name: "null retains typed SQL NULL", rows: sqlmock.NewRows([]string{"id"}).AddRow(nil), want: 1, kind: qm.CompletionRelationEnd},
		{name: "row failure", rows: sqlmock.NewRows([]string{"id"}).AddRow(1).RowError(0, io.ErrUnexpectedEOF), failure: io.ErrUnexpectedEOF},
		{name: "invalid integer not truncated", rows: sqlmock.NewRows([]string{"id"}).AddRow("9223372036854775808"), decodeFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectQuery("SELECT").WillReturnRows(tc.rows).RowsWillBeClosed()
			rows, err := db.QueryContext(context.Background(), "SELECT id")
			require.NoError(t, err)
			stream := &sqlResultStream{rows: rows, schema: qm.Schema{{ID: 1, Name: "id", Type: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, Nullable: true}}, offset: 5, limit: tc.limit}
			batch, err := stream.Next(context.Background())
			if tc.failure != nil || tc.decodeFailure {
				assert.Empty(t, batch.Rows)
				if tc.failure != nil {
					assert.ErrorIs(t, err, tc.failure)
				} else {
					assert.Error(t, err)
				}
				assert.Empty(t, stream.Completion().Kind)
			} else {
				if tc.want == 0 {
					assert.ErrorIs(t, err, io.EOF)
				} else {
					require.NoError(t, err)
				}
				assert.Len(t, batch.Rows, tc.want)
				_, err = stream.Next(context.Background())
				assert.ErrorIs(t, err, io.EOF)
				assert.Equal(t, tc.kind, stream.Completion().Kind)
				if tc.kind == qm.CompletionWindowEnd {
					completion := stream.Completion()
					require.NotNil(t, completion.NextOffset)
					assert.Equal(t, int64(6), *completion.NextOffset)
					*completion.NextOffset = 999
					assert.Equal(t, int64(6), *stream.Completion().NextOffset)
				}
				if tc.name == "null retains typed SQL NULL" {
					assert.IsType(t, qm.NullValue{}, batch.Rows[0][0].Data)
				}
			}
			require.NoError(t, stream.Close())
			require.NoError(t, stream.Close())
			if tc.failure != nil {
				assert.ErrorIs(t, rows.Err(), tc.failure)
			} else {
				assert.NoError(t, rows.Err())
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	t.Run("cancellation closes rows and never publishes completion", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		mock.ExpectQuery("SELECT").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1)).RowsWillBeClosed()
		rows, err := db.QueryContext(context.Background(), "SELECT id")
		require.NoError(t, err)
		stream := &sqlResultStream{rows: rows}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = stream.Next(ctx)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, stream.Completion().Kind)
		assert.NoError(t, rows.Err())
		require.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("early Close is not successful EOF", func(t *testing.T) {
		stream := &sqlResultStream{}
		require.NoError(t, stream.Close())
		_, err := stream.Next(context.Background())
		assert.Error(t, err)
		assert.False(t, errors.Is(err, io.EOF))
		assert.Empty(t, stream.Completion().Kind)
	})
}

func TestDecodeSQLCell(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     any
		typ     qm.LogicalType
		want    qm.ValueData
		invalid bool
	}{
		{name: "uint64 exact", raw: []byte("18446744073709551615"), typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64, Unsigned: true}, want: qm.IntegerValue("18446744073709551615")},
		{name: "signed minimum", raw: int64(-9223372036854775808), typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, want: qm.IntegerValue("-9223372036854775808")},
		{name: "boolean", raw: int64(1), typ: qm.LogicalType{Kind: qm.TypeBoolean}, want: qm.BooleanValue(true)},
		{name: "invalid boolean", raw: int64(2), typ: qm.LogicalType{Kind: qm.TypeBoolean}, invalid: true},
		{name: "string bytes", raw: []byte("text "), typ: qm.LogicalType{Kind: qm.TypeString}, want: qm.StringValue("text ")},
		{name: "no numeric to string coercion", raw: int64(2), typ: qm.LogicalType{Kind: qm.TypeString}, invalid: true},
		{name: "no float to integer coercion", raw: 1.2, typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, invalid: true},
		{name: "decimal exact", raw: []byte("12345678901234567890.123456789012345678"), typ: qm.LogicalType{Kind: qm.TypeDecimal, Precision: 38, Scale: 18}, want: qm.DecimalValue("12345678901234567890.123456789012345678")},
		{name: "decimal scale loss", raw: []byte("1.239"), typ: qm.LogicalType{Kind: qm.TypeDecimal, Precision: 4, Scale: 2}, invalid: true},
		{name: "decimal integer overflow", raw: []byte("100.00"), typ: qm.LogicalType{Kind: qm.TypeDecimal, Precision: 4, Scale: 2}, invalid: true},
		{name: "decimal from float refused", raw: 1.23, typ: qm.LogicalType{Kind: qm.TypeDecimal, Precision: 4, Scale: 2}, invalid: true},
		{name: "finite float", raw: 1.25, typ: qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}, want: qm.FloatValue(1.25)},
		{name: "float wire text", raw: []byte("1.25e10"), typ: qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}, want: qm.FloatValue(1.25e10)},
		{name: "infinite float", raw: math.Inf(1), typ: qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}, invalid: true},
		{name: "NaN float", raw: math.NaN(), typ: qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}, invalid: true},
		{name: "overflow float text", raw: "1e999", typ: qm.LogicalType{Kind: qm.TypeFloat, BitWidth: 64}, invalid: true},
		{name: "nonnullable null", typ: qm.LogicalType{Kind: qm.TypeInteger, BitWidth: 64}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cell, err := decodeSQLCell(tc.raw, qm.OutputColumn{Type: tc.typ})
			if tc.invalid {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, cell.Data)
			assert.Equal(t, tc.typ, cell.Type)
		})
	}
}
