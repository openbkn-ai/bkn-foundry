// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	sb "github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces/sqlbuilder"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestMariaDBConnectorConvertFilterConditionEqual(t *testing.T) {
	t.Run("convert equal const", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "==", "alice")
		sql, args := toSQL(t, c, cond)
		if sql != "(`name` = ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "alice" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("convert date equal from epoch milliseconds", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "==", float64(1785295334428))
		sql, args := toSQL(t, c, cond)
		if sql != "(`created_at` = ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != time.UnixMilli(1785295334428).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("accept numeric string epoch milliseconds", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "==", "1785295334428")
		sql, args := toSQL(t, c, cond)
		if sql != "(`created_at` = ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != time.UnixMilli(1785295334428).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("accept complete datetime without numeric coercion", func(t *testing.T) {
		t.Setenv("TZ", "UTC")
		sql, args := toSQL(t, &MariaDBConnector{}, mustNewCond(t, "created_at", "==", "2026-01-01 00:00:00"))
		assert.Equal(t, "(`created_at` = ?)", sql)
		assert.Equal(t, []any{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, args)
	})
	t.Run("convert equal field to field", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "==",
			ValueFrom: interfaces.ValueFrom_Field,
			Value:     "tags",
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		if sql != "(`name` = `tags`)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert equal alias column", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Name:      "alias_col",
			Operation: "==",
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     "test",
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		// OriginalName t1.col must become the qualified identifier `t1`.`col`.
		if sql != "(`t1`.`col` = ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotEqual(t *testing.T) {
	t.Run("convert not equal", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "!=", "bob")
		sql, args := toSQL(t, c, cond)
		if sql != "(`name` <> ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "bob" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("convert date not equal from epoch milliseconds", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "!=", float64(1785295334428))
		sql, args := toSQL(t, c, cond)
		if sql != "(`created_at` <> ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != time.UnixMilli(1785295334428).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionGt(t *testing.T) {
	t.Run("convert gt", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", ">", 18)
		sql, args := toSQL(t, c, cond)
		if sql != "(`age` > ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != 18 {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionGte(t *testing.T) {
	t.Run("convert gte", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", ">=", 18)
		sql, _ := toSQL(t, c, cond)
		if sql != "(`age` >= ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert date gte binds normalized datetime", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", ">=", json.Number("1710000000000"))
		sql, args := toSQL(t, c, cond)
		if sql != "(`created_at` >= ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != time.UnixMilli(1710000000000).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionLt(t *testing.T) {
	t.Run("convert lt", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "<", 65)
		sql, _ := toSQL(t, c, cond)
		if sql != "(`age` < ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionLte(t *testing.T) {
	t.Run("convert lte", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "<=", 65)
		sql, _ := toSQL(t, c, cond)
		if sql != "(`age` <= ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionIn(t *testing.T) {
	t.Run("convert in", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "in", []any{"alice", "bob"})
		sql, args := toSQL(t, c, cond)
		if sql != "(`name` IN (?, ?))" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d", len(args))
		}
	})
	t.Run("convert date in from epoch milliseconds", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "in", []any{1785295334428, 1785381734428})
		sql, args := toSQL(t, c, cond)
		if sql != "(`created_at` IN (?, ?))" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != time.UnixMilli(1785295334428).UTC() || args[1] != time.UnixMilli(1785381734428).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotIn(t *testing.T) {
	t.Run("convert not in", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "not_in", []any{"alice"})
		sql, _ := toSQL(t, c, cond)
		if sql != "(`name` NOT IN (?))" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert date not in from epoch milliseconds", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "not_in", []any{1785295334428, 1785381734428})
		sql, args := toSQL(t, c, cond)
		if sql != "(`created_at` NOT IN (?, ?))" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != time.UnixMilli(1785295334428).UTC() || args[1] != time.UnixMilli(1785381734428).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
}

func TestMariaDBDateExpressionsKeepTimeValuesRaw(t *testing.T) {
	t.Run("keeps time strings raw", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "event_time", ">=", "14:30:00")
		sql, args := toSQL(t, c, cond)
		if sql != "(`event_time` >= ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "14:30:00" {
			t.Errorf("unexpected args: %v", args)
		}
	})

	t.Run("rejects numeric time values before conversion", func(t *testing.T) {
		cond, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{
			Name: "event_time", Operation: ">=", Value: float64(1785295334428),
		}, testFieldsMap())
		require.ErrorContains(t, err, "invalid time value")
		assert.Nil(t, cond)
	})
}

func TestMariaDBDateExpressionsKeepCursorTimestampsNative(t *testing.T) {
	c := &MariaDBConnector{}
	wantMillis := int64(1785295334428)
	wantTime := time.UnixMilli(wantMillis).UTC()

	for name, value := range map[string]any{
		"time.Time": wantTime,
		"RFC3339":   wantTime.Format(time.RFC3339Nano),
	} {
		t.Run(name, func(t *testing.T) {
			cond := mustNewCond(t, "created_at", ">", value)
			sql, args := toSQL(t, c, cond)
			if sql != "(`created_at` > ?)" {
				t.Errorf("unexpected SQL: %s", sql)
			}
			if len(args) != 1 || !args[0].(time.Time).Equal(wantTime) {
				t.Errorf("unexpected args: %v", args)
			}
		})
	}
}
func TestMariaDBConnectorConvertFilterConditionLike(t *testing.T) {
	t.Run("convert like", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "like", "ali")
		sql, args := toSQL(t, c, cond)
		if sql != "(`name` LIKE CONCAT('%', REPLACE(REPLACE(REPLACE(?, '!', '!!'), '%', '!%'), '_', '!_'), '%') ESCAPE '!')" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "ali" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("convert like special chars", func(t *testing.T) {
		c := &MariaDBConnector{}
		// The caller escapes a literal percent sign; NewFilterCondition rejects
		// unescaped percent wildcards before SQL conversion.
		cond := mustNewCond(t, "name", "like", `100\%`)
		_, args := toSQL(t, c, cond)
		argStr, ok := args[0].(string)
		if !ok {
			t.Fatalf("expected string arg, got %T", args[0])
		}
		// The bound value stays literal; the renderer escapes LIKE metacharacters using !.
		if argStr != "100%" {
			t.Errorf("expected escaped %%, got %s", argStr)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNull(t *testing.T) {
	t.Run("convert null", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "name", Operation: "null"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, _ := toSQL(t, c, cond)
		if sql != "(`name` IS NULL)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotNull(t *testing.T) {
	t.Run("convert not null", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "name", Operation: "not_null"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, _ := toSQL(t, c, cond)
		if sql != "(`name` IS NOT NULL)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionEmpty(t *testing.T) {
	t.Run("convert empty", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "name", Operation: "empty"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, args := toSQL(t, c, cond)
		if sql != "(`name` = '')" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 0 {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionRange(t *testing.T) {
	t.Run("convert range", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "range", []any{18, 65})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "(`age` >= ?)") || !strings.Contains(sql, "(`age` <= ?)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d", len(args))
		}
	})
	t.Run("convert date range binds normalized datetime", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "(`created_at` >= ?)") ||
			!strings.Contains(sql, "(`created_at` <= ?)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != time.UnixMilli(1710000000000).UTC() || args[1] != time.UnixMilli(1710003600000).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionOutRange(t *testing.T) {
	t.Run("convert date out range binds normalized datetime", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "out_range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "(`created_at` < ?)") ||
			!strings.Contains(sql, "(`created_at` > ?)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != time.UnixMilli(1710000000000).UTC() || args[1] != time.UnixMilli(1710003600000).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionBetween(t *testing.T) {
	t.Run("convert date between binds normalized datetime", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "between", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "(`created_at` >= ?)") ||
			!strings.Contains(sql, "(`created_at` <= ?)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != time.UnixMilli(1710000000000).UTC() || args[1] != time.UnixMilli(1710003600000).UTC() {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionRegex(t *testing.T) {
	t.Run("convert regex", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "regex", "^ali.*")
		sql, args := toSQL(t, c, cond)
		if sql != "(`name` REGEXP ?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "^ali.*" {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionTrue(t *testing.T) {
	t.Run("convert true", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{Name: "is_active", Operation: "true"}
		cond, _ := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		sql, args := toSQL(t, c, cond)
		if sql != "(`is_active` = TRUE)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 0 {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("tinyint flag treats nonzero as true", func(t *testing.T) {
		field := &interfaces.Property{Name: "flag", OriginalName: "flag", OriginalType: "tinyint(1)", Type: interfaces.DataType_Integer}
		fields := map[string]*interfaces.Property{"flag": field}
		connector := &MariaDBConnector{}
		for _, test := range []struct {
			operation string
			sql       string
		}{
			{operation: "true", sql: "(`flag` <> 0)"},
			{operation: "false", sql: "(`flag` = 0)"},
		} {
			condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "flag", Operation: test.operation}, fields)
			if err != nil {
				t.Fatalf("build %s condition: %v", test.operation, err)
			}
			expression, err := connector.ConvertFilterCondition(condition)
			if err != nil {
				t.Fatalf("convert %s condition: %v", test.operation, err)
			}
			statement, args, err := render(expression)
			if err != nil {
				t.Fatalf("render %s condition: %v", test.operation, err)
			}
			if statement != test.sql || len(args) != 0 {
				t.Fatalf("%s condition: sql=%q args=%v", test.operation, statement, args)
			}
		}
	})
	t.Run("rejects ordinary integer", func(t *testing.T) {
		field := &interfaces.Property{Name: "count", OriginalName: "count", OriginalType: "int(11)", Type: interfaces.DataType_Integer}
		fields := map[string]*interfaces.Property{"count": field}
		for _, operation := range []string{"true", "false"} {
			condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "count", Operation: operation}, fields)
			if err != nil {
				t.Fatalf("build %s condition: %v", operation, err)
			}
			_, err = (&MariaDBConnector{}).ConvertFilterCondition(condition)
			if err == nil {
				t.Fatalf("expected MariaDB to reject %s for INT", operation)
			}
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionPrefix(t *testing.T) {
	t.Run("convert prefix", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "prefix", "ali")
		sql, args := toSQL(t, c, cond)
		if sql != "(`name` LIKE ? ESCAPE '!')" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "ali%" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("reject field source", func(t *testing.T) {
		field := testFieldsMap()["name"]
		cond := &filter_condition.PrefixCond{
			Cfg:    &interfaces.FilterCondCfg{ValueFrom: interfaces.ValueFrom_Field},
			Lfield: field, Value: "ali",
		}
		expr, err := cond.New(context.Background(), &interfaces.FilterCondCfg{Name: "name", ValueFrom: interfaces.ValueFrom_Field, Value: "ali"}, map[string]*interfaces.Property{"name": field})
		if expr != nil || err == nil || !strings.Contains(err.Error(), "value_from") {
			t.Fatalf("expected constant-source error and nil expression, got expr=%v err=%v", expr, err)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionAnd(t *testing.T) {
	t.Run("convert and", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Operation: "and",
			SubConds: []*interfaces.FilterCondCfg{
				{Name: "name", Operation: "==", ValueFrom: "const", Value: "alice"},
				{Name: "age", Operation: ">", ValueFrom: "const", Value: 18},
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "(`name` = ?)") || !strings.Contains(sql, "(`age` > ?)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if !strings.Contains(sql, " AND ") {
			t.Errorf("expected AND in SQL: %s", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d", len(args))
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionOr(t *testing.T) {
	t.Run("convert or", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Operation: "or",
			SubConds: []*interfaces.FilterCondCfg{
				{Name: "name", Operation: "==", ValueFrom: "const", Value: "alice"},
				{Name: "name", Operation: "==", ValueFrom: "const", Value: "bob"},
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		if !strings.Contains(sql, " OR ") {
			t.Errorf("expected OR in SQL: %s", sql)
		}
	})
}

func testFieldsMap() map[string]*interfaces.Property {
	return map[string]*interfaces.Property{
		"name":       {Name: "name", OriginalName: "name", Type: interfaces.DataType_String},
		"age":        {Name: "age", OriginalName: "age", Type: interfaces.DataType_Integer},
		"score":      {Name: "score", OriginalName: "score", Type: interfaces.DataType_Float},
		"created_at": {Name: "created_at", OriginalName: "created_at", Type: interfaces.DataType_Datetime},
		"event_time": {Name: "event_time", OriginalName: "event_time", OriginalType: "time", Type: interfaces.DataType_Time},
		"is_active":  {Name: "is_active", OriginalName: "is_active", Type: interfaces.DataType_Boolean},
		"tags":       {Name: "tags", OriginalName: "tags", Type: interfaces.DataType_Text},
		"alias_col":  {Name: "alias_col", OriginalName: "t1.col", Type: interfaces.DataType_String},
	}
}

// toSQL compiles a condition and fails the test if conversion or SQL serialization fails.
func toSQL(t *testing.T, connector *MariaDBConnector, cond interfaces.FilterCondition) (string, []interface{}) {
	t.Helper()
	sqlizer, err := connector.ConvertFilterCondition(cond)
	if err != nil {
		t.Fatalf("ConvertFilterCondition failed: %v", err)
	}
	sql, args, err := render(sqlizer)
	if err != nil {
		t.Fatalf("ToSql failed: %v", err)
	}
	return sql, args
}

// mustNewCond constructs a validated constant condition against the test schema.
func mustNewCond(t *testing.T, name, op string, value any) interfaces.FilterCondition {
	t.Helper()
	cfg := &interfaces.FilterCondCfg{
		Name:      name,
		Operation: op,
		ValueFrom: interfaces.ValueFrom_Const,
		Value:     value,
	}
	cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
	if err != nil {
		t.Fatalf("NewFilterCondition(%s, %s) failed: %v", name, op, err)
	}
	return cond
}

func TestMariaDBConnectorConvertFilterConditionCurrent(t *testing.T) {
	for _, tc := range []struct{ interval, format string }{
		{"year", "%Y"}, {"month", "%Y-%m"}, {"week", "%x-%v"},
		{"day", "%Y-%m-%d"}, {"hour", "%Y-%m-%d %H"}, {"minute", "%Y-%m-%d %H:%i"},
	} {
		t.Run(tc.interval, func(t *testing.T) {
			fields := map[string]*interfaces.Property{"created": {Name: "created", OriginalName: "created_at", Type: interfaces.DataType_Datetime}}
			cfg := &interfaces.FilterCondCfg{Name: "created", Operation: filter_condition.OperationCurrent, ValueFrom: interfaces.ValueFrom_Const, Value: tc.interval}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
			if err != nil {
				t.Fatal(err)
			}
			expr, err := (&MariaDBConnector{}).ConvertFilterCondition(cond)
			if err != nil {
				t.Fatal(err)
			}
			sql, args, err := render(expr)
			if err != nil {
				t.Fatal(err)
			}
			want := "(DATE_FORMAT(`created_at`, '" + tc.format + "') = DATE_FORMAT(NOW(), '" + tc.format + "'))"
			if sql != want || len(args) != 0 {
				t.Fatalf("got SQL %q args %v, want %q with the same format twice", sql, args, want)
			}
		})
	}
}

func TestMariaDBConnectorConvertFilterConditionBefore(t *testing.T) {
	field := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"positive int", 2, true}, {"positive float integer", float64(2), true},
		{"positive json integer", json.Number("2"), true}, {"zero", float64(0), false},
		{"negative", float64(-1), false}, {"fractional", float64(1.5), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &interfaces.FilterCondCfg{Name: "created", Operation: filter_condition.OperationBefore, ValueFrom: interfaces.ValueFrom_Const, Value: []any{tc.value, "day"}}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"created": field})
			if err != nil {
				t.Fatal(err)
			}
			expr, err := (&MariaDBConnector{}).ConvertFilterCondition(cond)
			if !tc.valid {
				if err == nil || expr != nil {
					t.Fatalf("expected rejection, got expression %v error %v", expr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sql, args, err := render(expr)
			if err != nil {
				t.Fatal(err)
			}
			if sql != "(`created` < DATE_SUB(NOW(), INTERVAL ? DAY))" || len(args) != 1 || args[0] != int64(2) {
				t.Fatalf("unexpected SQL %q args %v", sql, args)
			}
		})
	}
	t.Run("unit whitelist", func(t *testing.T) {
		for _, tc := range []struct {
			unit  string
			valid bool
		}{
			{"second", true}, {"microsecond", true}, {"DAY", true}, {"MiNuTe", true},
			{"day_second", false}, {"day) OR 1=1 --", false}, {"unknown", false},
		} {
			t.Run(tc.unit, func(t *testing.T) {
				cond := &filter_condition.BeforeCond{Lfield: field, Value: []any{int64(2), tc.unit}}
				expr, err := (&MariaDBConnector{}).ConvertFilterCondition(cond)
				if !tc.valid {
					if err == nil || expr != nil {
						t.Fatalf("expected unit rejection, got expression %v error %v", expr, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				sql, args, err := render(expr)
				if err != nil {
					t.Fatal(err)
				}
				want := "(`created` < DATE_SUB(NOW(), INTERVAL ? " + strings.ToUpper(tc.unit) + "))"
				if sql != want || len(args) != 1 || args[0] != int64(2) {
					t.Fatalf("unexpected SQL %q args %v", sql, args)
				}
			})
		}
	})

}

func TestMariaDBConnectorTemporalInputContract(t *testing.T) {
	t.Setenv("TZ", "Asia/Shanghai")
	for _, kind := range []string{interfaces.DataType_Datetime, interfaces.DataType_Date, interfaces.DataType_Time} {
		t.Run(kind, func(t *testing.T) {
			field := &interfaces.Property{Name: "value", OriginalName: "value", Type: kind, OriginalType: "datetime"}
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
						expr, err := (&MariaDBConnector{}).ConvertFilterCondition(cond)
						require.NoError(t, err)
						sql, args, err := render(expr)
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

func TestMariaDBConnectorConvertFilterCondition(t *testing.T) {
	field := &interfaces.Property{Name: "x", Type: interfaces.DataType_String}
	cfg := &interfaces.FilterCondCfg{ValueFrom: interfaces.ValueFrom_Const}
	connector := &MariaDBConnector{}
	for _, tc := range []struct {
		name      string
		condition interfaces.FilterCondition
		sql       string
		args      []any
	}{
		{"equal", &filter_condition.EqualCond{Cfg: cfg, Lfield: field, Value: "a"}, "(`x` = ?)", []any{"a"}},
		{"null equality", &filter_condition.EqualCond{Cfg: cfg, Lfield: field}, "(`x` IS NULL)", nil},
		{"null inequality", &filter_condition.NotEqualCond{Cfg: cfg, Lfield: field}, "(`x` IS NOT NULL)", nil},
		{"field comparison", &filter_condition.GtCond{Cfg: &interfaces.FilterCondCfg{ValueFrom: interfaces.ValueFrom_Field}, Lfield: field, Rfield: &interfaces.Property{Name: "y"}}, "(`x` > `y`)", nil},
		{"in", &filter_condition.InCond{Lfield: field, Value: []any{"a", nil}}, "(`x` IN (?, ?))", []any{"a", nil}},
		{"empty in", &filter_condition.InCond{Lfield: field}, "(1=0)", nil},
		{"empty not in", &filter_condition.NotInCond{Lfield: field}, "(1=1)", nil},
		{"inclusive range", &filter_condition.RangeCond{Lfield: field, Value: []any{1, 2}}, "((`x` >= ?) AND (`x` <= ?))", []any{1, 2}},
		{"outside range", &filter_condition.OutRangeCond{Lfield: field, Value: []any{1, 2}}, "((`x` < ?) OR (`x` > ?))", []any{1, 2}},
		{"regex", &filter_condition.RegexCond{Lfield: field, Value: "a.*'"}, "(`x` REGEXP ?)", []any{"a.*'"}},
		{"CSV contains", &filter_condition.ContainCond{Lfield: field, Value: []any{"a", "b"}}, "((FIND_IN_SET(?, `x`) > 0) AND (FIND_IN_SET(?, `x`) > 0))", []any{"a", "b"}},
		{"prefix literal", &filter_condition.PrefixCond{Lfield: field, Value: "a%_!"}, "(`x` LIKE ? ESCAPE '!')", []any{"a!%!_!!%"}},
		{"current month", &filter_condition.CurrentCond{Lfield: field, Value: filter_condition.CurrentMonth}, "(DATE_FORMAT(`x`, '%Y-%m') = DATE_FORMAT(NOW(), '%Y-%m'))", nil},
		{"before", &filter_condition.BeforeCond{Lfield: field, Value: []any{int64(2), "day"}}, "(`x` < DATE_SUB(NOW(), INTERVAL ? DAY))", []any{int64(2)}},
		{"tinyint true", &filter_condition.TrueCond{Lfield: &interfaces.Property{Name: "x", Type: interfaces.DataType_Integer, OriginalType: "TINYINT(1)"}}, "(`x` <> 0)", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := connector.ConvertFilterCondition(tc.condition)
			require.NoError(t, err)
			sql, args, err := render(node)
			require.NoError(t, err)
			assert.Equal(t, tc.sql, sql)
			assert.Equal(t, tc.args, args)
		})
	}
	t.Run("parsed LIKE is literal and parameters never become SQL", func(t *testing.T) {
		condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "x", Operation: filter_condition.OperationLike, Value: `O'Reilly\%_!`}, map[string]*interfaces.Property{"x": field})
		require.NoError(t, err)
		node, err := connector.ConvertFilterCondition(condition)
		require.NoError(t, err)
		sql, args, err := render(node)
		require.NoError(t, err)
		assert.Contains(t, sql, "LIKE CONCAT")
		assert.NotContains(t, sql, "O'Reilly")
		assert.Equal(t, []any{"O'Reilly%_!"}, args)
	})
	t.Run("temporal parsing preserves the server timezone and precision", func(t *testing.T) {
		t.Setenv("TZ", "UTC")
		field := &interfaces.Property{Name: "created", Type: interfaces.DataType_Datetime}
		condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "created", Operation: filter_condition.OperationEqual, Value: "2026-10-27T12:30:00.123456Z"}, map[string]*interfaces.Property{"created": field})
		require.NoError(t, err)
		node, err := connector.ConvertFilterCondition(condition)
		require.NoError(t, err)
		sql, args, err := render(node)
		require.NoError(t, err)
		assert.Equal(t, "(`created` = ?)", sql)
		require.Len(t, args, 1)
		value, ok := args[0].(time.Time)
		require.True(t, ok)
		assert.True(t, value.Equal(time.Date(2026, 10, 27, 12, 30, 0, 123456000, time.UTC)))
	})
	t.Run("unsupported fulltext and interval syntax are rejected", func(t *testing.T) {
		for _, condition := range []interfaces.FilterCondition{&filter_condition.MatchCond{}, &filter_condition.BeforeCond{Lfield: field, Value: []any{int64(1), "day); DROP TABLE t"}}} {
			node, err := connector.ConvertFilterCondition(condition)
			assert.Error(t, err)
			assert.Nil(t, node)
		}
	})
	t.Run("composite conditions retain null semantics", func(t *testing.T) {
		condition := &filter_condition.AndCond{SubConds: []interfaces.FilterCondition{&filter_condition.NullCond{Lfield: field}, &filter_condition.OrCond{SubConds: []interfaces.FilterCondition{&filter_condition.EmptyCond{Lfield: field}, &filter_condition.NotExistCond{Lfield: field}}}}}
		node, err := connector.ConvertFilterCondition(condition)
		require.NoError(t, err)
		sql, args, err := render(node)
		require.NoError(t, err)
		assert.Equal(t, "((`x` IS NULL) AND ((`x` = '') OR (`x` IS NULL)))", sql)
		assert.Equal(t, []any(nil), args)
		assert.IsType(t, &sb.And{}, node)
	})
}

func TestSqlColumnName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"name", "`name`"}, {"t1.name", "`t1`.`name`"}, {"col`name", "`col``name`"}, {" t1 . name ", "`t1`.`name`"},
	} {
		sql, args, err := render(sqlColumnName(tc.name))
		require.NoError(t, err)
		assert.Equal(t, tc.want, sql)
		assert.Empty(t, args)
	}
	sql, args, err := render(sqlColumnName(""))
	require.Error(t, err)
	assert.Empty(t, sql)
	assert.Nil(t, args)
}
