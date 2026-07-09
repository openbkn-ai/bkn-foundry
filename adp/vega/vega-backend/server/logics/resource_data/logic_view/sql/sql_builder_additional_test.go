// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package sql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func TestLogicViewSQLBuilderAdditional(t *testing.T) {
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	t.Run("add wheres ignores blank and string builds query", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		got := builder.AddWheres([]string{"age > 18", " ", "name IS NOT NULL"}).String()

		assert.Equal(t, "SELECT * FROM users WHERE age > 18 AND name IS NOT NULL", got)
	})

	t.Run("existing where appends before group by", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT age, COUNT(*) FROM users WHERE active = 1 GROUP BY age")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "SELECT age, COUNT(*) FROM users WHERE active = 1 AND age > 18  GROUP BY age", got)
	})

	t.Run("condition inserts before having", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT age, COUNT(*) FROM users GROUP BY age HAVING COUNT(*) > 1")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "SELECT age, COUNT(*) FROM users WHERE age > 18  GROUP BY age HAVING COUNT(*) > 1", got)
	})

	t.Run("sub query without alias wraps with default alias", func(t *testing.T) {
		builder := generator.NewSQLBuilder("(SELECT * FROM users)")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "((SELECT * FROM users)) AS subquery WHERE age > 18", got)
		assert.False(t, builder.hasAlias())
	})

	t.Run("sub query with alias keeps alias", func(t *testing.T) {
		builder := generator.NewSQLBuilder("(SELECT * FROM users) AS u")

		got := builder.AddWhere("age > 18").Build()

		assert.Equal(t, "(SELECT * FROM users) AS u WHERE age > 18", got)
		assert.True(t, builder.hasAlias())
	})

	t.Run("order by and limit append when missing", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		got := builder.OrderBy("`age` desc").Limit(5).Build()

		assert.Equal(t, "SELECT * FROM users ORDER BY `age` desc LIMIT 5", got)
	})

	t.Run("limit does not duplicate existing limit", func(t *testing.T) {
		builder := generator.NewSQLBuilder("SELECT * FROM users LIMIT 10")

		got := builder.Limit(5).Build()

		assert.Equal(t, "SELECT * FROM users LIMIT 10", got)
	})
}

func TestLogicViewSQLBuilderApplyParams(t *testing.T) {
	generator := NewlogicDefinitionSQLGenerator(testSQLView())

	t.Run("applies filter sort and standard limit", func(t *testing.T) {
		view := testSQLView()
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		err := builder.ApplyParams(context.Background(), &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Name:      "age",
				Operation: filter_condition.OperationGt,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     18,
				},
			},
			Sort:      []*interfaces.SortField{{Field: "age", Direction: interfaces.DESC_DIRECTION}},
			QueryType: interfaces.QueryType_Standard,
			Limit:     20,
		}, view)

		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM users WHERE `age` > 18 ORDER BY `age` desc LIMIT 20", builder.Build())
	})

	t.Run("stream query skips limit", func(t *testing.T) {
		view := testSQLView()
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		err := builder.ApplyParams(context.Background(), &interfaces.ResourceDataQueryParams{
			QueryType: interfaces.QueryType_Stream,
			Limit:     20,
		}, view)

		require.NoError(t, err)
		assert.Equal(t, "SELECT * FROM users", builder.Build())
	})

	t.Run("invalid filter returns error", func(t *testing.T) {
		view := testSQLView()
		builder := generator.NewSQLBuilder("SELECT * FROM users")

		err := builder.ApplyParams(context.Background(), &interfaces.ResourceDataQueryParams{
			FilterCondCfg: &interfaces.FilterCondCfg{
				Name:      "missing",
				Operation: filter_condition.OperationEqual,
				ValueOptCfg: interfaces.ValueOptCfg{
					ValueFrom: interfaces.ValueFrom_Const,
					Value:     1,
				},
			},
		}, view)

		require.Error(t, err)
		assert.ErrorContains(t, err, "not found")
	})
}
