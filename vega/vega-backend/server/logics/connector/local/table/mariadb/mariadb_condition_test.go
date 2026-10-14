// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package mariadb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/vega/vega-backend/server/logics/filter_condition"
)

func TestMariaDBConnectorConvertFilterConditionEqual(t *testing.T) {
	t.Run("convert equal const", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "==", "alice")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` = ?" {
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
		if sql != "`created_at` = FROM_UNIXTIME(?/1000)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != int64(1785295334428) {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("accept numeric string epoch milliseconds", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "==", "1785295334428")
		sql, args := toSQL(t, c, cond)
		if sql != "`created_at` = FROM_UNIXTIME(?/1000)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "1785295334428" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("reject date literal before mysql numeric coercion", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "==", "2026-01-01 00:00:00")
		expr, err := c.ConvertFilterCondition(cond)
		if err == nil {
			t.Fatal("expected non-epoch date value error")
		}
		if expr != nil {
			t.Fatalf("expected nil expression, got %v", expr)
		}
		if !strings.Contains(err.Error(), "requires epoch milliseconds") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("convert equal field to field", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Name:      "name",
			Operation: "==",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Field,
				Value:     "tags",
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		if sql != "`name` = `tags`" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert equal alias column", func(t *testing.T) {
		c := &MariaDBConnector{}
		cfg := &interfaces.FilterCondCfg{
			Name:      "alias_col",
			Operation: "==",
			ValueOptCfg: interfaces.ValueOptCfg{
				ValueFrom: interfaces.ValueFrom_Const,
				Value:     "test",
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, _ := toSQL(t, c, cond)
		// OriginalName t1.col must become the qualified identifier `t1`.`col`.
		if sql != "`t1`.`col` = ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotEqual(t *testing.T) {
	t.Run("convert not equal", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "!=", "bob")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` <> ?" {
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
		if sql != "`created_at` <> FROM_UNIXTIME(?/1000)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != int64(1785295334428) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionGt(t *testing.T) {
	t.Run("convert gt", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", ">", 18)
		sql, args := toSQL(t, c, cond)
		if sql != "`age` > ?" {
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
		if sql != "`age` >= ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert date gte uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", ">=", json.Number("1710000000000"))
		sql, args := toSQL(t, c, cond)
		if sql != "`created_at` >= FROM_UNIXTIME(?/1000)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != int64(1710000000000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionLt(t *testing.T) {
	t.Run("convert lt", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "<", 65)
		sql, _ := toSQL(t, c, cond)
		if sql != "`age` < ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionLte(t *testing.T) {
	t.Run("convert lte", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "<=", 65)
		sql, _ := toSQL(t, c, cond)
		if sql != "`age` <= ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionIn(t *testing.T) {
	t.Run("convert in", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "in", []any{"alice", "bob"})
		sql, args := toSQL(t, c, cond)
		if sql != "`name` IN (?,?)" {
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
		if sql != "`created_at` IN (FROM_UNIXTIME(?/1000), FROM_UNIXTIME(?/1000))" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1785295334428) || args[1] != int64(1785381734428) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionNotIn(t *testing.T) {
	t.Run("convert not in", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "not_in", []any{"alice"})
		sql, _ := toSQL(t, c, cond)
		if sql != "`name` NOT IN (?)" {
			t.Errorf("unexpected SQL: %s", sql)
		}
	})
	t.Run("convert date not in from epoch milliseconds", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "not_in", []any{1785295334428, 1785381734428})
		sql, args := toSQL(t, c, cond)
		if sql != "`created_at` NOT IN (FROM_UNIXTIME(?/1000), FROM_UNIXTIME(?/1000))" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1785295334428) || args[1] != int64(1785381734428) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}

func TestMariaDBDateExpressionsKeepTimeValuesRaw(t *testing.T) {
	t.Run("keeps time strings raw", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "event_time", ">=", "14:30:00")
		sql, args := toSQL(t, c, cond)
		if sql != "`event_time` >= ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "14:30:00" {
			t.Errorf("unexpected args: %v", args)
		}
	})

	t.Run("rejects numeric time values", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "event_time", ">=", float64(1785295334428))
		expr, err := c.ConvertFilterCondition(cond)
		if err == nil {
			t.Fatal("expected numeric time value error")
		}
		if expr != nil {
			t.Fatalf("expected nil expression, got %v", expr)
		}
		if !strings.Contains(err.Error(), "requires a time string") {
			t.Fatalf("unexpected error: %v", err)
		}
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
			if sql != "`created_at` > ?" {
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
		if sql != "`name` LIKE ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "%ali%" {
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
		// Preserve the escaped percent sign in the bound LIKE pattern.
		if !strings.Contains(argStr, `\%`) {
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
		if sql != "`name` IS NULL" {
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
		if sql != "`name` IS NOT NULL" {
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
		if sql != "`name` = ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "" {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionRange(t *testing.T) {
	t.Run("convert range", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "age", "range", []any{18, 65})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`age` >= ?") || !strings.Contains(sql, "`age` <= ?") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 {
			t.Errorf("expected 2 args, got %d", len(args))
		}
	})
	t.Run("convert date range uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`created_at` >= FROM_UNIXTIME(?/1000)") ||
			!strings.Contains(sql, "`created_at` <= FROM_UNIXTIME(?/1000)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionOutRange(t *testing.T) {
	t.Run("convert date out range uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "out_range", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`created_at` < FROM_UNIXTIME(?/1000)") ||
			!strings.Contains(sql, "`created_at` > FROM_UNIXTIME(?/1000)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionBetween(t *testing.T) {
	t.Run("convert date between uses from unix time", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "created_at", "between", []any{1710000000000, 1710003600000})
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`created_at` >= FROM_UNIXTIME(?/1000)") ||
			!strings.Contains(sql, "`created_at` <= FROM_UNIXTIME(?/1000)") {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 2 || args[0] != int64(1710000000000) || args[1] != int64(1710003600000) {
			t.Errorf("unexpected args: %v", args)
		}
	})
}
func TestMariaDBConnectorConvertFilterConditionRegex(t *testing.T) {
	t.Run("convert regex", func(t *testing.T) {
		c := &MariaDBConnector{}
		cond := mustNewCond(t, "name", "regex", "^ali.*")
		sql, args := toSQL(t, c, cond)
		if sql != "`name` REGEXP ?" {
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
		if sql != "`is_active` = ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != true {
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
			{operation: "true", sql: "`flag` <> ?"},
			{operation: "false", sql: "`flag` = ?"},
		} {
			condition, err := filter_condition.NewFilterCondition(context.Background(), &interfaces.FilterCondCfg{Name: "flag", Operation: test.operation}, fields)
			if err != nil {
				t.Fatalf("build %s condition: %v", test.operation, err)
			}
			expression, err := connector.ConvertFilterCondition(condition)
			if err != nil {
				t.Fatalf("convert %s condition: %v", test.operation, err)
			}
			statement, args, err := expression.ToSql()
			if err != nil {
				t.Fatalf("render %s condition: %v", test.operation, err)
			}
			if statement != test.sql || len(args) != 1 || args[0] != 0 {
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
		if sql != "`name` LIKE ?" {
			t.Errorf("unexpected SQL: %s", sql)
		}
		if len(args) != 1 || args[0] != "ali%" {
			t.Errorf("unexpected args: %v", args)
		}
	})
	t.Run("reject field source", func(t *testing.T) {
		field := testFieldsMap()["name"]
		cond := &filter_condition.PrefixCond{
			Cfg:    &interfaces.FilterCondCfg{ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Field}},
			Lfield: field, Value: "ali",
		}
		expr, err := cond.New(context.Background(), &interfaces.FilterCondCfg{Name: "name", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Field, Value: "ali"}}, map[string]*interfaces.Property{"name": field})
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
				{Name: "name", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "alice"}},
				{Name: "age", Operation: ">", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: 18}},
			},
		}
		cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, testFieldsMap())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sql, args := toSQL(t, c, cond)
		if !strings.Contains(sql, "`name` = ?") || !strings.Contains(sql, "`age` > ?") {
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
				{Name: "name", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "alice"}},
				{Name: "name", Operation: "==", ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: "const", Value: "bob"}},
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

func TestSpecialReplacer(t *testing.T) {
	t.Run("special replacer", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{`hello`, `hello`},
			{`%`, `\%`},
			{`_`, `\_`},
			{`'`, `\'`},
			{`\`, `\\\\`},
			{`100%_done`, `100\%\_done`},
		}
		for _, tt := range tests {
			got := likeEscaper.Replace(tt.input)
			if got != tt.expected {
				t.Errorf("Special.Replace(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		}
	})
}

// testFieldsMap defines the scalar fields and alias used by condition tests.
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
	sql, args, err := sqlizer.ToSql()
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
		ValueOptCfg: interfaces.ValueOptCfg{
			ValueFrom: interfaces.ValueFrom_Const,
			Value:     value,
		},
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
			cfg := &interfaces.FilterCondCfg{Name: "created", Operation: filter_condition.OperationCurrent, ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: tc.interval}}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, fields)
			if err != nil {
				t.Fatal(err)
			}
			expr, err := (&MariaDBConnector{}).ConvertFilterConditionCurrent(cond)
			if err != nil {
				t.Fatal(err)
			}
			sql, args, err := expr.ToSql()
			if err != nil {
				t.Fatal(err)
			}
			want := "DATE_FORMAT(`created_at`, '" + tc.format + "') = DATE_FORMAT(NOW(), '" + tc.format + "')"
			if sql != want || len(args) != 0 {
				t.Fatalf("got SQL %q args %v, want %q without parameters", sql, args, want)
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
			cfg := &interfaces.FilterCondCfg{Name: "created", Operation: filter_condition.OperationBefore, ValueOptCfg: interfaces.ValueOptCfg{ValueFrom: interfaces.ValueFrom_Const, Value: []any{tc.value, "day"}}}
			cond, err := filter_condition.NewFilterCondition(context.Background(), cfg, map[string]*interfaces.Property{"created": field})
			if err != nil {
				t.Fatal(err)
			}
			expr, err := (&MariaDBConnector{}).ConvertFilterConditionBefore(cond)
			if !tc.valid {
				if err == nil || expr != nil {
					t.Fatalf("expected rejection, got expression %v error %v", expr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			sql, args, err := expr.ToSql()
			if err != nil {
				t.Fatal(err)
			}
			if sql != "`created` < DATE_SUB(NOW(), INTERVAL ? DAY)" || len(args) != 1 || args[0] != int64(2) {
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
				expr, err := (&MariaDBConnector{}).ConvertFilterConditionBefore(cond)
				if !tc.valid {
					if err == nil || expr != nil {
						t.Fatalf("expected unit rejection, got expression %v error %v", expr, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				sql, args, err := expr.ToSql()
				if err != nil {
					t.Fatal(err)
				}
				want := "`created` < DATE_SUB(NOW(), INTERVAL ? " + strings.ToUpper(tc.unit) + ")"
				if sql != want || len(args) != 1 || args[0] != int64(2) {
					t.Fatalf("unexpected SQL %q args %v", sql, args)
				}
			})
		}
	})

}
