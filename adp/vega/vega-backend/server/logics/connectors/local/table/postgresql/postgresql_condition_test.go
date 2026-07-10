// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"age":        {Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		"created_at": {Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
	}
}

func mustNewCond(t *testing.T, name, op string, value any) interfaces.FilterCondition {
	t.Helper()
	cfg := &interfaces.FilterCondCfg{
		Name:      name,
		Operation: op,
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     value,
		},
	}
	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
	require.NoError(t, err)
	return cond
}

func toSQL(t *testing.T, connector *PostgresqlConnector, cond interfaces.FilterCondition) (string, []interface{}) {
	t.Helper()
	sqlizer, err := connector.ConvertFilterCondition(context.Background(), cond, testFieldsMap())
	require.NoError(t, err)
	sql, args, err := sqlizer.ToSql()
	require.NoError(t, err)
	return sql, args
}

func TestConvertGteKeepsNonDateFieldAsParameter(t *testing.T) {
	t.Run("keeps integer field as parameter", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "age", ">=", 18)
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"age" >= ?`, sql)
		assert.Equal(t, []interface{}{18}, args)
	})
}

func TestConvertDateGteUsesToTimestamp(t *testing.T) {
	t.Run("uses to_timestamp for datetime field", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", ">=", float64(1710000000000))
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"created_at" >= to_timestamp(?/1000)`, sql)
		assert.Equal(t, []interface{}{int64(1710000000000)}, args)
	})
}

func TestConvertDateRangeUsesToTimestamp(t *testing.T) {
	t.Run("uses to_timestamp for both range bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" >= to_timestamp(?/1000)`)
		assert.Contains(t, sql, `"created_at" <= to_timestamp(?/1000)`)
		assert.Equal(t, []interface{}{int64(1710000000000), int64(1710003600000)}, args)
	})
}

func TestConvertDateOutRangeUsesToTimestamp(t *testing.T) {
	t.Run("uses to_timestamp for both out range bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "out_range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" < to_timestamp(?/1000)`)
		assert.Contains(t, sql, `"created_at" > to_timestamp(?/1000)`)
		assert.Equal(t, []interface{}{int64(1710000000000), int64(1710003600000)}, args)
	})
}

func TestConvertDateBetweenUsesToTimestamp(t *testing.T) {
	t.Run("uses to_timestamp for both between bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "between", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" >= to_timestamp(?/1000)`)
		assert.Contains(t, sql, `"created_at" <= to_timestamp(?/1000)`)
		assert.Equal(t, []interface{}{int64(1710000000000), int64(1710003600000)}, args)
	})
}

func TestPostgresqlConnectorConvertFilterConditionBefore(t *testing.T) {
	t.Run("converts before condition to interval expression", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "before", []any{float64(2), "days"})
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"created_at" < NOW() - (?::bigint * INTERVAL '1 day')`, sql)
		assert.Equal(t, []interface{}{int64(2)}, args)
	})

	t.Run("returns error for unsupported interval unit", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "before", []any{float64(2), "fortnight"})

		got, err := c.ConvertFilterCondition(context.Background(), cond, testFieldsMap())

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "unsupported interval unit")
	})
}

func TestPostgresqlConnectorConvertFilterConditionCurrent(t *testing.T) {
	t.Run("converts current day condition", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "current", filter_condition.CurrentDay)
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `date_trunc('day', "created_at"::timestamptz) = date_trunc('day', CURRENT_TIMESTAMP)`, sql)
		assert.Empty(t, args)
	})

	t.Run("returns error for unsupported format", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := &filter_condition.CurrentCond{
			Cfg:    &interfaces.FilterCondCfg{ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const}},
			Lfield: testFieldsMap()["created_at"],
			Value:  "quarter",
		}

		got, err := c.ConvertFilterCondition(context.Background(), cond, testFieldsMap())

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "unsupported format")
	})
}

func TestPgIntervalUnit(t *testing.T) {
	t.Run("maps mysql style units to postgres units", func(t *testing.T) {
		tests := map[string]string{
			" YEAR ":  "year",
			"MONTHS":  "month",
			"days":    "day",
			"HOUR":    "hour",
			"MINUTES": "minute",
			"seconds": "second",
		}
		for input, want := range tests {
			got, err := pgIntervalUnit(input)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	})

	t.Run("returns error for unsupported unit", func(t *testing.T) {
		got, err := pgIntervalUnit("week")

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "unsupported interval unit")
	})
}
