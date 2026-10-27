// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package mariadb

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/sql/compiler"
)

type unknownNode struct{ *sb.Column }

func TestToSql(t *testing.T) {
	t.Run("clauses compose in SQL parameter order", func(t *testing.T) {
		query := sb.Select(&sb.Alias{Expr: &sb.Param{Value: "projection"}, Name: "label"}, &sb.Column{Qualifier: "a", Name: "id"}).
			From(&sb.Table{SourceIdentifier: "`sales.eu`.`order.items`", Alias: "a"}).
			LeftJoin(&sb.Table{SourceIdentifier: "`customers`", Alias: "b"}, &sb.Eq{Left: &sb.Column{Qualifier: "b", Name: "id"}, Right: &sb.Param{Value: "join"}}).
			Where(&sb.Gt{Left: &sb.Column{Qualifier: "a", Name: "id"}, Right: &sb.Param{Value: "where"}}).
			GroupBy(&sb.Column{Qualifier: "a", Name: "id"}).
			Having(&sb.Gt{Left: &sb.Function{Name: "COUNT", Args: []sb.SqlNode{&sb.Star{}}}, Right: &sb.Param{Value: "having"}}).
			OrderBy(sb.Order{Expr: &sb.Param{Value: "order"}, Descending: true}).Limit(10).Offset(2)
		before, err := json.Marshal(query)
		require.NoError(t, err)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT ? AS `label`, `a`.`id` FROM `sales.eu`.`order.items` AS `a` LEFT JOIN `customers` AS `b` ON (`b`.`id` = ?) WHERE (`a`.`id` > ?) GROUP BY `a`.`id` HAVING (COUNT(*) > ?) ORDER BY ? DESC LIMIT 10 OFFSET 2", text)
		assert.Equal(t, []any{"projection", "join", "where", "having", "order"}, args)
		after, err := json.Marshal(query)
		require.NoError(t, err)
		assert.JSONEq(t, string(before), string(after))
	})
	t.Run("structured expressions compose without SQL fragments", func(t *testing.T) {
		amount := &sb.Cast{Expr: &sb.Case{Operand: &sb.Column{Name: "kind"}, WhenParts: []sb.When{{Condition: &sb.Param{Value: 1}, Result: &sb.Arithmetic{Left: &sb.Param{Value: "1.20"}, Op: sb.Add, Right: &sb.Param{Value: "0.30"}}}}, Else: &sb.Param{Value: nil}}, Type: sb.CastDecimal, Parameters: []uint64{12, 2}}
		query := sb.Select(&sb.Alias{Expr: amount, Name: "amount"}).From(&sb.Table{SourceIdentifier: "`orders`"}).
			Where(&sb.In{Expr: &sb.Column{Name: "id"}, Values: []sb.SqlNode{&sb.Param{Value: 1}, &sb.Param{Value: 2}}},
				&sb.Between{Expr: &sb.Column{Name: "cost"}, Lower: &sb.Param{Value: 3}, Upper: &sb.Param{Value: 4}},
				&sb.Like{Expr: &sb.Column{Name: "name"}, Pattern: &sb.Param{Value: "p%"}, Escape: new("!")},
				&sb.Contains{Expr: &sb.Column{Name: "note"}, Value: &sb.Param{Value: "_50%"}})
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT CAST(CASE `kind` WHEN ? THEN (? + ?) ELSE ? END AS DECIMAL(12,2)) AS `amount` FROM `orders` WHERE (`id` IN (?, ?)) AND (`cost` BETWEEN ? AND ?) AND (`name` LIKE ? ESCAPE ?) AND (`note` LIKE CONCAT('%', REPLACE(REPLACE(REPLACE(?, '!', '!!'), '%', '!%'), '_', '!_'), '%') ESCAPE '!')", text)
		assert.Equal(t, []any{1, "1.20", "0.30", nil, 1, 2, 3, 4, "p%", "!", "_50%"}, args)
	})
	t.Run("chain methods modify the same builder", func(t *testing.T) {
		query := sb.Select(&sb.Column{Name: "id"})
		query.From(&sb.Table{SourceIdentifier: "`orders`"})
		query.Columns(&sb.Alias{Expr: &sb.Param{Value: "label"}, Name: "label"})
		query.Where(&sb.Gt{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: 1}})
		query.Where(&sb.Lt{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: 10}})
		query.GroupBy(&sb.Column{Name: "id"})
		query.Having(&sb.Param{Value: true})
		query.OrderBy(sb.Order{Expr: &sb.Column{Name: "id"}})
		query.Distinct()
		query.Limit(2)
		query.Offset(1)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT `id`, ? AS `label` FROM `orders` WHERE (`id` > ?) AND (`id` < ?) GROUP BY `id` HAVING ? ORDER BY `id` ASC LIMIT 2 OFFSET 1", text)
		assert.Equal(t, []any{"label", 1, 10, true}, args)
		assert.Same(t, query, query.Having(nil).OrderBy())
		again, againArgs, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, text, again)
		assert.Equal(t, args, againArgs)
	})
	t.Run("method order follows SELECT clause semantics", func(t *testing.T) {
		condition := &sb.Eq{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: 7}}
		first := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"})
		first.Where(condition).Limit(2).Offset(1)
		second := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"})
		second.Offset(1).Limit(2).Where(condition)
		left, leftArgs, err := ToSql(first)
		require.NoError(t, err)
		right, rightArgs, err := ToSql(second)
		require.NoError(t, err)
		assert.Equal(t, left, right)
		assert.Equal(t, leftArgs, rightArgs)
	})
	t.Run("subquery retains limit and lexical parameters", func(t *testing.T) {
		inner := sb.Select(&sb.Alias{Expr: &sb.Param{Value: "inner projection"}, Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"}).
			Where(&sb.Eq{Left: &sb.Column{Name: "id"}, Right: &sb.Param{Value: "inner filter"}}).OrderBy(sb.Order{Expr: &sb.Column{Name: "id"}}).Limit(2)
		outer := sb.Select(&sb.Param{Value: "outer projection"}).FromSelect(inner, "q").Where(&sb.Eq{Left: &sb.Column{Qualifier: "q", Name: "id"}, Right: &sb.Param{Value: "outer filter"}})
		text, args, err := ToSql(outer)
		require.NoError(t, err)
		assert.Equal(t, "SELECT ? FROM (SELECT ? AS `id` FROM `orders` WHERE (`id` = ?) ORDER BY `id` ASC LIMIT 2) AS `q` WHERE (`q`.`id` = ?)", text)
		assert.Equal(t, []any{"outer projection", "inner projection", "inner filter", "outer filter"}, args)
	})
	t.Run("zero limit offset only and empty conditions", func(t *testing.T) {
		query := sb.Select(&sb.Param{Value: 1})
		query.Where(nil).Having(nil).Distinct().Limit(0)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT ? LIMIT 0", text)
		assert.Equal(t, []any{1}, args)
		query.RemoveLimit().Offset(4)
		text, _, err = ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT ? LIMIT 18446744073709551615 OFFSET 4", text)
		query.Limit(0).RemoveLimit().RemoveOffset()
		text, _, err = ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, "SELECT DISTINCT ?", text)
	})
	t.Run("malformed inputs produce no partial SQL", func(t *testing.T) {
		var missing *sb.Column
		var missingTable *sb.Table
		var missingCondition *sb.Eq
		var missingParam *sb.Param
		var missingAnd *sb.And
		for _, query := range []*sb.SelectBuilder{
			nil, {}, sb.Select(nil), sb.Select(missing), sb.Select(&sb.Star{}).From(&sb.Table{ResourceID: "unresolved"}),
			sb.Select(unknownNode{}),
			sb.Select(&sb.Star{}).From(missingTable),
			sb.Select(&sb.Param{Value: 1}).Where(missingCondition),
			sb.Select(missingParam),
			sb.Select(&sb.Param{Value: 1}).Having(missingAnd),
			sb.Select(&sb.Param{Value: 1}).OrderBy(sb.Order{Expr: missing}),
			sb.Select(&sb.Star{}).LeftJoin(&sb.Table{SourceIdentifier: "`b`"}, &sb.Param{Value: true}),
			sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`a`"}).Join(&sb.Table{SourceIdentifier: "`b`"}, nil),
			{ColumnsPart: []sb.SqlNode{&sb.Column{Name: "id"}}, WherePart: []sb.SqlNode{nil}},
		} {
			text, args, err := ToSql(query)
			require.Error(t, err)
			assert.Empty(t, text)
			assert.Nil(t, args)
		}
		text, args, err := render(sb.Select(&sb.Param{Value: 1}).Where(&sb.Column{}))
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	})
	t.Run("join convenience methods retain their connection kinds", func(t *testing.T) {
		text, args, err := render(sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`a`"}).
			Join(&sb.Table{SourceIdentifier: "`b`"}, &sb.Param{Value: 1}).
			RightJoin(&sb.Table{SourceIdentifier: "`c`"}, &sb.Param{Value: 2}))
		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM `a` INNER JOIN `b` ON ? RIGHT JOIN `c` ON ?", text)
		assert.Equal(t, []any{1, 2}, args)
	})
}

func TestTableToSql(t *testing.T) {
	text, args, err := render((&sb.Table{SourceIdentifier: "`a.b`.`x``y`", Alias: "q`r"}))
	require.NoError(t, err)
	assert.Equal(t, "`a.b`.`x``y` AS `q``r`", text)
	assert.Empty(t, args)
	text, args, err = render(&sb.Table{SourceIdentifier: "orders"})
	require.NoError(t, err)
	assert.Equal(t, "orders", text)
	assert.Empty(t, args)

}

func TestSubqueryToSql(t *testing.T) {
	inner := sb.Select(&sb.Param{Value: 7})
	outer := sb.Select(&sb.Star{}).FromSelect(inner, "q")
	assert.Same(t, inner, outer.FromPart.(*sb.Subquery).Query)
	inner.Where(&sb.Param{Value: true})
	text, args, err := ToSql(outer)
	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM (SELECT ? WHERE ?) AS `q`", text)
	assert.Equal(t, []any{7, true}, args)
	for _, subquery := range []*sb.Subquery{{}, {Query: &sb.SelectBuilder{}}} {
		text, args, err := render(subquery)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
	invalid := sb.Select()
	text, args, err = render((&sb.Subquery{Query: invalid, Alias: "q"}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestJoinToSql(t *testing.T) {
	for _, tc := range []struct {
		kind    sb.JoinKind
		keyword string
	}{{sb.InnerJoin, "INNER JOIN"}, {sb.LeftJoin, "LEFT JOIN"}, {sb.RightJoin, "RIGHT JOIN"}, {sb.CrossJoin, "CROSS JOIN"}} {
		join := sb.Join{Kind: tc.kind, Source: &sb.Table{SourceIdentifier: "`b`"}}
		want := tc.keyword + " `b`"
		var expected []any
		if tc.kind != sb.CrossJoin {
			join.On = &sb.Param{Value: true}
			want += " ON ?"
			expected = []any{true}
		}
		text, args, err := render(&join)
		require.NoError(t, err)
		assert.Equal(t, want, text)
		assert.Equal(t, expected, args)
	}
	for _, join := range []sb.Join{{Kind: "invalid", Source: &sb.Table{SourceIdentifier: "`b`"}}, {Kind: sb.CrossJoin, Source: &sb.Table{SourceIdentifier: "`b`"}, On: &sb.Param{Value: true}}} {
		text, args, err := render(&join)
		require.Error(t, err)
		assert.Empty(t, text)
		assert.Nil(t, args)
	}
}

func TestJoinedTableToSql(t *testing.T) {
	source := &sb.JoinedTable{From: &sb.Table{SourceIdentifier: "`b`"}, Joins: []sb.Join{{Kind: sb.InnerJoin, Source: &sb.Table{SourceIdentifier: "`c`"}, On: &sb.Eq{Left: &sb.Column{Qualifier: "b", Name: "id"}, Right: &sb.Param{Value: 2}}}}}
	text, args, err := render(sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`a`"}).LeftJoin(source, &sb.Eq{Left: &sb.Column{Qualifier: "a", Name: "id"}, Right: &sb.Param{Value: 3}}))
	require.NoError(t, err)
	assert.Equal(t, "SELECT * FROM `a` LEFT JOIN (`b` INNER JOIN `c` ON (`b`.`id` = ?)) ON (`a`.`id` = ?)", text)
	assert.Equal(t, []any{2, 3}, args)
	text, args, err = render((&sb.JoinedTable{}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestOrderToSql(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order sb.Order
		want  string
		args  []any
	}{
		{"default asc", sb.Order{Expr: &sb.Column{Name: "id"}}, "`id` ASC", nil},
		{"default desc", sb.Order{Expr: &sb.Column{Name: "id"}, Descending: true}, "`id` DESC", nil},
		{"null first", sb.Order{Expr: &sb.Param{Value: 9}, Descending: true, Nulls: sb.NullsFirst}, "(? IS NULL) DESC, ? DESC", []any{9, 9}},
		{"null last", sb.Order{Expr: &sb.Param{Value: 9}, Nulls: sb.NullsLast}, "(? IS NULL) ASC, ? ASC", []any{9, 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, args, err := render(&tc.order)
			require.NoError(t, err)
			assert.Equal(t, tc.want, text)
			assert.Equal(t, tc.args, args)
		})
	}
	text, args, err := render((&sb.Order{Expr: &sb.Param{Value: 1}, Nulls: "invalid"}))
	require.Error(t, err)
	assert.Empty(t, text)
	assert.Nil(t, args)
}

func TestMariaDBConnectorExecuteSQLBuilder(t *testing.T) {
	for _, tc := range []struct {
		name, input, statement string
		args                   []driver.Value
	}{
		{"single table", "SELECT id,name FROM {{orders}} WHERE id>10 ORDER BY id LIMIT 2", "SELECT `id`, `name` FROM `orders` WHERE (`id` > ?) ORDER BY `id` ASC LIMIT 2", []driver.Value{int64(10)}},
		{"join", "SELECT a.id,b.name FROM {{orders}} a LEFT JOIN {{orders}} b ON a.id=b.id WHERE b.name='O''Reilly'", "SELECT `a`.`id`, `b`.`name` FROM `orders` AS `a` LEFT JOIN `orders` AS `b` ON (`a`.`id` = `b`.`id`) WHERE (`b`.`name` = ?)", []driver.Value{"O'Reilly"}},
		{"subquery limit", "SELECT q.id,q.name FROM (SELECT id,name FROM {{orders}} WHERE id>1 ORDER BY id LIMIT 2) q WHERE q.id<10", "SELECT `q`.`id`, `q`.`name` FROM (SELECT `id`, `name` FROM `orders` WHERE (`id` > ?) ORDER BY `id` ASC LIMIT 2) AS `q` WHERE (`q`.`id` < ?)", []driver.Value{int64(1), int64(10)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() {
				mock.ExpectClose()
				require.NoError(t, db.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
			connector := &MariaDBConnector{db: db, connected: true}
			query, err := compiler.ParseSQL(tc.input)
			require.NoError(t, err)
			// 模拟准备阶段填入已授权资源的 SourceIdentifier，字段保持解析结果。
			switch from := query.FromPart.(type) {
			case *sb.Table:
				from.SourceIdentifier = "`orders`"
			case *sb.Subquery:
				from.Query.FromPart.(*sb.Table).SourceIdentifier = "`orders`"
			}
			for _, join := range query.JoinsPart {
				join.Source.(*sb.Table).SourceIdentifier = "`orders`"
			}
			before, err := json.Marshal(query)
			require.NoError(t, err)
			mock.ExpectQuery(tc.statement).WithArgs(tc.args...).WillReturnRows(sqlmock.NewRowsWithColumnDefinition(
				sqlmock.NewColumn("id").OfType("BIGINT", int64(0)),
				sqlmock.NewColumn("name").OfType("VARCHAR", ""),
			).AddRow(int64(11), []byte("alice")).AddRow(int64(12), nil)).RowsWillBeClosed()
			result, err := connector.ExecuteSQLBuilder(context.Background(), query)
			require.NoError(t, err)
			assert.Equal(t, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}, {Name: "name", Type: interfaces.DataType_String}}, result.Columns)
			assert.Equal(t, []map[string]any{{"id": int64(11), "name": "alice"}, {"id": int64(12), "name": nil}}, result.Entries)
			require.NotNil(t, result.TotalCount)
			assert.EqualValues(t, 2, *result.TotalCount)
			after, err := json.Marshal(query)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
	t.Run("generation and cancellation stop before connecting", func(t *testing.T) {
		connector := &MariaDBConnector{}
		result, err := connector.ExecuteSQLBuilder(context.Background(), sb.Select())
		assert.Nil(t, result)
		require.Error(t, err)
		assert.False(t, connector.connected)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err = connector.ExecuteSQLBuilder(ctx, sb.Select(&sb.Param{Value: 1}))
		assert.Nil(t, result)
		assert.ErrorIs(t, err, context.Canceled)
		assert.False(t, connector.connected)
	})
	t.Run("empty results retain columns and zero returned count", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		t.Cleanup(func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
		mock.ExpectQuery("SELECT `id` FROM `orders`").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(sqlmock.NewColumn("id").OfType("BIGINT", int64(0)))).RowsWillBeClosed()
		connector := &MariaDBConnector{db: db, connected: true}
		query := sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"})
		result, err := connector.ExecuteSQLBuilder(context.Background(), query)
		require.NoError(t, err)
		assert.Equal(t, []interfaces.ColumnInfo{{Name: "id", Type: interfaces.DataType_Integer}}, result.Columns)
		assert.NotNil(t, result.Entries)
		assert.Empty(t, result.Entries)
		require.NotNil(t, result.TotalCount)
		assert.Zero(t, *result.TotalCount)
	})
	t.Run("duplicate columns do not overwrite object values", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		require.NoError(t, err)
		t.Cleanup(func() {
			mock.ExpectClose()
			require.NoError(t, db.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
		mock.ExpectQuery("SELECT * FROM `orders`").WillReturnRows(sqlmock.NewRows([]string{"id", "id"}).AddRow(1, 2)).RowsWillBeClosed()
		connector := &MariaDBConnector{db: db, connected: true}
		result, err := connector.ExecuteSQLBuilder(context.Background(), sb.Select(&sb.Star{}).From(&sb.Table{SourceIdentifier: "`orders`"}))
		require.Error(t, err)
		assert.Nil(t, result)
		assert.ErrorContains(t, err, "duplicate column names")
	})
	for _, failure := range []struct {
		name       string
		duringRows bool
		cause      error
	}{
		{"query unknown column", false, &mysqldriver.MySQLError{Number: 1054, Message: "private source detail"}},
		{"iteration unknown column", true, &mysqldriver.MySQLError{Number: 1054, Message: "private source detail"}},
		{"query cancellation", false, context.Canceled},
		{"iteration cancellation", true, context.Canceled},
	} {
		t.Run(failure.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			t.Cleanup(func() {
				mock.ExpectClose()
				require.NoError(t, db.Close())
				require.NoError(t, mock.ExpectationsWereMet())
			})
			expectation := mock.ExpectQuery("SELECT `id` FROM `orders`")
			if failure.duringRows {
				expectation.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2).RowError(1, failure.cause)).RowsWillBeClosed()
			} else {
				expectation.WillReturnError(failure.cause)
			}
			connector := &MariaDBConnector{db: db, connected: true}
			result, err := connector.ExecuteSQLBuilder(context.Background(), sb.Select(&sb.Column{Name: "id"}).From(&sb.Table{SourceIdentifier: "`orders`"}))
			require.Error(t, err)
			assert.Nil(t, result)
			assert.ErrorIs(t, err, failure.cause)
			var driverErr *mysqldriver.MySQLError
			if errors.As(failure.cause, &driverErr) {
				var invalid *interfaces.SourceQueryInvalidParameterError
				require.ErrorAs(t, err, &invalid)
				assert.Equal(t, interfaces.SourceQueryInvalidParameterUnknownColumn, invalid.Reason)
			}
		})
	}
}

func TestSQLBuilderArguments(t *testing.T) {
	args := []any{json.Number("1"), json.Number("18446744073709551615"), json.Number("1.2300"), json.Number("1e-3"), "1", nil}
	values, err := sqlBuilderArguments(args)
	require.NoError(t, err)
	assert.Equal(t, []any{int64(1), uint64(18446744073709551615), "1.2300", float64(0.001), "1", nil}, values)
	assert.Equal(t, json.Number("1"), args[0])
	_, err = sqlBuilderArguments([]any{json.Number("1e10000")})
	require.Error(t, err)
}
