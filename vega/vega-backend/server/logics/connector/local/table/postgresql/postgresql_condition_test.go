// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package postgresql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

// testFieldsMap provides properties for filter-conversion tests.
func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"age":        {Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		"created_at": {Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		"event_time": {Name: "event_time", OriginalName: "event_time", OriginalType: "time", Type: interfaces.DataType_Time},
	}
}

func TestBooleanConditionRejectsNumericField(t *testing.T) {
	fields := testFieldsMap()
	for _, operation := range []string{"true", "false"} {
		condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "age", Operation: operation}, fields)
		require.NoError(t, err)
		_, err = (&PostgresqlConnector{}).ConvertFilterCondition(condition)
		require.ErrorContains(t, err, "requires BOOLEAN")
	}
}

// mustNewCond compiles a filter condition and fails the test on invalid input.
func mustNewCond(t *testing.T, name, op string, value any) interfaces.FilterCondition {
	t.Helper()
	cfg := &interfaces.FilterCondCfg{
		Name:      name,
		Operation: op,
		ValueFrom: interfaces.ValueFrom_Const,
		Value:     value,
	}
	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
	require.NoError(t, err)
	return cond
}

// renderFilterSQL 使用生产 PG 生成器输出单个节点，便于核对谓词和参数。
func renderFilterSQL(node sb.SqlNode) (string, []any, error) {
	text, args, err := ToSql(sb.Select(node))
	return strings.TrimPrefix(text, "SELECT "), args, err
}

// toSQL renders a converted filter and returns its bound arguments.
func toSQL(t *testing.T, connector *PostgresqlConnector, cond interfaces.FilterCondition) (string, []interface{}) {
	t.Helper()
	sqlizer, err := connector.ConvertFilterCondition(cond)
	require.NoError(t, err)
	sql, args, err := renderFilterSQL(sqlizer)
	require.NoError(t, err)
	return sql, args
}

// TestPostgresqlConnectorConvertFilterCondition 校验 LIKE 参数绑定仅转义模式字符，不额外转义 SQL 字面量。
func TestPostgresqlConnectorConvertFilterCondition(t *testing.T) {
	t.Run("structured decimal parameters preserve scientific notation exactly", func(t *testing.T) {
		field := &interfaces.Property{Name: "amount", OriginalName: "amount", Type: interfaces.DataType_Decimal}
		for _, value := range []json.Number{"10.1234567890", "10.123456789000000001e0"} {
			cfg := &interfaces.FilterCondCfg{Name: "amount", Operation: "==", Value: value}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"amount": field})
			require.NoError(t, err)
			text, args := toSQL(t, &PostgresqlConnector{}, cond)
			assert.Equal(t, `("amount" = $1)`, text)
			assert.Equal(t, []any{value.String()}, args)
			assert.Equal(t, value, cfg.Value)
		}
	})

	t.Run("conditions compose directly into the shared Builder", func(t *testing.T) {
		cond := mustNewCond(t, "age", ">=", 18)
		predicate, err := (&PostgresqlConnector{}).ConvertFilterCondition(cond)
		require.NoError(t, err)
		query := sb.Select(&sb.Column{Name: "age"}).
			From(&sb.Table{SourceIdentifier: `"public"."people"`}).
			Where(predicate)
		text, args, err := ToSql(query)
		require.NoError(t, err)
		assert.Equal(t, `SELECT "age" FROM "public"."people" WHERE ("age" >= CAST($1 AS BIGINT))`, text)
		assert.Equal(t, []any{18}, args)
	})

	field := &interfaces.Property{Name: "name", OriginalName: "name", Type: interfaces.DataType_String}
	for _, operation := range []struct{ op, sql, prefix, suffix string }{
		{"like", `("name" LIKE $1)`, "%", "%"},
		{"not_like", `("name" NOT LIKE $1)`, "%", "%"},
		{"prefix", `("name" LIKE $1)`, "", "%"},
		{"not_prefix", `("name" NOT LIKE $1)`, "", "%"},
	} {
		t.Run(operation.op, func(t *testing.T) {
			for _, value := range []struct{ name, likeInput, prefixInput, pattern string }{
				{"plain", "plain", "plain", "plain"},
				{"backslash", `\one`, `\one`, `\\one`},
				{"single quote", "O'Reilly", "O'Reilly", "O'Reilly"},
				{"literal wildcards", `\%_`, "%_", `\%\_`},
				{"mixed", `O'Reilly\\\%_`, `O'Reilly\%_`, `O'Reilly\\\%\_`},
			} {
				t.Run(value.name, func(t *testing.T) {
					// prefix 输入本身就是字面前缀；只有 like/not_like 使用输入转义协议。
					input := value.likeInput
					if operation.op == "prefix" || operation.op == "not_prefix" {
						input = value.prefixInput
					}
					cfg := &interfaces.FilterCondCfg{Name: "name", Operation: operation.op, ValueFrom: interfaces.ValueFrom_Const, Value: input}
					cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"name": field})
					require.NoError(t, err)
					sql, args := toSQL(t, &PostgresqlConnector{}, cond)
					assert.Equal(t, operation.sql, sql)
					assert.Equal(t, []any{operation.prefix + value.pattern + operation.suffix}, args)
					assert.Equal(t, input, cfg.Value, "filter input must remain unchanged")
				})
			}
		})
	}
}

func TestPostgresqlConnectorConvertFilterConditionPrefix(t *testing.T) {
	field := &interfaces.Property{Name: "name", OriginalName: "name", Type: interfaces.DataType_String}

	t.Run("convert constant prefix", func(t *testing.T) {
		cond := &filter_condition.PrefixCond{
			Cfg:    &interfaces.FilterCondCfg{ValueFrom: interfaces.ValueFrom_Const},
			Lfield: field, Value: "ali",
		}
		expr, err := (&PostgresqlConnector{}).ConvertFilterConditionPrefix(cond)
		require.NoError(t, err)
		sql, args, err := renderFilterSQL(expr)
		require.NoError(t, err)
		assert.Equal(t, `("name" LIKE $1)`, sql)
		assert.Equal(t, []any{"ali%"}, args)
	})
	t.Run("reject field source", func(t *testing.T) {
		cond := &filter_condition.PrefixCond{
			Cfg:    &interfaces.FilterCondCfg{ValueFrom: interfaces.ValueFrom_Field},
			Lfield: field, Value: "ali",
		}
		expr, err := cond.New(context.Background(), &interfaces.FilterCondCfg{Name: "name", ValueFrom: interfaces.ValueFrom_Field, Value: "ali"}, map[string]*interfaces.Property{"name": field})
		require.ErrorContains(t, err, "value_from")
		assert.Nil(t, expr)
	})
}

func TestConvertGteKeepsNonDateFieldAsParameter(t *testing.T) {
	t.Run("keeps integer field as parameter", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "age", ">=", 18)
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("age" >= CAST($1 AS BIGINT))`, sql)
		assert.Equal(t, []interface{}{18}, args)
	})
}

func TestConvertDateGteBindsNormalizedDatetime(t *testing.T) {
	t.Run("binds normalized datetime for datetime field", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", ">=", json.Number("1710000000000"))
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("created_at" >= CAST($1 AS TIMESTAMP))`, sql)
		assert.Equal(t, []interface{}{time.UnixMilli(1710000000000).UTC()}, args)
	})
}

func TestConvertDateEqualityBindsNormalizedDatetime(t *testing.T) {
	c := &PostgresqlConnector{}

	t.Run("binds normalized datetime for equal", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "==", float64(1785295334428))
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("created_at" = CAST($1 AS TIMESTAMP))`, sql)
		assert.Equal(t, []interface{}{time.UnixMilli(1785295334428).UTC()}, args)
	})

	t.Run("binds normalized datetime for not equal", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "!=", float64(1785295334428))
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("created_at" <> CAST($1 AS TIMESTAMP))`, sql)
		assert.Equal(t, []interface{}{time.UnixMilli(1785295334428).UTC()}, args)
	})
}

func TestConvertDateSetMembershipBindsNormalizedDatetime(t *testing.T) {
	c := &PostgresqlConnector{}
	values := []any{1785295334428, 1785381734428}

	t.Run("binds normalized datetime for in", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "in", values)
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("created_at" IN (CAST($1 AS TIMESTAMP), CAST($2 AS TIMESTAMP)))`, sql)
		assert.Equal(t, []interface{}{time.UnixMilli(1785295334428).UTC(), time.UnixMilli(1785381734428).UTC()}, args)
	})

	t.Run("binds normalized datetime for not in", func(t *testing.T) {
		cond := mustNewCond(t, "created_at", "not_in", values)
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("created_at" NOT IN (CAST($1 AS TIMESTAMP), CAST($2 AS TIMESTAMP)))`, sql)
		assert.Equal(t, []interface{}{time.UnixMilli(1785295334428).UTC(), time.UnixMilli(1785381734428).UTC()}, args)
	})
}

func TestSQLComparisonUsesSessionTimezoneForTimestampWithoutTimezone(t *testing.T) {
	for _, originalType := range []string{"timestamp", "timestamp without time zone"} {
		t.Run(originalType, func(t *testing.T) {
			field := &interfaces.Property{
				OriginalName: "created_at",
				OriginalType: originalType,
				Type:         interfaces.DataType_Timestamp,
			}

			expr, err := sqlComparison(field, nil, float64(1785295334428), ">=")
			require.NoError(t, err)
			sql, args, err := renderFilterSQL(expr)
			require.NoError(t, err)
			assert.Equal(t, "(\"created_at\" >= CAST(TO_TIMESTAMP(CAST((CAST(CAST($1 AS BIGINT) AS NUMERIC) / CAST(CAST(1000 AS BIGINT) AS NUMERIC)) AS DOUBLE PRECISION)) AS TIMESTAMP))", sql)
			assert.Equal(t, []any{int64(1785295334428)}, args)
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
		{originalType: "timestamp", fieldType: interfaces.DataType_Timestamp, wantSQL: `("created_at" > CAST($1 AS TIMESTAMP))`},
		{originalType: "date", fieldType: interfaces.DataType_Date, wantSQL: `("created_at" > CAST($1 AS DATE))`},
		{originalType: "timestamptz", fieldType: interfaces.DataType_Timestamp, wantSQL: `("created_at" > $1)`},
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
				expr, err := sqlComparison(field, nil, value, ">")
				if test.fieldType == interfaces.DataType_Date && name == "RFC3339" {
					require.ErrorContains(t, err, "invalid date value")
					assert.Nil(t, expr)
					return
				}
				require.NoError(t, err)
				sql, args, err := renderFilterSQL(expr)
				require.NoError(t, err)
				assert.Equal(t, test.wantSQL, sql)
				if test.fieldType == interfaces.DataType_Date {
					assert.Equal(t, []any{wantTime.Format(time.DateOnly)}, args)
				} else {
					assert.Equal(t, []interface{}{wantTime}, args)
				}
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

		expr, err := sqlComparison(field, nil, "14:30:00", ">=")
		require.NoError(t, err)
		sql, args, err := renderFilterSQL(expr)
		require.NoError(t, err)
		assert.Equal(t, `("event_time" >= $1)`, sql)
		assert.Equal(t, []interface{}{"14:30:00"}, args)
	})

	for _, originalType := range []string{"time", "timetz", "time without time zone", "time with time zone"} {
		t.Run(originalType, func(t *testing.T) {
			field := &interfaces.Property{
				OriginalName: "event_time",
				OriginalType: originalType,
				Type:         interfaces.DataType_Time,
			}

			expr, err := sqlComparison(field, nil, "14:30:00", ">=")
			require.NoError(t, err)
			sql, args, err := renderFilterSQL(expr)
			require.NoError(t, err)
			assert.Equal(t, `("event_time" >= $1)`, sql)
			assert.Equal(t, []interface{}{"14:30:00"}, args)
		})
	}

	t.Run("keeps time set values raw", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "event_time", "in", []any{"14:30:00", "16:45:00"})
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("event_time" IN ($1, $2))`, sql)
		assert.Equal(t, []interface{}{"14:30:00", "16:45:00"}, args)
	})

	t.Run("rejects numeric time values before conversion", func(t *testing.T) {
		cond, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "event_time", Operation: ">=", Value: float64(1785295334428)}, testFieldsMap())
		require.ErrorContains(t, err, "invalid time value")
		assert.Nil(t, cond)
	})
}

func TestConvertDateRangeBindsNormalizedDatetime(t *testing.T) {
	t.Run("binds normalized datetime for both range bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" >= CAST($1 AS TIMESTAMP)`)
		assert.Contains(t, sql, `"created_at" <= CAST($2 AS TIMESTAMP)`)
		assert.Equal(t, []interface{}{time.UnixMilli(1710000000000).UTC(), time.UnixMilli(1710003600000).UTC()}, args)
	})
}

func TestConvertDateOutRangeBindsNormalizedDatetime(t *testing.T) {
	t.Run("binds normalized datetime for both out range bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "out_range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" < CAST($1 AS TIMESTAMP)`)
		assert.Contains(t, sql, `"created_at" > CAST($2 AS TIMESTAMP)`)
		assert.Equal(t, []interface{}{time.UnixMilli(1710000000000).UTC(), time.UnixMilli(1710003600000).UTC()}, args)
	})
}

func TestConvertDateBetweenBindsNormalizedDatetime(t *testing.T) {
	t.Run("binds normalized datetime for both between bounds", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "between", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)

		assert.Contains(t, sql, `"created_at" >= CAST($1 AS TIMESTAMP)`)
		assert.Contains(t, sql, `"created_at" <= CAST($2 AS TIMESTAMP)`)
		assert.Equal(t, []interface{}{time.UnixMilli(1710000000000).UTC(), time.UnixMilli(1710003600000).UTC()}, args)
	})
}

func TestPostgresqlConnectorConvertFilterConditionBefore(t *testing.T) {
	for _, fieldType := range []string{interfaces.DataType_Date, interfaces.DataType_Datetime} {
		for _, unit := range []string{"week", "weeks", " WEEKS "} {
			t.Run(fieldType+"/"+unit, func(t *testing.T) {
				field := &interfaces.Property{Name: "created", OriginalName: "created_at", Type: fieldType}
				cond, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{
					Name: "created", Operation: "before", ValueFrom: interfaces.ValueFrom_Const,
					Value: []any{json.Number("3"), unit},
				}, map[string]*interfaces.Property{"created": field})
				require.NoError(t, err)
				sql, args := toSQL(t, &PostgresqlConnector{}, cond)
				assert.Equal(t, `("created_at" < (TRANSACTION_TIMESTAMP() - (CAST(CAST($1 AS BIGINT) AS DOUBLE PRECISION) * INTERVAL '1 week')))`, sql)
				assert.Equal(t, []any{int64(3)}, args)
			})
		}
	}

	t.Run("converts before condition to interval expression", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := &filter_condition.BeforeCond{
			Cfg: &interfaces.FilterCondCfg{
				ValueFrom: interfaces.ValueFrom_Const,
			},
			Lfield: testFieldsMap()["created_at"],
			Value:  []any{json.Number("2"), "days"},
		}
		sql, args := toSQL(t, c, cond)

		assert.Equal(t, `("created_at" < (TRANSACTION_TIMESTAMP() - (CAST(CAST($1 AS BIGINT) AS DOUBLE PRECISION) * INTERVAL '1 day')))`, sql)
		assert.Equal(t, []interface{}{int64(2)}, args)
	})

	t.Run("returns error for unsupported interval unit", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := mustNewCond(t, "created_at", "before", []any{float64(2), "fortnight"})

		got, err := c.ConvertFilterCondition(cond)

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

		assert.Equal(t, "(DATE_TRUNC('day', \"created_at\") = DATE_TRUNC('day', TRANSACTION_TIMESTAMP()))", sql)
		assert.Equal(t, []any(nil), args)
	})

	t.Run("returns error for unsupported format", func(t *testing.T) {
		c := &PostgresqlConnector{}
		cond := &filter_condition.CurrentCond{
			Cfg:    &interfaces.FilterCondCfg{ValueFrom: interfaces.ValueFrom_Const},
			Lfield: testFieldsMap()["created_at"],
			Value:  "quarter",
		}

		got, err := c.ConvertFilterCondition(cond)

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
			"week":    "week",
			"WEEKS":   "week",
			" Week ":  "week",
		}
		for input, want := range tests {
			got, err := intervalUnit(input)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	})

	t.Run("returns error for unsupported unit", func(t *testing.T) {
		got, err := intervalUnit("fortnight")

		require.Error(t, err)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), "unsupported interval unit")
	})
}

func TestPostgresqlConnectorTemporalInputContract(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	t.Run("timezone-aware source column retains the instant", func(t *testing.T) {
		field := &interfaces.Property{Name: "value", OriginalName: "value", Type: interfaces.DataType_Datetime, OriginalType: "timestamp with time zone"}
		cfg := &interfaces.FilterCondCfg{Name: "value", Operation: "==", Value: "2026-10-19 00:00:00"}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"value": field})
		require.NoError(t, err)
		expr, err := (&PostgresqlConnector{}).ConvertFilterCondition(cond)
		require.NoError(t, err)
		_, args, err := renderFilterSQL(expr)
		require.NoError(t, err)
		require.Len(t, args, 1)
		instant, ok := args[0].(time.Time)
		require.True(t, ok, "bound: %T", args[0])
		assert.Equal(t, int64(1792339200000), instant.UnixMilli())
	})

	for _, kind := range []string{interfaces.DataType_Datetime, interfaces.DataType_Date, interfaces.DataType_Time} {
		t.Run(kind, func(t *testing.T) {
			field := &interfaces.Property{Name: "value", OriginalName: "value", Type: kind, OriginalType: "timestamp without time zone"}
			literals := []any{"2026-10-19T00:00:00+08:00", "2026-10-19 00:00:00", json.Number("1792339200000"), "1792339200000", "2026-10-19 00:00:00.000000789", "2026-10-19T00:00:00.000000789+08:00"}
			if kind == interfaces.DataType_Date {
				literals = []any{"2026-10-19"}
			}
			if kind == interfaces.DataType_Time {
				literals = []any{"14:30:00.123"}
			}
			for _, op := range []string{"==", "!=", ">", ">=", "<", "<=", "in", "not_in", "range", "out_range", "between"} {
				t.Run(op, func(t *testing.T) {
					for _, literal := range literals {
						value := literal
						count := 1
						switch op {
						case "in", "not_in", "range", "out_range", "between":
							value = []any{literal, literal}
							count = 2
						}
						cfg := &interfaces.FilterCondCfg{Name: "value", Operation: op, Value: value}
						cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"value": field})
						require.NoError(t, err)
						expr, err := (&PostgresqlConnector{}).ConvertFilterCondition(cond)
						require.NoError(t, err)
						sql, args, err := renderFilterSQL(expr)
						require.NoError(t, err)
						assert.NotEmpty(t, sql)
						require.Len(t, args, count)
						for _, bound := range args {
							if kind == interfaces.DataType_Datetime {
								timestamp, ok := bound.(time.Time)
								require.True(t, ok, "bound: %T", bound)
								assert.Equal(t, "2026-10-19 00:00:00", timestamp.Format(time.DateTime))
								wantNanos := 0
								if literal == "2026-10-19 00:00:00.000000789" || literal == "2026-10-19T00:00:00.000000789+08:00" {
									wantNanos = 789
								}
								assert.Equal(t, wantNanos, timestamp.Nanosecond())
								assert.Equal(t, int64(1792339200000), timestamp.UnixMilli())
							} else {
								assert.Equal(t, literal, bound)
							}
						}
						assert.Equal(t, value, cfg.Value, "stored condition must retain its original literal")
					}
				})
			}
		})
	}
}
