// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vega-backend/interfaces"
	"vega-backend/logics/filter_condition"
)

func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"age":        {Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		"created_at": {Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		"event_time": {Name: "event_time", OriginalName: "event_time", OriginalType: "time", Type: interfaces.DataType_Time},
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
		cond := mustNewCond(t, "created_at", ">=", json.Number("1710000000000"))
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"created_at" >= to_timestamp(?::double precision / 1000.0)`, sql)
		assert.Equal(t, []interface{}{int64(1710000000000)}, args)
	})
}

func TestConvertDateEqualityUsesToTimestamp(t *testing.T) {
	c := &PostgresqlConnector{}

	t.Run("uses to_timestamp for equal", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "==", float64(1785295334428))
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"created_at" = to_timestamp(?::double precision / 1000.0)`, sql)
		assert.Equal(t, []interface{}{int64(1785295334428)}, args)
	})

	t.Run("uses to_timestamp for not equal", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "!=", float64(1785295334428))
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"created_at" <> to_timestamp(?::double precision / 1000.0)`, sql)
		assert.Equal(t, []interface{}{int64(1785295334428)}, args)
	})
}

func TestConvertDateSetMembershipUsesToTimestamp(t *testing.T) {
	c := &PostgresqlConnector{}
	values := []any{1785295334428, 1785381734428}

	t.Run("uses to_timestamp for in", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "in", values)
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"created_at" IN (to_timestamp(?::double precision / 1000.0), to_timestamp(?::double precision / 1000.0))`, sql)
		assert.Equal(t, []interface{}{int64(1785295334428), int64(1785381734428)}, args)
	})

	t.Run("uses to_timestamp for not in", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "not_in", values)
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"created_at" NOT IN (to_timestamp(?::double precision / 1000.0), to_timestamp(?::double precision / 1000.0))`, sql)
		assert.Equal(t, []interface{}{int64(1785295334428), int64(1785381734428)}, args)
	})
}

func TestPostgresqlDateCompareExprUsesSessionTimezoneForTimestampWithoutTimezone(t *testing.T) {
	for _, originalType := range []string{"timestamp", "timestamp without time zone"} {
		t.Run(originalType, func(t *testing.T) {
			field := &interfaces.Property{
				OriginalName: "created_at",
				OriginalType: originalType,
			}

			expr, err := postgresqlDateCompareExpr(field, ">=", float64(1785295334428))
			require.NoError(t, err)
			sql, args, err := expr.ToSql()
			require.NoError(t, err)
			assert.Equal(t, `"created_at" >= (to_timestamp(?::double precision / 1000.0) AT TIME ZONE current_setting('TimeZone'))`, sql)
			assert.Equal(t, []interface{}{int64(1785295334428)}, args)
		})
	}
}

func TestPostgresqlDateExpressionsKeepCursorTimestampsNative(t *testing.T) {
	wantMillis := int64(1785295334428)
	wantTime := time.UnixMilli(wantMillis).UTC()

	tests := []struct {
		originalType string
		fieldType    string
		wantSQL      string
	}{
		{originalType: "timestamp", fieldType: interfaces.DataType_Timestamp, wantSQL: `"created_at" > ?::timestamp`},
		{originalType: "date", fieldType: interfaces.DataType_Date, wantSQL: `"created_at" > ?::timestamp`},
		{originalType: "timestamptz", fieldType: interfaces.DataType_Timestamp, wantSQL: `"created_at" > ?`},
	}
	for _, test := range tests {
		field := &interfaces.Property{
			Name:         "created_at",
			OriginalName: "created_at",
			OriginalType: test.originalType,
			Type:         test.fieldType,
		}
		for name, value := range map[string]any{
			"time.Time": wantTime,
			"RFC3339":   wantTime.Format(time.RFC3339Nano),
		} {
			t.Run(test.originalType+"/"+name, func(t *testing.T) {
				expr, err := postgresqlDateCompareExpr(field, ">", value)
				require.NoError(t, err)
				sql, args, err := expr.ToSql()
				require.NoError(t, err)
				assert.Equal(t, test.wantSQL, sql)
				assert.Equal(t, []interface{}{wantTime}, args)
			})
		}
	}
}

func TestPostgresqlDateExpressionsKeepTimeOfDayValuesRaw(t *testing.T) {
	t.Run("uses semantic type when original type is absent", func(t *testing.T) {
		field := &interfaces.Property{
			Name:         "event_time",
			OriginalName: "event_time",
			Type:         interfaces.DataType_Time,
		}

		expr, err := postgresqlDateCompareExpr(field, ">=", "14:30:00")
		require.NoError(t, err)
		sql, args, err := expr.ToSql()
		require.NoError(t, err)
		assert.Equal(t, `"event_time" >= ?`, sql)
		assert.Equal(t, []interface{}{"14:30:00"}, args)
	})

	for _, originalType := range []string{"time", "timetz", "time without time zone", "time with time zone"} {
		t.Run(originalType, func(t *testing.T) {
			field := &interfaces.Property{
				OriginalName: "event_time",
				OriginalType: originalType,
			}

			expr, err := postgresqlDateCompareExpr(field, ">=", "14:30:00")
			require.NoError(t, err)
			sql, args, err := expr.ToSql()
			require.NoError(t, err)
			assert.Equal(t, `"event_time" >= ?`, sql)
			assert.Equal(t, []interface{}{"14:30:00"}, args)
		})
	}

	t.Run("keeps time set values raw", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "event_time", "in", []any{"14:30:00", "16:45:00"})
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `"event_time" IN (?, ?)`, sql)
		assert.Equal(t, []interface{}{"14:30:00", "16:45:00"}, args)
	})

	t.Run("rejects numeric time values", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "event_time", ">=", float64(1785295334428))

		expr, err := c.ConvertFilterCondition(context.Background(), cond, testFieldsMap())
		require.Error(t, err)
		assert.Nil(t, expr)
		assert.Contains(t, err.Error(), "requires a time string")
	})
}

func TestConvertDateRangeUsesToTimestamp(t *testing.T) {
	t.Run("uses to_timestamp for both range bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" >= to_timestamp(?::double precision / 1000.0)`)
		assert.Contains(t, sql, `"created_at" <= to_timestamp(?::double precision / 1000.0)`)
		assert.Equal(t, []interface{}{int64(1710000000000), int64(1710003600000)}, args)
	})
}

func TestConvertDateOutRangeUsesToTimestamp(t *testing.T) {
	t.Run("uses to_timestamp for both out range bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "out_range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" < to_timestamp(?::double precision / 1000.0)`)
		assert.Contains(t, sql, `"created_at" > to_timestamp(?::double precision / 1000.0)`)
		assert.Equal(t, []interface{}{int64(1710000000000), int64(1710003600000)}, args)
	})
}

func TestConvertDateBetweenUsesToTimestamp(t *testing.T) {
	t.Run("uses to_timestamp for both between bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "between", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" >= to_timestamp(?::double precision / 1000.0)`)
		assert.Contains(t, sql, `"created_at" <= to_timestamp(?::double precision / 1000.0)`)
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
